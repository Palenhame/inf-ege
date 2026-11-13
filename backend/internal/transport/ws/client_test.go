package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type mockWSConn struct {
	mu           sync.Mutex
	readCh       chan []byte
	writtenMsgs  [][]byte
	pingCount    int
	closeCode    websocket.StatusCode
	closeReason  string
	readLimit    int64
	closed       bool
	writeErrFunc func() error
	readErrFunc  func() error
}

func newMockWSConn() *mockWSConn {
	return &mockWSConn{
		readCh: make(chan []byte, 100),
	}
}

func (m *mockWSConn) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	if m.readErrFunc != nil {
		if err := m.readErrFunc(); err != nil {
			return 0, nil, err
		}
	}
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case data, ok := <-m.readCh:
		if !ok {
			return 0, nil, errors.New("conn closed")
		}
		return websocket.MessageText, data, nil
	}
}

func (m *mockWSConn) Write(ctx context.Context, typ websocket.MessageType, p []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeErrFunc != nil {
		if err := m.writeErrFunc(); err != nil {
			return err
		}
	}
	cpy := make([]byte, len(p))
	copy(cpy, p)
	m.writtenMsgs = append(m.writtenMsgs, cpy)
	return nil
}

func (m *mockWSConn) Ping(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pingCount++
	return nil
}

func (m *mockWSConn) Close(code websocket.StatusCode, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.closeCode = code
	m.closeReason = reason
	return nil
}

func (m *mockWSConn) CloseNow() error {
	return m.Close(websocket.StatusGoingAway, "now")
}

func (m *mockWSConn) SetReadLimit(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readLimit = n
}

type mockHub struct {
	mu           sync.Mutex
	unregistered []*Client
	handledEnvs  []Envelope
}

func (mh *mockHub) Unregister(client *Client) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.unregistered = append(mh.unregistered, client)
}

func (mh *mockHub) HandleMessage(client *Client, env Envelope) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.handledEnvs = append(mh.handledEnvs, env)
}

func TestClient_ReadPump_Success(t *testing.T) {
	conn := newMockWSConn()
	hub := &mockHub{}
	userID := uuid.New()
	client := NewClient(userID, "student", conn, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go client.ReadPump(ctx)

	// Send valid frame
	msgBytes, _ := FormatMessage(TypeStudentSessionJoin, StudentSessionJoinPayload{
		SessionID:     uuid.New(),
		CurrentTaskID: 1042,
	})
	conn.readCh <- msgBytes

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	hub.mu.Lock()
	if len(hub.handledEnvs) != 1 {
		t.Fatalf("expected 1 handled envelope, got %d", len(hub.handledEnvs))
	}
	if hub.handledEnvs[0].Type != TypeStudentSessionJoin {
		t.Errorf("expected type %s, got %s", TypeStudentSessionJoin, hub.handledEnvs[0].Type)
	}
	hub.mu.Unlock()

	client.Close()
}

func TestClient_ReadPump_MalformedFrame(t *testing.T) {
	conn := newMockWSConn()
	hub := &mockHub{}
	userID := uuid.New()
	client := NewClient(userID, "student", conn, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go client.ReadPump(ctx)
	go client.WritePump(ctx)

	// Send malformed JSON
	conn.readCh <- []byte("{broken json")

	time.Sleep(50 * time.Millisecond)

	// Should not have reached Hub
	hub.mu.Lock()
	if len(hub.handledEnvs) != 0 {
		t.Errorf("expected 0 handled envelopes, got %d", len(hub.handledEnvs))
	}
	hub.mu.Unlock()

	// Should have written server.error to client
	conn.mu.Lock()
	foundErr := false
	for _, raw := range conn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeServerError {
			foundErr = true
			break
		}
	}
	conn.mu.Unlock()

	if !foundErr {
		t.Error("expected server.error to be written on malformed frame")
	}

	client.Close()
}

func TestClient_WritePump_And_NonBlockingRingBuffer(t *testing.T) {
	conn := newMockWSConn()
	hub := &mockHub{}
	userID := uuid.New()
	client := NewClient(userID, "teacher", conn, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go client.WritePump(ctx)

	_ = client.SendFormatted(TypeMatrixPresencePatch, MatrixPresencePatchPayload{
		Online: true,
	})

	time.Sleep(50 * time.Millisecond)

	conn.mu.Lock()
	if len(conn.writtenMsgs) != 1 {
		t.Fatalf("expected 1 written message, got %d", len(conn.writtenMsgs))
	}
	conn.mu.Unlock()

	// Test non-blocking behavior on a slow client whose queue fills up:
	slowConn := newMockWSConn()
	slowClient := NewClient(uuid.New(), "student", slowConn, hub)
	for i := 0; i < DefaultClientQueueCapacity+20; i++ {
		slowClient.Send([]byte("payload"))
	}

	if slowClient.sendQueue.Dropped() != 20 {
		t.Errorf("expected 20 dropped messages on ring buffer overflow, got %d", slowClient.sendQueue.Dropped())
	}

	client.Close()
	slowClient.Close()
	hub.mu.Lock()
	if len(hub.unregistered) != 2 {
		t.Errorf("expected 2 clients to unregister from hub on close, got %d", len(hub.unregistered))
	}
	hub.mu.Unlock()
}
