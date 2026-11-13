package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// helper to convert http(s) URL to ws(s) URL
func toWebSocketURL(httpURL, path string) string {
	wsURL := strings.TrimRight(httpURL, "/") + path
	if strings.HasPrefix(wsURL, "https://") {
		return "wss://" + strings.TrimPrefix(wsURL, "https://")
	}
	if strings.HasPrefix(wsURL, "http://") {
		return "ws://" + strings.TrimPrefix(wsURL, "http://")
	}
	return wsURL
}

// =============================================================================
// 1. WebSocket Handshake & Authentication (/ws)
// =============================================================================

func TestWS_Handshake_UnauthenticatedRejected(t *testing.T) {
	h := GetTestHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	wsURL := toWebSocketURL(h.BaseURL, "/ws")

	// Attempt connection without ege_access cookie
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "done")
		t.Fatalf("expected handshake rejection with 401 Unauthorized, but dial succeeded")
	}

	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized, got %d", resp.StatusCode)
	}
}

func TestWS_Handshake_ExpiredTokenRejected(t *testing.T) {
	h := GetTestHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	expiredToken := GenerateTestToken(StudentUUID, "student", true)
	cookie := MakeAccessCookie(expiredToken)

	wsURL := toWebSocketURL(h.BaseURL, "/ws")
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{cookie.String()},
		},
	}

	conn, resp, err := websocket.Dial(ctx, wsURL, opts)
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "done")
		t.Fatalf("expected handshake rejection for expired token, but dial succeeded")
	}

	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401 Unauthorized, got %d", resp.StatusCode)
	}
}

func TestWS_Handshake_AuthenticatedStudentSuccess(t *testing.T) {
	h := GetTestHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	studentToken := GenerateTestToken(StudentUUID, "student", false)
	cookie := MakeAccessCookie(studentToken)

	wsURL := toWebSocketURL(h.BaseURL, "/ws")
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{cookie.String()},
		},
	}

	conn, resp, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("failed to dial websocket with student token: %v (resp: %+v)", err, resp)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test finished")

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("expected status 101 Switching Protocols, got %d", resp.StatusCode)
	}
}

func TestWS_Handshake_AuthenticatedTeacherSuccess(t *testing.T) {
	h := GetTestHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	teacherToken := GenerateTestToken(TeacherUUID, "teacher", false)
	cookie := MakeAccessCookie(teacherToken)

	wsURL := toWebSocketURL(h.BaseURL, "/ws")
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{cookie.String()},
		},
	}

	conn, resp, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		t.Fatalf("failed to dial websocket with teacher token: %v (resp: %+v)", err, resp)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test finished")

	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("expected status 101 Switching Protocols, got %d", resp.StatusCode)
	}
}

// =============================================================================
// 2. Level 1: Telemetry & Live Classroom Matrix
// =============================================================================

func TestWS_Level1_StudentTelemetryAndTeacherMatrix(t *testing.T) {
	h := GetTestHarness(t)
	wsURL := toWebSocketURL(h.BaseURL, "/ws")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Connect Teacher Socket
	teacherToken := GenerateTestToken(TeacherUUID, "teacher", false)
	teacherOpts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{MakeAccessCookie(teacherToken).String()}},
	}
	teacherConn, _, err := websocket.Dial(ctx, wsURL, teacherOpts)
	if err != nil {
		t.Fatalf("teacher ws dial failed: %v", err)
	}
	defer teacherConn.Close(websocket.StatusNormalClosure, "done")

	// Teacher sends "teacher.watch_assignment"
	watchMsg := map[string]any{
		"type": "teacher.watch_assignment",
		"ts":   time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"assignment_id": TestAssignUUID,
		},
	}
	b, _ := json.Marshal(watchMsg)
	if err := teacherConn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("teacher write failed: %v", err)
	}

	// 2. Connect Student Socket
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentOpts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{MakeAccessCookie(studentToken).String()}},
	}
	studentConn, _, err := websocket.Dial(ctx, wsURL, studentOpts)
	if err != nil {
		t.Fatalf("student ws dial failed: %v", err)
	}
	defer studentConn.Close(websocket.StatusNormalClosure, "done")

	// Student sends "student.session_join"
	joinMsg := map[string]any{
		"type": "student.session_join",
		"ts":   time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"session_id":      TestSessionUUID,
			"assignment_id":   TestAssignUUID,
			"current_task_id": 1042,
		},
	}
	b, _ = json.Marshal(joinMsg)
	if err := studentConn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("student join write failed: %v", err)
	}

	// Student sends "student.telemetry_batch"
	telemetryMsg := map[string]any{
		"type": "student.telemetry_batch",
		"ts":   time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"session_id": TestSessionUUID,
			"events": []map[string]any{
				{
					"event":     "task_opened",
					"task_id":   1042,
					"client_ts": time.Now().UTC().Format(time.RFC3339Nano),
					"payload":   map[string]any{},
				},
				{
					"event":     "insert_in_ide",
					"task_id":   1042,
					"client_ts": time.Now().UTC().Format(time.RFC3339Nano),
					"payload": map[string]any{
						"chars_count": 32,
						"preview":     "print('test insert')",
					},
				},
			},
		},
	}
	b, _ = json.Marshal(telemetryMsg)
	if err := studentConn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("student telemetry write failed: %v", err)
	}

	// 3. Teacher socket should receive "matrix.presence_patch"
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	_, receivedBytes, err := teacherConn.Read(readCtx)
	if err != nil {
		t.Fatalf("teacher failed to receive presence patch: %v", err)
	}

	var env struct {
		Type string `json:"type"`
		Data struct {
			SessionID string `json:"session_id"`
			UserID    string `json:"user_id"`
			Online    bool   `json:"online"`
		} `json:"data"`
	}
	if err := json.Unmarshal(receivedBytes, &env); err != nil {
		t.Fatalf("failed to unmarshal teacher received message: %v", err)
	}

	if env.Type != "matrix.presence_patch" {
		t.Errorf("expected message type matrix.presence_patch, got %s", env.Type)
	}
	if env.Data.SessionID != TestSessionUUID {
		t.Errorf("expected session_id %s, got %s", TestSessionUUID, env.Data.SessionID)
	}
}

// =============================================================================
// 3. Level 2: On-Demand Student Code Streaming
// =============================================================================

func TestWS_Level2_LiveCodeStreaming(t *testing.T) {
	h := GetTestHarness(t)
	wsURL := toWebSocketURL(h.BaseURL, "/ws")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	targetSessionID := LiveSessionUUID

	// 1. Student connects and joins targetSessionID
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentOpts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{MakeAccessCookie(studentToken).String()}},
	}
	studentConn, _, err := websocket.Dial(ctx, wsURL, studentOpts)
	if err != nil {
		t.Fatalf("student ws dial failed: %v", err)
	}
	defer studentConn.Close(websocket.StatusNormalClosure, "done")

	joinMsg := map[string]any{
		"type": "student.session_join",
		"ts":   time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"session_id":      targetSessionID,
			"assignment_id":   TestAssignUUID,
			"current_task_id": 1042,
		},
	}
	b, _ := json.Marshal(joinMsg)
	if err := studentConn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("student join write failed: %v", err)
	}

	// 2. Teacher connects
	teacherToken := GenerateTestToken(TeacherUUID, "teacher", false)
	teacherOpts := &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{MakeAccessCookie(teacherToken).String()}},
	}
	teacherConn, _, err := websocket.Dial(ctx, wsURL, teacherOpts)
	if err != nil {
		t.Fatalf("teacher ws dial failed: %v", err)
	}
	defer teacherConn.Close(websocket.StatusNormalClosure, "done")

	// Teacher sends "teacher.watch_student_live"
	watchLiveMsg := map[string]any{
		"type": "teacher.watch_student_live",
		"ts":   time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"session_id": targetSessionID,
		},
	}
	b, _ = json.Marshal(watchLiveMsg)
	if err := teacherConn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("teacher watch live write failed: %v", err)
	}

	// 3. Student should receive "server.live_stream_toggle" with enabled: true
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	_, receivedBytes, err := studentConn.Read(readCtx)
	if err != nil {
		t.Fatalf("student failed to receive stream toggle: %v", err)
	}

	var toggleEnv struct {
		Type string `json:"type"`
		Data struct {
			SessionID  string `json:"session_id"`
			Enabled    bool   `json:"enabled"`
			DebounceMS int    `json:"debounce_ms"`
		} `json:"data"`
	}
	if err := json.Unmarshal(receivedBytes, &toggleEnv); err != nil {
		t.Fatalf("unmarshal stream toggle error: %v", err)
	}

	if toggleEnv.Type != "server.live_stream_toggle" {
		t.Errorf("expected type server.live_stream_toggle, got %s", toggleEnv.Type)
	}
	if !toggleEnv.Data.Enabled {
		t.Errorf("expected enabled=true")
	}

	// 4. Student streams a live code frame: "student.live_code_frame"
	codeFrameMsg := map[string]any{
		"type": "student.live_code_frame",
		"ts":   time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"session_id": targetSessionID,
			"task_id":    1042,
			"code":       "s = open('24.txt').read()\nprint(len(s))",
			"cursor": map[string]int{
				"line":   2,
				"column": 14,
			},
		},
	}
	b, _ = json.Marshal(codeFrameMsg)
	if err := studentConn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("student code frame write failed: %v", err)
	}

	// 5. Teacher should receive "teacher.student_live_frame"
	teacherReadCtx, teacherReadCancel := context.WithTimeout(ctx, 2*time.Second)
	defer teacherReadCancel()

	_, teacherRecvBytes, err := teacherConn.Read(teacherReadCtx)
	if err != nil {
		t.Fatalf("teacher failed to receive student live frame: %v", err)
	}

	var frameEnv struct {
		Type string `json:"type"`
		Data struct {
			SessionID string `json:"session_id"`
			TaskID    int    `json:"task_id"`
			Code      string `json:"code"`
			Cursor    struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(teacherRecvBytes, &frameEnv); err != nil {
		t.Fatalf("unmarshal live frame error: %v", err)
	}

	if frameEnv.Type != "teacher.student_live_frame" {
		t.Errorf("expected type teacher.student_live_frame, got %s", frameEnv.Type)
	}
	if frameEnv.Data.SessionID != targetSessionID {
		t.Errorf("expected session_id %s, got %s", targetSessionID, frameEnv.Data.SessionID)
	}
	if !strings.Contains(frameEnv.Data.Code, "open('24.txt')") {
		t.Errorf("expected streamed code to match, got: %s", frameEnv.Data.Code)
	}

	// 6. Teacher unwatches live code
	unwatchMsg := map[string]any{
		"type": "teacher.unwatch_student_live",
		"ts":   time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"session_id": targetSessionID,
		},
	}
	b, _ = json.Marshal(unwatchMsg)
	if err := teacherConn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("teacher unwatch write failed: %v", err)
	}

	// 7. Student receives "server.live_stream_toggle" with enabled: false
	closeCtx, closeCancel := context.WithTimeout(ctx, 2*time.Second)
	defer closeCancel()

	_, toggleCloseBytes, err := studentConn.Read(closeCtx)
	if err != nil {
		t.Fatalf("student failed to receive stream toggle disable: %v", err)
	}

	_ = json.Unmarshal(toggleCloseBytes, &toggleEnv)
	if toggleEnv.Type != "server.live_stream_toggle" || toggleEnv.Data.Enabled {
		t.Errorf("expected server.live_stream_toggle with enabled=false, got type=%s enabled=%v", toggleEnv.Type, toggleEnv.Data.Enabled)
	}
}
