package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	// PingPeriod is the interval between outgoing heartbeat pings.
	PingPeriod = 25 * time.Second

	// ReadDeadline is the maximum time allowed to read the next frame from peer.
	ReadDeadline = 60 * time.Second

	// WriteDeadline is the maximum time allowed for writing a frame.
	WriteDeadline = 10 * time.Second

	// MaxMessageSize is the maximum allowed frame size (1 MB).
	MaxMessageSize = 1024 * 1024

	// DefaultClientQueueCapacity is the ring buffer capacity per client.
	DefaultClientQueueCapacity = 512
)

// WSConn abstracts coder/websocket.Conn methods for both production and unit testing.
type WSConn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
	Ping(ctx context.Context) error
	Close(code websocket.StatusCode, reason string) error
	CloseNow() error
	SetReadLimit(n int64)
}

// HubInterface defines the interaction contract between Client and Hub.
type HubInterface interface {
	Unregister(client *Client)
	HandleMessage(client *Client, env Envelope)
}

// Client represents an authenticated WebSocket connection wrapper.
type Client struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Role      string
	conn      WSConn
	hub       HubInterface
	sendQueue *RingBuffer

	mu                 sync.RWMutex
	sessionID          *uuid.UUID
	watchedAssignments map[uuid.UUID]struct{}
	watchedStudents    map[uuid.UUID]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Bool
}

// NewClient initializes a new Client wrapper.
func NewClient(userID uuid.UUID, role string, conn WSConn, hub HubInterface) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	conn.SetReadLimit(MaxMessageSize)

	return &Client{
		ID:                 uuid.New(),
		UserID:             userID,
		Role:               role,
		conn:               conn,
		hub:                hub,
		sendQueue:          NewRingBuffer(DefaultClientQueueCapacity),
		watchedAssignments: make(map[uuid.UUID]struct{}),
		watchedStudents:    make(map[uuid.UUID]struct{}),
		ctx:                ctx,
		cancel:             cancel,
	}
}

// Send queues a byte payload to the client's non-blocking ring buffer.
// Returns false if client is closed or payload could not be queued.
func (c *Client) Send(msg []byte) bool {
	if c.closed.Load() {
		return false
	}
	return c.sendQueue.Push(msg)
}

// SendFormatted marshals a standard envelope and queues it to the client.
func (c *Client) SendFormatted(msgType string, payload any) error {
	data, err := FormatMessage(msgType, payload)
	if err != nil {
		return err
	}
	if !c.Send(data) {
		return errors.New("failed to send message: client closed or queue dropped")
	}
	return nil
}

// Close gracefully closes the client connection and signals internal queues.
func (c *Client) Close() {
	if c.closed.CompareAndSwap(false, true) {
		c.cancel()
		c.sendQueue.Close()
		if c.hub != nil {
			c.hub.Unregister(c)
		}
		_ = c.conn.Close(websocket.StatusNormalClosure, "connection closed")
	}
}

// ReadPump continuously pumps incoming WebSocket frames into the Hub dispatcher.
// Enforces a 60-second read deadline for each incoming frame.
func (c *Client) ReadPump(parentCtx context.Context) {
	defer c.Close()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-parentCtx.Done():
			return
		default:
		}

		readCtx, readCancel := context.WithTimeout(c.ctx, ReadDeadline)
		msgType, data, err := c.conn.Read(readCtx)
		readCancel()

		if err != nil {
			// Normal closure, peer closed, or read deadline expired
			return
		}

		if msgType != websocket.MessageText {
			continue
		}

		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			_ = c.SendFormatted(TypeServerError, ServerErrorPayload{
				Code:    "INVALID_FRAME",
				Message: "Malformed JSON frame: " + err.Error(),
			})
			continue
		}

		if c.hub != nil {
			c.hub.HandleMessage(c, env)
		}
	}
}

// WritePump continuously pumps outgoing messages from the ring buffer to the peer,
// and transmits periodic Ping heartbeats every 25 seconds.
func (c *Client) WritePump(parentCtx context.Context) {
	ticker := time.NewTicker(PingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-parentCtx.Done():
			return
		case <-ticker.C:
			pingCtx, pingCancel := context.WithTimeout(c.ctx, 5*time.Second)
			err := c.conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
		case <-c.sendQueue.Notify():
			for {
				msg, ok := c.sendQueue.TryPop()
				if !ok {
					break
				}
				writeCtx, writeCancel := context.WithTimeout(c.ctx, WriteDeadline)
				err := c.conn.Write(writeCtx, websocket.MessageText, msg)
				writeCancel()
				if err != nil {
					return
				}
			}
			if c.sendQueue.IsClosed() {
				return
			}
		}
	}
}

// SessionID returns the currently bound student session ID, or nil if none.
func (c *Client) SessionID() *uuid.UUID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionID
}

// SetSessionID sets the bound student session ID.
func (c *Client) SetSessionID(sessionID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessionID = &sessionID
}

// AddWatchedAssignment tracks that this client watches an assignment.
func (c *Client) AddWatchedAssignment(assignmentID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.watchedAssignments[assignmentID] = struct{}{}
}

// RemoveWatchedAssignment removes assignment from client's watch list.
func (c *Client) RemoveWatchedAssignment(assignmentID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.watchedAssignments, assignmentID)
}

// WatchedAssignments returns a snapshot of watched assignment IDs.
func (c *Client) WatchedAssignments() []uuid.UUID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := make([]uuid.UUID, 0, len(c.watchedAssignments))
	for id := range c.watchedAssignments {
		res = append(res, id)
	}
	return res
}

// AddWatchedStudent tracks that this client watches a student's live stream.
func (c *Client) AddWatchedStudent(sessionID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.watchedStudents[sessionID] = struct{}{}
}

// RemoveWatchedStudent removes student session from client's watch list.
func (c *Client) RemoveWatchedStudent(sessionID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.watchedStudents, sessionID)
}

// WatchedStudents returns a snapshot of watched student session IDs.
func (c *Client) WatchedStudents() []uuid.UUID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := make([]uuid.UUID, 0, len(c.watchedStudents))
	for id := range c.watchedStudents {
		res = append(res, id)
	}
	return res
}
