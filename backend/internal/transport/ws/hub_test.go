package ws

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHub_Level1_AssignmentMatrixWatch(t *testing.T) {
	hub := NewHub(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teacherConn := newMockWSConn()
	teacherClient := NewClient(uuid.New(), "teacher", teacherConn, hub)
	hub.Register(teacherClient)
	go teacherClient.WritePump(ctx)

	studentConn := newMockWSConn()
	studentClient := NewClient(uuid.New(), "student", studentConn, hub)
	hub.Register(studentClient)
	go studentClient.WritePump(ctx)

	assignmentID := uuid.New()
	sessionID := uuid.New()

	// 1. Teacher subscribes to assignment
	watchPayload, _ := json.Marshal(TeacherWatchAssignmentPayload{AssignmentID: assignmentID})
	hub.HandleMessage(teacherClient, Envelope{
		Type: TypeTeacherWatchAssignment,
		Data: watchPayload,
	})

	_, assignmentsWatched, _, _ := hub.Stats()
	if assignmentsWatched != 1 {
		t.Fatalf("expected 1 assignment watched, got %d", assignmentsWatched)
	}

	// Associate session with assignment
	hub.SetSessionAssignment(sessionID, assignmentID)

	// 2. Student joins session
	joinPayload, _ := json.Marshal(StudentSessionJoinPayload{
		SessionID:     sessionID,
		CurrentTaskID: 1042,
	})
	hub.HandleMessage(studentClient, Envelope{
		Type: TypeStudentSessionJoin,
		Data: joinPayload,
	})

	// Teacher should have received matrix.presence_patch
	time.Sleep(50 * time.Millisecond)
	teacherConn.mu.Lock()
	foundPresence := false
	for _, raw := range teacherConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeMatrixPresencePatch {
			var p MatrixPresencePatchPayload
			if err := json.Unmarshal(env.Data, &p); err == nil && p.Online && p.CurrentTaskID != nil && *p.CurrentTaskID == 1042 {
				foundPresence = true
				break
			}
		}
	}
	teacherConn.mu.Unlock()

	if !foundPresence {
		t.Error("expected teacher to receive matrix.presence_patch with online=true and task=1042")
	}

	// 3. Teacher unsubscribes
	unwatchPayload, _ := json.Marshal(TeacherUnwatchAssignmentPayload{AssignmentID: assignmentID})
	hub.HandleMessage(teacherClient, Envelope{
		Type: TypeTeacherUnwatchAssignment,
		Data: unwatchPayload,
	})

	_, assignmentsWatchedAfter, _, _ := hub.Stats()
	if assignmentsWatchedAfter != 0 {
		t.Fatalf("expected 0 assignments watched after unwatch, got %d", assignmentsWatchedAfter)
	}
}

func TestHub_Level2_OnDemandLiveStreamingLifecycle(t *testing.T) {
	hub := NewHub(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teacherConn1 := newMockWSConn()
	teacher1 := NewClient(uuid.New(), "teacher", teacherConn1, hub)
	hub.Register(teacher1)
	go teacher1.WritePump(ctx)

	teacherConn2 := newMockWSConn()
	teacher2 := NewClient(uuid.New(), "teacher", teacherConn2, hub)
	hub.Register(teacher2)
	go teacher2.WritePump(ctx)

	studentConn := newMockWSConn()
	student := NewClient(uuid.New(), "student", studentConn, hub)
	hub.Register(student)
	go student.WritePump(ctx)

	sessionID := uuid.New()

	// Student joins session
	joinPayload, _ := json.Marshal(StudentSessionJoinPayload{
		SessionID:     sessionID,
		CurrentTaskID: 24,
	})
	hub.HandleMessage(student, Envelope{
		Type: TypeStudentSessionJoin,
		Data: joinPayload,
	})

	time.Sleep(30 * time.Millisecond)

	// Initial check: no streaming toggle sent yet
	studentConn.mu.Lock()
	if len(studentConn.writtenMsgs) != 0 {
		t.Fatalf("expected 0 messages sent to student initially, got %d", len(studentConn.writtenMsgs))
	}
	studentConn.mu.Unlock()

	// 1. Teacher 1 watches student live
	watchPayload, _ := json.Marshal(TeacherWatchStudentLivePayload{SessionID: sessionID})
	hub.HandleMessage(teacher1, Envelope{
		Type: TypeTeacherWatchStudentLive,
		Data: watchPayload,
	})

	time.Sleep(50 * time.Millisecond)

	// Student should receive server.live_stream_toggle{enabled: true, debounce_ms: 350}
	studentConn.mu.Lock()
	foundEnable := false
	for _, raw := range studentConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeServerLiveStreamToggle {
			var toggle ServerLiveStreamTogglePayload
			if err := json.Unmarshal(env.Data, &toggle); err == nil && toggle.Enabled && toggle.DebounceMS == 350 {
				foundEnable = true
				break
			}
		}
	}
	studentConn.mu.Unlock()

	if !foundEnable {
		t.Fatal("expected student to receive server.live_stream_toggle with enabled=true")
	}

	// 2. Student sends live code frame
	codeFramePayload, _ := json.Marshal(StudentLiveCodeFramePayload{
		SessionID: sessionID,
		TaskID:    24,
		Code:      "with open('24.txt') as f: print(f.read())",
	})
	hub.HandleMessage(student, Envelope{
		Type: TypeStudentLiveCodeFrame,
		Data: codeFramePayload,
	})

	time.Sleep(50 * time.Millisecond)

	// Teacher 1 should receive teacher.student_live_frame
	teacherConn1.mu.Lock()
	foundFrame1 := false
	for _, raw := range teacherConn1.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeTeacherStudentLiveFrame {
			var frame TeacherStudentLiveFramePayload
			if err := json.Unmarshal(env.Data, &frame); err == nil && frame.Code == "with open('24.txt') as f: print(f.read())" {
				foundFrame1 = true
				break
			}
		}
	}
	teacherConn1.mu.Unlock()

	if !foundFrame1 {
		t.Fatal("expected teacher 1 to receive student code frame")
	}

	// 3. Teacher 2 opens watch on the SAME student
	hub.HandleMessage(teacher2, Envelope{
		Type: TypeTeacherWatchStudentLive,
		Data: watchPayload,
	})

	time.Sleep(50 * time.Millisecond)

	// Teacher 2 must IMMEDIATELY receive the cached frame!
	teacherConn2.mu.Lock()
	foundCachedFrame := false
	for _, raw := range teacherConn2.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeTeacherStudentLiveFrame {
			var frame TeacherStudentLiveFramePayload
			if err := json.Unmarshal(env.Data, &frame); err == nil && frame.Code == "with open('24.txt') as f: print(f.read())" {
				foundCachedFrame = true
				break
			}
		}
	}
	teacherConn2.mu.Unlock()

	if !foundCachedFrame {
		t.Fatal("expected teacher 2 to immediately receive cached frame upon watching")
	}

	// Student should NOT have received a second toggle
	studentConn.mu.Lock()
	toggleCount := 0
	for _, raw := range studentConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeServerLiveStreamToggle {
			toggleCount++
		}
	}
	studentConn.mu.Unlock()
	if toggleCount != 1 {
		t.Fatalf("expected exactly 1 toggle to student so far, got %d", toggleCount)
	}

	// 4. Teacher 1 unwatches -> Stream continues because Teacher 2 is still watching
	unwatchPayload, _ := json.Marshal(TeacherUnwatchStudentLivePayload{SessionID: sessionID})
	hub.HandleMessage(teacher1, Envelope{
		Type: TypeTeacherUnwatchStudentLive,
		Data: unwatchPayload,
	})

	time.Sleep(50 * time.Millisecond)

	studentConn.mu.Lock()
	toggleCountAfter1 := 0
	for _, raw := range studentConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeServerLiveStreamToggle {
			toggleCountAfter1++
		}
	}
	studentConn.mu.Unlock()
	if toggleCountAfter1 != 1 {
		t.Fatalf("stream should still be active for teacher 2; toggle count changed to %d", toggleCountAfter1)
	}

	// 5. Teacher 2 unwatches (last watcher) -> Student receives server.live_stream_toggle{enabled: false}
	hub.HandleMessage(teacher2, Envelope{
		Type: TypeTeacherUnwatchStudentLive,
		Data: unwatchPayload,
	})

	time.Sleep(50 * time.Millisecond)

	studentConn.mu.Lock()
	foundDisable := false
	for _, raw := range studentConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeServerLiveStreamToggle {
			var toggle ServerLiveStreamTogglePayload
			if err := json.Unmarshal(env.Data, &toggle); err == nil && !toggle.Enabled {
				foundDisable = true
				break
			}
		}
	}
	studentConn.mu.Unlock()

	if !foundDisable {
		t.Fatal("expected student to receive server.live_stream_toggle with enabled=false when last teacher left")
	}
}

func TestHub_StudentDisconnect_BroadcastsPresenceOffline(t *testing.T) {
	hub := NewHub(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	teacherConn := newMockWSConn()
	teacher := NewClient(uuid.New(), "teacher", teacherConn, hub)
	hub.Register(teacher)
	go teacher.WritePump(ctx)

	studentConn := newMockWSConn()
	student := NewClient(uuid.New(), "student", studentConn, hub)
	hub.Register(student)
	go student.WritePump(ctx)

	assignmentID := uuid.New()
	sessionID := uuid.New()

	hub.SetSessionAssignment(sessionID, assignmentID)

	// Teacher watches assignment
	watchPayload, _ := json.Marshal(TeacherWatchAssignmentPayload{AssignmentID: assignmentID})
	hub.HandleMessage(teacher, Envelope{
		Type: TypeTeacherWatchAssignment,
		Data: watchPayload,
	})

	// Student joins
	joinPayload, _ := json.Marshal(StudentSessionJoinPayload{
		SessionID:     sessionID,
		CurrentTaskID: 1,
	})
	hub.HandleMessage(student, Envelope{
		Type: TypeStudentSessionJoin,
		Data: joinPayload,
	})

	time.Sleep(50 * time.Millisecond)

	// Student disconnects
	hub.Unregister(student)

	time.Sleep(50 * time.Millisecond)
	teacherConn.mu.Lock()
	foundOffline := false
	for _, raw := range teacherConn.writtenMsgs {
		var env Envelope
		if err := json.Unmarshal(raw, &env); err == nil && env.Type == TypeMatrixPresencePatch {
			var p MatrixPresencePatchPayload
			if err := json.Unmarshal(env.Data, &p); err == nil && !p.Online {
				foundOffline = true
				break
			}
		}
	}
	teacherConn.mu.Unlock()

	if !foundOffline {
		t.Error("expected teacher to receive matrix.presence_patch with online=false on student disconnect")
	}
}
