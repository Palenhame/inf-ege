package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RedisFrameCacher defines the subset of redis operations needed by Hub.
type RedisFrameCacher interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
}

// TelemetryBatcherEnqueuer interface for enqueueing telemetry batches into background batcher.
type TelemetryBatcherEnqueuer interface {
	EnqueueBatch(sessionID uuid.UUID, assignmentID uuid.UUID, userID uuid.UUID, events []TelemetryEventItem)
}

// TeacherAuthorizer allows pluggable authorization for watching assignment matrices and live code streams.
type TeacherAuthorizer interface {
	CanWatchAssignment(ctx context.Context, teacherID uuid.UUID, assignmentID uuid.UUID) (bool, error)
	CanWatchStudentLive(ctx context.Context, teacherID uuid.UUID, sessionID uuid.UUID) (bool, error)
}

// DefaultTeacherAuthorizer checks if user role is "teacher" or "superuser".
type DefaultTeacherAuthorizer struct{}

func (d *DefaultTeacherAuthorizer) CanWatchAssignment(ctx context.Context, teacherID uuid.UUID, assignmentID uuid.UUID) (bool, error) {
	return true, nil
}

func (d *DefaultTeacherAuthorizer) CanWatchStudentLive(ctx context.Context, teacherID uuid.UUID, sessionID uuid.UUID) (bool, error) {
	return true, nil
}

// StudentSessionPresence tracks live in-memory presence and telemetry counters for active sessions.
type StudentSessionPresence struct {
	AssignmentID   uuid.UUID
	UserID         uuid.UUID
	SessionID      uuid.UUID
	Online         bool
	TabFocused     bool
	CurrentTaskID  *int
	IDEOpen        bool
	BlurCount      int
	PasteCount     int
	LastActivityAt time.Time
}

// ToPatch creates MatrixPresencePatchPayload from current presence state.
func (p *StudentSessionPresence) ToPatch() *MatrixPresencePatchPayload {
	return &MatrixPresencePatchPayload{
		AssignmentID:   p.AssignmentID,
		UserID:         p.UserID,
		SessionID:      p.SessionID,
		Online:         p.Online,
		TabFocused:     p.TabFocused,
		CurrentTaskID:  p.CurrentTaskID,
		IDEOpen:        p.IDEOpen,
		BlurCount:      p.BlurCount,
		PasteCount:     p.PasteCount,
		LastActivityAt: p.LastActivityAt,
	}
}

// Hub manages multi-tenant WebSocket routing, Level 1 matrix subscriptions,
// Level 2 on-demand student code streaming, and client lifecycles.
type Hub struct {
	mu sync.RWMutex

	// Multi-tenant routing maps
	clients             map[*Client]struct{}
	assignmentWatchers  map[uuid.UUID]map[*Client]struct{} // assignment_id -> teacher clients
	liveStudentWatchers map[uuid.UUID]map[*Client]struct{} // session_id -> teacher clients
	studentConnections  map[uuid.UUID]*Client              // session_id -> student client

	// Session state and cached frames
	sessionPresence    map[uuid.UUID]*StudentSessionPresence
	sessionAssignments map[uuid.UUID]uuid.UUID // session_id -> assignment_id
	liveCodeFrames     map[uuid.UUID][]byte    // in-memory cached latest frame: session_id -> raw json

	redisClient     RedisFrameCacher
	batcher         TelemetryBatcherEnqueuer
	authorizer      TeacherAuthorizer
	sessionResolver SessionAssignmentResolver
}

// NewHub constructs a new WebSocket Hub.
func NewHub(redisClient RedisFrameCacher, batcher TelemetryBatcherEnqueuer, authorizer TeacherAuthorizer) *Hub {
	if authorizer == nil {
		authorizer = &DefaultTeacherAuthorizer{}
	}
	return &Hub{
		clients:             make(map[*Client]struct{}),
		assignmentWatchers:  make(map[uuid.UUID]map[*Client]struct{}),
		liveStudentWatchers: make(map[uuid.UUID]map[*Client]struct{}),
		studentConnections:  make(map[uuid.UUID]*Client),
		sessionPresence:    make(map[uuid.UUID]*StudentSessionPresence),
		sessionAssignments: make(map[uuid.UUID]uuid.UUID),
		liveCodeFrames:     make(map[uuid.UUID][]byte),
		redisClient:         redisClient,
		batcher:             batcher,
		authorizer:          authorizer,
	}
}

// Register adds a newly connected client to the Hub.
func (h *Hub) Register(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[client] = struct{}{}
}

// Unregister cleanly removes a client and cleans up all its active subscriptions.
func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.clients, client)

	// Clean up Level 1 assignment matrix subscriptions
	for _, assignmentID := range client.WatchedAssignments() {
		if watchers, ok := h.assignmentWatchers[assignmentID]; ok {
			delete(watchers, client)
			if len(watchers) == 0 {
				delete(h.assignmentWatchers, assignmentID)
			}
		}
	}

	// Clean up Level 2 student live code stream subscriptions
	for _, sessionID := range client.WatchedStudents() {
		if watchers, ok := h.liveStudentWatchers[sessionID]; ok {
			delete(watchers, client)
			if len(watchers) == 0 {
				delete(h.liveStudentWatchers, sessionID)
				// When last teacher unwatches -> send server.live_stream_toggle{enabled: false} to student
				if studentConn, exists := h.studentConnections[sessionID]; exists {
					_ = studentConn.SendFormatted(TypeServerLiveStreamToggle, ServerLiveStreamTogglePayload{
						SessionID: sessionID,
						Enabled:   false,
					})
				}
			}
		}
	}

	// Clean up student session if this was a student connection
	if sessionID := client.SessionID(); sessionID != nil {
		sid := *sessionID
		if existing, ok := h.studentConnections[sid]; ok && existing == client {
			delete(h.studentConnections, sid)

			// Update presence to offline and broadcast
			if presence, exists := h.sessionPresence[sid]; exists {
				presence.Online = false
				presence.LastActivityAt = time.Now().UTC()
				h.broadcastPresencePatchLocked(presence.AssignmentID, presence.ToPatch())
			}
		}
	}
}

// HandleMessage parses and routes incoming frames based on message type.
func (h *Hub) HandleMessage(client *Client, env Envelope) {
	switch env.Type {
	case TypeStudentSessionJoin:
		h.handleStudentSessionJoin(client, env.Data)
	case TypeStudentTelemetryBatch:
		h.handleStudentTelemetryBatch(client, env.Data)
	case TypeTeacherWatchAssignment:
		h.handleTeacherWatchAssignment(client, env.Data)
	case TypeTeacherUnwatchAssignment:
		h.handleTeacherUnwatchAssignment(client, env.Data)
	case TypeTeacherWatchStudentLive:
		h.handleTeacherWatchStudentLive(client, env.Data)
	case TypeTeacherUnwatchStudentLive:
		h.handleTeacherUnwatchStudentLive(client, env.Data)
	case TypeStudentLiveCodeFrame:
		h.handleStudentLiveCodeFrame(client, env.Data)
	default:
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "UNKNOWN_EVENT_TYPE",
			Message: fmt.Sprintf("Unsupported event type: %s", env.Type),
		})
	}
}

// handleStudentSessionJoin binds student connection to session_id and emits presence.
func (h *Hub) handleStudentSessionJoin(client *Client, data json.RawMessage) {
	var payload StudentSessionJoinPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "INVALID_PAYLOAD",
			Message: "Cannot parse student.session_join payload: " + err.Error(),
		})
		return
	}

	h.mu.RLock()
	assignmentID := h.sessionAssignments[payload.SessionID]
	resolver := h.sessionResolver
	h.mu.RUnlock()

	if assignmentID == uuid.Nil && payload.AssignmentID != uuid.Nil {
		assignmentID = payload.AssignmentID
	}
	if assignmentID == uuid.Nil && resolver != nil {
		assignmentID = resolver(payload.SessionID)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if assignmentID != uuid.Nil {
		h.sessionAssignments[payload.SessionID] = assignmentID
	} else {
		assignmentID = h.sessionAssignments[payload.SessionID]
	}

	client.SetSessionID(payload.SessionID)
	h.studentConnections[payload.SessionID] = client

	// Check if any teachers are already watching this student live
	if watchers, ok := h.liveStudentWatchers[payload.SessionID]; ok && len(watchers) > 0 {
		_ = client.SendFormatted(TypeServerLiveStreamToggle, ServerLiveStreamTogglePayload{
			SessionID:  payload.SessionID,
			Enabled:    true,
			DebounceMS: 350,
		})
	}

	// Update presence
	presence, exists := h.sessionPresence[payload.SessionID]
	if !exists {
		presence = &StudentSessionPresence{
			AssignmentID:   assignmentID,
			UserID:         client.UserID,
			SessionID:      payload.SessionID,
			Online:         true,
			TabFocused:     true,
			CurrentTaskID:  &payload.CurrentTaskID,
			LastActivityAt: time.Now().UTC(),
		}
		h.sessionPresence[payload.SessionID] = presence
	} else {
		if presence.AssignmentID == uuid.Nil && assignmentID != uuid.Nil {
			presence.AssignmentID = assignmentID
		}
		presence.Online = true
		presence.CurrentTaskID = &payload.CurrentTaskID
		presence.LastActivityAt = time.Now().UTC()
	}

	if presence.AssignmentID != uuid.Nil {
		h.broadcastPresencePatchLocked(presence.AssignmentID, presence.ToPatch())
	}
}

// handleStudentTelemetryBatch receives telemetry events from student and sends to batcher.
func (h *Hub) handleStudentTelemetryBatch(client *Client, data json.RawMessage) {
	var payload StudentTelemetryBatchPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "INVALID_PAYLOAD",
			Message: "Cannot parse student.telemetry_batch payload: " + err.Error(),
		})
		return
	}

	h.mu.RLock()
	assignmentID := h.sessionAssignments[payload.SessionID]
	resolver := h.sessionResolver
	h.mu.RUnlock()

	if assignmentID == uuid.Nil && resolver != nil {
		if resolved := resolver(payload.SessionID); resolved != uuid.Nil {
			assignmentID = resolved
			h.mu.Lock()
			h.sessionAssignments[payload.SessionID] = assignmentID
			h.mu.Unlock()
		}
	}

	if h.batcher != nil {
		h.batcher.EnqueueBatch(payload.SessionID, assignmentID, client.UserID, payload.Events)
	}
}

// handleTeacherWatchAssignment subscribes teacher to Level 1 matrix updates.
func (h *Hub) handleTeacherWatchAssignment(client *Client, data json.RawMessage) {
	var payload TeacherWatchAssignmentPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "INVALID_PAYLOAD",
			Message: "Cannot parse teacher.watch_assignment payload: " + err.Error(),
		})
		return
	}

	// Permission check
	if client.Role != "teacher" && client.Role != "superuser" {
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "FORBIDDEN",
			Message: "Only teachers or superusers can watch assignments",
		})
		return
	}

	ok, err := h.authorizer.CanWatchAssignment(context.Background(), client.UserID, payload.AssignmentID)
	if err != nil || !ok {
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "FORBIDDEN",
			Message: "Access to assignment matrix denied",
		})
		return
	}

	h.mu.Lock()
	watchers, exists := h.assignmentWatchers[payload.AssignmentID]
	if !exists {
		watchers = make(map[*Client]struct{})
		h.assignmentWatchers[payload.AssignmentID] = watchers
	}
	watchers[client] = struct{}{}
	client.AddWatchedAssignment(payload.AssignmentID)

	// Send current presence states for all students in this assignment to newly watching teacher
	var patches []*MatrixPresencePatchPayload
	for _, p := range h.sessionPresence {
		if p.AssignmentID == payload.AssignmentID {
			patches = append(patches, p.ToPatch())
		}
	}
	h.mu.Unlock()

	for _, patch := range patches {
		_ = client.SendFormatted(TypeMatrixPresencePatch, patch)
	}
}

// handleTeacherUnwatchAssignment unsubscribes teacher from assignment matrix updates.
func (h *Hub) handleTeacherUnwatchAssignment(client *Client, data json.RawMessage) {
	var payload TeacherUnwatchAssignmentPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if watchers, exists := h.assignmentWatchers[payload.AssignmentID]; exists {
		delete(watchers, client)
		if len(watchers) == 0 {
			delete(h.assignmentWatchers, payload.AssignmentID)
		}
	}
	client.RemoveWatchedAssignment(payload.AssignmentID)
}

// handleTeacherWatchStudentLive manages Level 2 live code stream subscription.
func (h *Hub) handleTeacherWatchStudentLive(client *Client, data json.RawMessage) {
	var payload TeacherWatchStudentLivePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "INVALID_PAYLOAD",
			Message: "Cannot parse teacher.watch_student_live payload: " + err.Error(),
		})
		return
	}

	// Permission check
	if client.Role != "teacher" && client.Role != "superuser" {
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "FORBIDDEN",
			Message: "Only teachers or superusers can watch live code streams",
		})
		return
	}

	ok, err := h.authorizer.CanWatchStudentLive(context.Background(), client.UserID, payload.SessionID)
	if err != nil || !ok {
		_ = client.SendFormatted(TypeServerError, ServerErrorPayload{
			Code:    "FORBIDDEN",
			Message: "Access to student live stream denied",
		})
		return
	}

	h.mu.Lock()
	watchers, exists := h.liveStudentWatchers[payload.SessionID]
	isFirstWatcher := false
	if !exists {
		watchers = make(map[*Client]struct{})
		h.liveStudentWatchers[payload.SessionID] = watchers
		isFirstWatcher = true
	}
	watchers[client] = struct{}{}
	client.AddWatchedStudent(payload.SessionID)

	// If this is the first teacher watching, toggle student stream on
	if isFirstWatcher {
		if studentConn, connected := h.studentConnections[payload.SessionID]; connected {
			_ = studentConn.SendFormatted(TypeServerLiveStreamToggle, ServerLiveStreamTogglePayload{
				SessionID:  payload.SessionID,
				Enabled:    true,
				DebounceMS: 350,
			})
		}
	}

	// If cached frame exists, immediately push cached frame to teacher
	cachedFrame := h.getLatestCachedFrameLocked(payload.SessionID)
	h.mu.Unlock()

	if cachedFrame != nil {
		_ = client.SendFormatted(TypeTeacherStudentLiveFrame, cachedFrame)
	}
}

// handleTeacherUnwatchStudentLive unsubscribes teacher from student live code stream.
func (h *Hub) handleTeacherUnwatchStudentLive(client *Client, data json.RawMessage) {
	var payload TeacherUnwatchStudentLivePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if watchers, exists := h.liveStudentWatchers[payload.SessionID]; exists {
		delete(watchers, client)
		if len(watchers) == 0 {
			delete(h.liveStudentWatchers, payload.SessionID)
			// When last teacher unwatches -> send server.live_stream_toggle{enabled: false} to student
			if studentConn, connected := h.studentConnections[payload.SessionID]; connected {
				_ = studentConn.SendFormatted(TypeServerLiveStreamToggle, ServerLiveStreamTogglePayload{
					SessionID: payload.SessionID,
					Enabled:   false,
				})
			}
		}
	}
	client.RemoveWatchedStudent(payload.SessionID)
}

// handleStudentLiveCodeFrame receives code frame from student and relays to watching teachers.
func (h *Hub) handleStudentLiveCodeFrame(client *Client, data json.RawMessage) {
	var payload StudentLiveCodeFramePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}

	frame := TeacherStudentLiveFramePayload{
		SessionID:       payload.SessionID,
		TaskID:          payload.TaskID,
		IDEOpen:         payload.IDEOpen,
		SpreadsheetOpen: payload.SpreadsheetOpen,
		Code:            payload.Code,
		Cursor:          payload.Cursor,
		PasteRanges:     payload.PasteRanges,
		TerminalTail:    payload.TerminalTail,
		FrameTS:         time.Now().UTC(),
	}

	frameBytes, err := json.Marshal(frame)
	if err != nil {
		return
	}

	h.mu.Lock()
	// Cache frame in memory
	h.liveCodeFrames[payload.SessionID] = frameBytes

	// Relay to watching teachers
	var targetTeachers []*Client
	if watchers, exists := h.liveStudentWatchers[payload.SessionID]; exists {
		for teacher := range watchers {
			targetTeachers = append(targetTeachers, teacher)
		}
	}
	h.mu.Unlock()

	// Cache frame in Redis asynchronously (live:frame:{session_id})
	if h.redisClient != nil {
		go func(sid uuid.UUID, b []byte) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = h.redisClient.Set(ctx, fmt.Sprintf("live:frame:%s", sid), b, 2*time.Hour).Err()
		}(payload.SessionID, frameBytes)
	}

	// Relay frame to all watching teachers
	formattedMsg, err := FormatMessage(TypeTeacherStudentLiveFrame, frame)
	if err == nil {
		for _, teacher := range targetTeachers {
			_ = teacher.Send(formattedMsg)
		}
	}
}

// getLatestCachedFrameLocked checks in-memory cache and Redis for the latest frame.
func (h *Hub) getLatestCachedFrameLocked(sessionID uuid.UUID) *TeacherStudentLiveFramePayload {
	if b, exists := h.liveCodeFrames[sessionID]; exists {
		var frame TeacherStudentLiveFramePayload
		if err := json.Unmarshal(b, &frame); err == nil {
			return &frame
		}
	}

	if h.redisClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		val, err := h.redisClient.Get(ctx, fmt.Sprintf("live:frame:%s", sessionID)).Result()
		if err == nil && val != "" {
			var frame TeacherStudentLiveFramePayload
			if err := json.Unmarshal([]byte(val), &frame); err == nil {
				h.liveCodeFrames[sessionID] = []byte(val)
				return &frame
			}
		}
	}

	return nil
}

// SetSessionAssignment associates session_id with assignment_id for routing.
func (h *Hub) SetSessionAssignment(sessionID uuid.UUID, assignmentID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionAssignments[sessionID] = assignmentID
	if p, ok := h.sessionPresence[sessionID]; ok {
		p.AssignmentID = assignmentID
	}
}

// SetSessionResolver configures a fallback resolver for session_id -> assignment_id lookups.
func (h *Hub) SetSessionResolver(resolver SessionAssignmentResolver) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionResolver = resolver
}

// BroadcastPresencePatch broadcasts matrix.presence_patch to all teachers watching the assignment.
func (h *Hub) BroadcastPresencePatch(assignmentID uuid.UUID, patch *MatrixPresencePatchPayload) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	h.broadcastPresencePatchLocked(assignmentID, patch)
}

func (h *Hub) broadcastPresencePatchLocked(assignmentID uuid.UUID, patch *MatrixPresencePatchPayload) {
	watchers, exists := h.assignmentWatchers[assignmentID]
	if !exists || len(watchers) == 0 {
		return
	}
	msg, err := FormatMessage(TypeMatrixPresencePatch, patch)
	if err != nil {
		return
	}
	for teacher := range watchers {
		_ = teacher.Send(msg)
	}
}

// BroadcastCellPatch broadcasts matrix.cell_patch to all teachers watching the assignment.
func (h *Hub) BroadcastCellPatch(assignmentID uuid.UUID, patch *MatrixCellPatchPayload) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	watchers, exists := h.assignmentWatchers[assignmentID]
	if !exists || len(watchers) == 0 {
		return
	}
	msg, err := FormatMessage(TypeMatrixCellPatch, patch)
	if err != nil {
		return
	}
	for teacher := range watchers {
		_ = teacher.Send(msg)
	}
}

// BroadcastTimelineEvent broadcasts matrix.timeline_event to all teachers watching the assignment.
func (h *Hub) BroadcastTimelineEvent(assignmentID uuid.UUID, payload *MatrixTimelineEventPayload) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	watchers, exists := h.assignmentWatchers[assignmentID]
	if !exists || len(watchers) == 0 {
		return
	}
	msg, err := FormatMessage(TypeMatrixTimelineEvent, payload)
	if err != nil {
		return
	}
	for teacher := range watchers {
		_ = teacher.Send(msg)
	}
}

// UpdatePresenceFromEvent updates in-memory presence and broadcasts presence patch.
func (h *Hub) UpdatePresenceFromEvent(row EventRow) {
	h.mu.Lock()
	defer h.mu.Unlock()

	presence, exists := h.sessionPresence[row.SessionID]
	if !exists {
		presence = &StudentSessionPresence{
			AssignmentID:   row.AssignmentID,
			UserID:         row.UserID,
			SessionID:      row.SessionID,
			Online:         true,
			TabFocused:     true,
			LastActivityAt: row.OccurredAt,
		}
		h.sessionPresence[row.SessionID] = presence
	}

	presence.LastActivityAt = row.OccurredAt
	switch row.Event {
	case "tab_blur":
		presence.TabFocused = false
		presence.BlurCount++
	case "tab_focus":
		presence.TabFocused = true
	case "task_opened":
		if row.TaskID != nil {
			presence.CurrentTaskID = row.TaskID
		}
	case "insert_in_ide":
		presence.PasteCount++
	case "open_ide":
		var p struct {
			Open bool `json:"open"`
		}
		if err := json.Unmarshal(row.Payload, &p); err == nil {
			presence.IDEOpen = p.Open
		}
	}

	if row.AssignmentID != uuid.Nil {
		h.broadcastPresencePatchLocked(row.AssignmentID, presence.ToPatch())
	}
}

// Stats returns snapshot counts for testing and health monitoring.
func (h *Hub) Stats() (clientsCount, assignmentsWatched, studentsWatched, activeStudents int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients), len(h.assignmentWatchers), len(h.liveStudentWatchers), len(h.studentConnections)
}
