package ws

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFormatMessage_EnvelopeStructure(t *testing.T) {
	sessionID := uuid.New()
	payload := StudentSessionJoinPayload{
		SessionID:     sessionID,
		CurrentTaskID: 1042,
	}

	data, err := FormatMessage(TypeStudentSessionJoin, payload)
	if err != nil {
		t.Fatalf("FormatMessage failed: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if env.Type != TypeStudentSessionJoin {
		t.Errorf("expected type %s, got %s", TypeStudentSessionJoin, env.Type)
	}
	if env.TS.IsZero() {
		t.Error("expected non-zero timestamp")
	}

	var parsed StudentSessionJoinPayload
	if err := json.Unmarshal(env.Data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal data payload: %v", err)
	}

	if parsed.SessionID != sessionID || parsed.CurrentTaskID != 1042 {
		t.Errorf("payload mismatch: %+v", parsed)
	}
}

func TestMessages_ClientToServerPayloads(t *testing.T) {
	sessionID := uuid.New()
	assignmentID := uuid.New()
	taskID := 1042

	// 1. student.session_join
	joinPayload := StudentSessionJoinPayload{SessionID: sessionID, CurrentTaskID: 1042}
	b, err := json.Marshal(joinPayload)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripJoin StudentSessionJoinPayload
	if err := json.Unmarshal(b, &roundtripJoin); err != nil || roundtripJoin.CurrentTaskID != 1042 {
		t.Fatalf("join payload error: %v", err)
	}

	// 2. student.telemetry_batch
	batchPayload := StudentTelemetryBatchPayload{
		SessionID: sessionID,
		Events: []TelemetryEventItem{
			{
				Event:    "task_opened",
				TaskID:   &taskID,
				ClientTS: time.Now().UTC(),
				Payload:  json.RawMessage("{}"),
			},
			{
				Event:    "tab_blur",
				TaskID:   &taskID,
				ClientTS: time.Now().UTC(),
				Payload:  json.RawMessage("{}"),
			},
		},
	}
	b, err = json.Marshal(batchPayload)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripBatch StudentTelemetryBatchPayload
	if err := json.Unmarshal(b, &roundtripBatch); err != nil || len(roundtripBatch.Events) != 2 {
		t.Fatalf("batch payload error: %v", err)
	}

	// 3. teacher.watch_assignment & unwatch
	watchAssign := TeacherWatchAssignmentPayload{AssignmentID: assignmentID}
	b, err = json.Marshal(watchAssign)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripWatchAssign TeacherWatchAssignmentPayload
	if err := json.Unmarshal(b, &roundtripWatchAssign); err != nil || roundtripWatchAssign.AssignmentID != assignmentID {
		t.Fatalf("watch assign error: %v", err)
	}

	unwatchAssign := TeacherUnwatchAssignmentPayload{AssignmentID: assignmentID}
	b, err = json.Marshal(unwatchAssign)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripUnwatchAssign TeacherUnwatchAssignmentPayload
	if err := json.Unmarshal(b, &roundtripUnwatchAssign); err != nil || roundtripUnwatchAssign.AssignmentID != assignmentID {
		t.Fatalf("unwatch assign error: %v", err)
	}

	// 4. teacher.watch_student_live & unwatch
	watchStudent := TeacherWatchStudentLivePayload{SessionID: sessionID}
	b, err = json.Marshal(watchStudent)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripWatchStudent TeacherWatchStudentLivePayload
	if err := json.Unmarshal(b, &roundtripWatchStudent); err != nil || roundtripWatchStudent.SessionID != sessionID {
		t.Fatalf("watch student error: %v", err)
	}

	// 5. student.live_code_frame
	codeFrame := StudentLiveCodeFramePayload{
		SessionID:       sessionID,
		TaskID:          1042,
		IDEOpen:         true,
		SpreadsheetOpen: false,
		Code:            "with open('24.txt') as f:\n    print(f.read())",
		Cursor:          &CursorPosition{Line: 2, Column: 10},
		PasteRanges:     [][2]int{{0, 25}},
		TerminalTail:    "test output\n",
	}
	b, err = json.Marshal(codeFrame)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripCodeFrame StudentLiveCodeFramePayload
	if err := json.Unmarshal(b, &roundtripCodeFrame); err != nil || roundtripCodeFrame.Cursor.Line != 2 {
		t.Fatalf("code frame error: %v", err)
	}
}

func TestMessages_ServerToClientPayloads(t *testing.T) {
	sessionID := uuid.New()
	assignmentID := uuid.New()
	userID := uuid.New()
	taskID := 1042

	// 1. matrix.presence_patch
	presencePatch := MatrixPresencePatchPayload{
		AssignmentID:   assignmentID,
		UserID:         userID,
		SessionID:      sessionID,
		Online:         true,
		TabFocused:     false,
		CurrentTaskID:  &taskID,
		IDEOpen:        true,
		BlurCount:      3,
		PasteCount:     1,
		LastActivityAt: time.Now().UTC(),
	}
	b, err := json.Marshal(presencePatch)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripPresence MatrixPresencePatchPayload
	if err := json.Unmarshal(b, &roundtripPresence); err != nil || roundtripPresence.BlurCount != 3 {
		t.Fatalf("presence patch error: %v", err)
	}

	// 2. matrix.cell_patch
	cellPatch := MatrixCellPatchPayload{
		AssignmentID:        assignmentID,
		UserID:              userID,
		SessionID:           sessionID,
		TaskID:              1042,
		IsCorrect:           true,
		EarnedScore:         2,
		Attempts:            1,
		TotalPrimaryScore:   14,
		TotalSecondaryScore: 68,
		TaskStat: &TaskStatSummary{
			SolvedCount:    12,
			AttemptedCount: 18,
			AvgAttempts:    1.6,
			AvgTimeSec:     310,
		},
	}
	b, err = json.Marshal(cellPatch)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripCell MatrixCellPatchPayload
	if err := json.Unmarshal(b, &roundtripCell); err != nil || roundtripCell.TotalSecondaryScore != 68 {
		t.Fatalf("cell patch error: %v", err)
	}

	// 3. matrix.timeline_event
	timelinePayload := MatrixTimelineEventPayload{
		AssignmentID: assignmentID,
		UserID:       userID,
		SessionID:    sessionID,
		Event: TimelineEventData{
			ID:         1,
			TaskID:     &taskID,
			Event:      "tab_blur",
			Payload:    json.RawMessage("{}"),
			OccurredAt: time.Now().UTC(),
		},
	}
	b, err = json.Marshal(timelinePayload)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripTimeline MatrixTimelineEventPayload
	if err := json.Unmarshal(b, &roundtripTimeline); err != nil || roundtripTimeline.Event.Event != "tab_blur" {
		t.Fatalf("timeline event error: %v", err)
	}

	// 4. server.live_stream_toggle
	streamToggle := ServerLiveStreamTogglePayload{
		SessionID:  sessionID,
		Enabled:    true,
		DebounceMS: 350,
	}
	b, err = json.Marshal(streamToggle)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripToggle ServerLiveStreamTogglePayload
	if err := json.Unmarshal(b, &roundtripToggle); err != nil || roundtripToggle.DebounceMS != 350 {
		t.Fatalf("stream toggle error: %v", err)
	}

	// 5. teacher.student_live_frame
	liveFrame := TeacherStudentLiveFramePayload{
		SessionID:    sessionID,
		TaskID:       1042,
		IDEOpen:      true,
		Code:         "print(42)",
		Cursor:       &CursorPosition{Line: 1, Column: 9},
		TerminalTail: "42\n",
		FrameTS:      time.Now().UTC(),
	}
	b, err = json.Marshal(liveFrame)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripLiveFrame TeacherStudentLiveFramePayload
	if err := json.Unmarshal(b, &roundtripLiveFrame); err != nil || roundtripLiveFrame.Code != "print(42)" {
		t.Fatalf("live frame error: %v", err)
	}

	// 6. server.error
	srvErr := ServerErrorPayload{Code: "FORBIDDEN", Message: "Access denied"}
	b, err = json.Marshal(srvErr)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripErr ServerErrorPayload
	if err := json.Unmarshal(b, &roundtripErr); err != nil || roundtripErr.Code != "FORBIDDEN" {
		t.Fatalf("server error error: %v", err)
	}
}
