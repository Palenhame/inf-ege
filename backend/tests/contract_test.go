package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// =============================================================================
// 1. Auth Routes (/auth/refresh, /auth/logout, /auth/me)
// =============================================================================

func TestAuthMe_Unauthenticated(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/auth/me", nil, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Authenticated bool `json:"authenticated"`
		User          any  `json:"user"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if res.Authenticated {
		t.Errorf("expected authenticated=false, got true")
	}
	if res.User != nil {
		t.Errorf("expected user=null, got %v", res.User)
	}
}

func TestAuthMe_AuthenticatedStudent(t *testing.T) {
	h := GetTestHarness(t)
	token := GenerateTestToken(StudentUUID, "student", false)
	cookie := MakeAccessCookie(token)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/auth/me", nil, []*http.Cookie{cookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !res.Authenticated {
		t.Errorf("expected authenticated=true")
	}
	if res.User.ID != StudentUUID {
		t.Errorf("expected user ID %s, got %s", StudentUUID, res.User.ID)
	}
	if res.User.Role != "student" {
		t.Errorf("expected role student, got %s", res.User.Role)
	}
}

func TestAuthRefresh_Success(t *testing.T) {
	h := GetTestHarness(t)
	refreshCookie := MakeRefreshCookie("valid-session-jti-token")

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/auth/refresh", nil, []*http.Cookie{refreshCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !res.OK {
		t.Errorf("expected ok=true")
	}

	// Verify Set-Cookie headers contain rotated tokens
	cookies := resp.Cookies()
	var hasAccess, hasRefresh bool
	for _, c := range cookies {
		if c.Name == "ege_access" && c.Value != "" {
			hasAccess = true
		}
		if c.Name == "ege_refresh" && c.Value != "" {
			hasRefresh = true
		}
	}
	if !hasAccess || !hasRefresh {
		t.Errorf("expected ege_access and ege_refresh cookies in Set-Cookie")
	}
}

func TestAuthRefresh_MissingCookie(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/auth/refresh", nil, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d: %s", resp.StatusCode, string(body))
	}

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "UNAUTHORIZED" && errResp.Error.Code != "TOKEN_EXPIRED" {
		t.Errorf("expected UNAUTHORIZED/TOKEN_EXPIRED error code, got %s", errResp.Error.Code)
	}
}

func TestAuthLogout_Success(t *testing.T) {
	h := GetTestHarness(t)
	token := GenerateTestToken(StudentUUID, "student", false)
	cookie := MakeAccessCookie(token)

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/auth/logout", nil, []*http.Cookie{cookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	// Verify cookies cleared (MaxAge < 0 or empty)
	for _, c := range resp.Cookies() {
		if c.Name == "ege_access" && c.MaxAge > 0 {
			t.Errorf("expected ege_access MaxAge <= 0")
		}
	}
}

func TestAuthLogout_Unauthenticated(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/auth/logout", nil, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK (clears cookies idempotently), got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !res.OK {
		t.Errorf("expected ok=true")
	}

	// Verify cookies cleared (MaxAge < 0 or empty)
	for _, c := range resp.Cookies() {
		if (c.Name == "ege_access" || c.Name == "ege_refresh") && c.MaxAge > 0 {
			t.Errorf("expected %s MaxAge <= 0", c.Name)
		}
	}
}

// =============================================================================
// 2. Superuser Impersonation (X-Impersonate-User)
// =============================================================================

func TestImpersonation_SuperuserAllowed(t *testing.T) {
	h := GetTestHarness(t)
	superToken := GenerateTestToken(SuperuserUUID, "superuser", false)
	superCookie := MakeAccessCookie(superToken)

	headers := map[string]string{
		"X-Impersonate-User": TargetStudentUUID,
	}

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/auth/me", nil, []*http.Cookie{superCookie}, headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Authenticated bool `json:"authenticated"`
		User          struct {
			ID             string  `json:"id"`
			Role           string  `json:"role"`
			ImpersonatedBy *string `json:"impersonated_by"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !res.Authenticated {
		t.Errorf("expected authenticated=true")
	}
	if res.User.ID != TargetStudentUUID {
		t.Errorf("expected effective user ID %s, got %s", TargetStudentUUID, res.User.ID)
	}
	if res.User.ImpersonatedBy == nil || *res.User.ImpersonatedBy != SuperuserUUID {
		t.Errorf("expected impersonated_by=%s, got %v", SuperuserUUID, res.User.ImpersonatedBy)
	}
}

func TestImpersonation_NonSuperuserForbidden(t *testing.T) {
	h := GetTestHarness(t)

	// Test 1: Student tries impersonation
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	headers := map[string]string{
		"X-Impersonate-User": TargetStudentUUID,
	}

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/auth/me", nil, []*http.Cookie{studentCookie}, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for student impersonation, got %d: %s", resp.StatusCode, string(body))
	}

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "FORBIDDEN" {
		t.Errorf("expected error code FORBIDDEN, got %s", errResp.Error.Code)
	}

	// Test 2: Teacher tries impersonation
	teacherToken := GenerateTestToken(TeacherUUID, "teacher", false)
	teacherCookie := MakeAccessCookie(teacherToken)

	resp, body = DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/auth/me", nil, []*http.Cookie{teacherCookie}, headers)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for teacher impersonation, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestImpersonation_InvalidUUID(t *testing.T) {
	h := GetTestHarness(t)
	superToken := GenerateTestToken(SuperuserUUID, "superuser", false)
	superCookie := MakeAccessCookie(superToken)

	headers := map[string]string{
		"X-Impersonate-User": "not-a-valid-uuid",
	}

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/auth/me", nil, []*http.Cookie{superCookie}, headers)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid UUID, got %d: %s", resp.StatusCode, string(body))
	}

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("expected VALIDATION_ERROR code, got %s", errResp.Error.Code)
	}
}

// =============================================================================
// 3. Bank Routes (/bank/tasks, /bank/tasks/{id}, /bank/tasks/{id}/submit)
// =============================================================================

func TestBankTasks_ListAndPagination(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/bank/tasks", nil, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Items  []map[string]any `json:"items"`
		Total  int              `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if res.Items == nil {
		t.Fatalf("expected items array, got nil")
	}
	if res.Total < 1 {
		t.Errorf("expected total >= 1, got %d", res.Total)
	}
}

func TestBankTasks_FilterByKIM(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/bank/tasks?kim=24", nil, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Items []struct {
			ID         int   `json:"id"`
			KIMNumbers []int `json:"kim_numbers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	for _, it := range res.Items {
		found := false
		for _, k := range it.KIMNumbers {
			if k == 24 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("task %d does not contain KIM 24", it.ID)
		}
	}
}

func TestBankTasks_ActiveExamExclusion(t *testing.T) {
	h := GetTestHarness(t)
	// StudentUUID has active exam locking task #1042
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/bank/tasks", nil, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Items []struct {
			ID int `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(body, &res)

	// Ensure task #1042 is EXCLUDED from public bank list for this student
	for _, it := range res.Items {
		if it.ID == 1042 {
			t.Errorf("task 1042 should be excluded from bank tasks during active exam")
		}
	}
}

func TestBankTask_GetByID(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/bank/tasks/1043", nil, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var task struct {
		ID       int    `json:"id"`
		Source   string `json:"source"`
		Solution any    `json:"solution"`
	}
	if err := json.Unmarshal(body, &task); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if task.ID != 1043 {
		t.Errorf("expected task ID 1043, got %d", task.ID)
	}
	// Solution should be null for unauthenticated / unsolved solver
	if task.Solution != nil {
		t.Errorf("expected solution to be null, got %v", task.Solution)
	}
}

func TestBankTask_GetByID_ActiveExamLocked(t *testing.T) {
	h := GetTestHarness(t)
	// StudentUUID has active exam locking task #1042
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/bank/tasks/1042", nil, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for locked task, got %d: %s", resp.StatusCode, string(body))
	}

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "TASK_LOCKED_BY_ACTIVE_EXAM" {
		t.Errorf("expected TASK_LOCKED_BY_ACTIVE_EXAM, got %s", errResp.Error.Code)
	}
}

func TestBankTask_GetByID_NotFound(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/bank/tasks/999999", nil, nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestBankTask_Submit_Success(t *testing.T) {
	h := GetTestHarness(t)
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	payload := map[string]any{
		"user_answer": map[string]string{
			"answer_type": "single",
			"value":       "42",
		},
	}

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/bank/tasks/1043/submit", payload, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		IsCorrect       bool `json:"is_correct"`
		EarnedScore     int  `json:"earned_score"`
		MaxScore        int  `json:"max_score"`
		TotalAttempts   int  `json:"total_attempts"`
		CanViewSolution bool `json:"can_view_solution"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !res.IsCorrect {
		t.Errorf("expected is_correct=true")
	}
	if res.EarnedScore != 1 {
		t.Errorf("expected earned_score=1, got %d", res.EarnedScore)
	}
	if !res.CanViewSolution {
		t.Errorf("expected can_view_solution=true")
	}
}

func TestBankTask_Submit_ActiveExamLocked(t *testing.T) {
	h := GetTestHarness(t)
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	payload := map[string]any{
		"user_answer": map[string]string{
			"answer_type": "single",
			"value":       "1024",
		},
	}

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/bank/tasks/1042/submit", payload, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for locked task submit, got %d: %s", resp.StatusCode, string(body))
	}
}

// =============================================================================
// 4. Room Routes (Create, Join, Limit, Free Code)
// =============================================================================

func TestRooms_CreateRoom_Teacher(t *testing.T) {
	h := GetTestHarness(t)
	teacherToken := GenerateTestToken(TeacherUUID, "teacher", false)
	teacherCookie := MakeAccessCookie(teacherToken)

	payload := map[string]any{
		"name":        "11-Б Информатика",
		"max_members": 25,
	}

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/rooms", payload, []*http.Cookie{teacherCookie}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", resp.StatusCode, string(body))
	}

	var room struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		InviteCode string `json:"invite_code"`
		MaxMembers int    `json:"max_members"`
	}
	if err := json.Unmarshal(body, &room); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if room.ID == "" || room.InviteCode == "" {
		t.Errorf("expected non-empty ID and invite code")
	}
	if room.MaxMembers != 25 {
		t.Errorf("expected max_members=25, got %d", room.MaxMembers)
	}
}

func TestRooms_CreateRoom_StudentForbidden(t *testing.T) {
	h := GetTestHarness(t)
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	payload := map[string]any{
		"name":        "Моя группа",
		"max_members": 20,
	}

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/rooms", payload, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for student room creation, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestRooms_JoinRoomPreview(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/rooms/join/K7X9M2P4", nil, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var preview struct {
		RoomID       string `json:"room_id"`
		RoomName     string `json:"room_name"`
		MembersCount int    `json:"members_count"`
		MaxMembers   int    `json:"max_members"`
	}
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if preview.RoomID == "" || preview.RoomName == "" {
		t.Errorf("expected non-empty room details")
	}
}

func TestRooms_JoinRoom_MembershipLimit(t *testing.T) {
	h := GetTestHarness(t)
	// Try joining room RoomFullUUID which has max_members = 2 and currently 2 members
	studentToken := GenerateTestToken(TargetStudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/rooms/join/FULL1234", nil, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for full room, got %d: %s", resp.StatusCode, string(body))
	}

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "LIMIT_EXCEEDED" {
		t.Errorf("expected LIMIT_EXCEEDED error code, got %s", errResp.Error.Code)
	}
}

func TestRooms_FreeCodeStorage(t *testing.T) {
	h := GetTestHarness(t)
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	codePayload := map[string]string{
		"code": "print('hello from free room editor')",
	}

	resp, body := DoRequest(t, http.MethodPut, h.BaseURL+"/api/v1/rooms/"+RoomStandardUUID+"/free-code", codePayload, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		OK    bool   `json:"ok"`
		S3Key string `json:"s3_key"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !res.OK || res.S3Key == "" {
		t.Errorf("expected ok=true and non-empty s3_key, got ok=%v, s3_key=%s", res.OK, res.S3Key)
	}
}

// =============================================================================
// 5. Assignment Routes (5 Modes, Self-Start, Sessions, Score Scale)
// =============================================================================

func TestAssignments_Create5Modes(t *testing.T) {
	h := GetTestHarness(t)
	teacherToken := GenerateTestToken(TeacherUUID, "teacher", false)
	teacherCookie := MakeAccessCookie(teacherToken)

	modes := []string{"homework", "classwork", "practice", "proctored_test", "mock_exam"}

	for _, mode := range modes {
		t.Run(fmt.Sprintf("mode_%s", mode), func(t *testing.T) {
			payload := map[string]any{
				"title":    "Assignment " + mode,
				"mode":     mode,
				"task_ids": []int{1043, 1044},
				"settings": map[string]any{
					"max_attempts": 3,
				},
			}

			resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/rooms/"+RoomStandardUUID+"/assignments", payload, []*http.Cookie{teacherCookie}, nil)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("expected 201 Created for mode %s, got %d: %s", mode, resp.StatusCode, string(body))
			}

			var res struct {
				ID         string `json:"id"`
				Mode       string `json:"mode"`
				TasksCount int    `json:"tasks_count"`
			}
			if err := json.Unmarshal(body, &res); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if res.Mode != mode {
				t.Errorf("expected mode %s, got %s", mode, res.Mode)
			}
			if res.TasksCount != 2 {
				t.Errorf("expected tasks_count=2, got %d", res.TasksCount)
			}
		})
	}
}

func TestExams_SelfStart(t *testing.T) {
	h := GetTestHarness(t)
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/exams/self-start", map[string]any{}, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		SessionID    string `json:"session_id"`
		AssignmentID string `json:"assignment_id"`
		ExpiresAt    string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if res.SessionID == "" || res.AssignmentID == "" || res.ExpiresAt == "" {
		t.Errorf("expected session_id, assignment_id, and expires_at, got %+v", res)
	}
}

func TestSessions_Lifecycle_AndScoreTranslation(t *testing.T) {
	h := GetTestHarness(t)
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	// 1. Self-start an exam session
	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/exams/self-start", map[string]any{}, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("failed to self-start exam: %s", string(body))
	}
	var initResp struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(body, &initResp)
	sessionID := initResp.SessionID

	// 2. Submit answer to task 1043 (1 primary point)
	// Mock exams use reveal_policy="after_submit", so AnswerTask stores the answer
	// without revealing is_correct/earned_score until FinishSession.
	ansPayload := map[string]any{
		"user_answer": map[string]string{
			"answer_type": "single",
			"value":       "42",
		},
	}
	resp, body = DoRequest(t, http.MethodPost, fmt.Sprintf("%s/api/v1/sessions/%s/tasks/1043/answer", h.BaseURL, sessionID), ansPayload, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to submit answer: %s", string(body))
	}

	var ansResp struct {
		IsCorrect    *bool  `json:"is_correct"`
		EarnedScore  *int   `json:"earned_score"`
		MaxScore     int    `json:"max_score"`
		AttemptsUsed int    `json:"attempts_used"`
		RevealPolicy string `json:"reveal_policy"`
	}
	if err := json.Unmarshal(body, &ansResp); err != nil {
		t.Fatalf("unmarshal answer response error: %v", err)
	}
	if ansResp.RevealPolicy != "after_submit" {
		t.Errorf("expected reveal_policy=after_submit, got %s", ansResp.RevealPolicy)
	}
	if ansResp.IsCorrect != nil || ansResp.EarnedScore != nil {
		t.Errorf("expected is_correct and earned_score to be masked (nil) during after_submit exam, got is_correct=%v earned_score=%v", ansResp.IsCorrect, ansResp.EarnedScore)
	}
	if ansResp.MaxScore != 1 || ansResp.AttemptsUsed != 1 {
		t.Errorf("expected max_score=1 and attempts_used=1, got max=%d attempts=%d", ansResp.MaxScore, ansResp.AttemptsUsed)
	}

	// 3. Finish session and verify KEGE score translation (primary 0..29 -> secondary 0..100)
	resp, body = DoRequest(t, http.MethodPost, fmt.Sprintf("%s/api/v1/sessions/%s/finish", h.BaseURL, sessionID), nil, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to finish session: %s", string(body))
	}

	var finishResp struct {
		Status         string `json:"status"`
		PrimaryScore   int    `json:"primary_score"`
		SecondaryScore int    `json:"secondary_score"`
		Protocol       struct {
			CanViewResults bool `json:"can_view_results"`
			Tasks          []struct {
				TaskID      int  `json:"task_id"`
				IsCorrect   bool `json:"is_correct"`
				EarnedScore int  `json:"earned_score"`
			} `json:"tasks"`
		} `json:"protocol"`
	}
	if err := json.Unmarshal(body, &finishResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if finishResp.Status != "finished" {
		t.Errorf("expected status=finished, got %s", finishResp.Status)
	}
	if finishResp.PrimaryScore != 1 {
		t.Errorf("expected primary_score=1, got %d", finishResp.PrimaryScore)
	}
	// Per scale table: 1 primary score maps to 7 secondary score
	if finishResp.SecondaryScore != 7 {
		t.Errorf("expected secondary_score=7 for primary_score=1, got %d", finishResp.SecondaryScore)
	}
	if !finishResp.Protocol.CanViewResults {
		t.Errorf("expected protocol.can_view_results=true")
	}
	found1043 := false
	for _, pt := range finishResp.Protocol.Tasks {
		if pt.TaskID == 1043 {
			found1043 = true
			if !pt.IsCorrect || pt.EarnedScore != 1 {
				t.Errorf("expected task 1043 protocol is_correct=true earned_score=1, got %+v", pt)
			}
		}
	}
	if !found1043 {
		t.Errorf("expected task 1043 in finish protocol tasks")
	}

	// 4. Second finish must return 400 SESSION_FINISHED
	resp, body = DoRequest(t, http.MethodPost, fmt.Sprintf("%s/api/v1/sessions/%s/finish", h.BaseURL, sessionID), nil, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on second finish, got %d: %s", resp.StatusCode, string(body))
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "SESSION_FINISHED" {
		t.Errorf("expected SESSION_FINISHED error code, got %s", errResp.Error.Code)
	}
}

// =============================================================================
// 6. Expired Subscription Read-Only Behavior
// =============================================================================

func TestExpiredSubscription_MutationsBlocked(t *testing.T) {
	h := GetTestHarness(t)
	// ExpiredTeacherUUID represents a teacher whose subscription expired
	expiredToken := GenerateTestToken(ExpiredTeacherUUID, "teacher", false)
	expiredCookie := MakeAccessCookie(expiredToken)

	// 1. POST /api/v1/rooms must return 403 SUBSCRIPTION_READ_ONLY
	resp, body := DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/rooms", map[string]any{"name": "Nova", "max_members": 20}, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for room creation under expired sub, got %d: %s", resp.StatusCode, string(body))
	}
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "SUBSCRIPTION_READ_ONLY" {
		t.Errorf("expected SUBSCRIPTION_READ_ONLY error code, got %s", errResp.Error.Code)
	}

	// 2. POST /api/v1/rooms/{id}/assignments must return 403 SUBSCRIPTION_READ_ONLY
	assignPayload := map[string]any{
		"title":    "New assignment",
		"mode":     "homework",
		"task_ids": []int{1043},
	}
	resp, body = DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/rooms/"+ExpiredRoomUUID+"/assignments", assignPayload, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for assignment creation under expired sub, got %d: %s", resp.StatusCode, string(body))
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "SUBSCRIPTION_READ_ONLY" {
		t.Errorf("expected SUBSCRIPTION_READ_ONLY code, got %s", errResp.Error.Code)
	}

	// 3. PATCH /api/v1/rooms/{id} must return 403 SUBSCRIPTION_READ_ONLY
	resp, body = DoRequest(t, http.MethodPatch, h.BaseURL+"/api/v1/rooms/"+ExpiredRoomUUID, map[string]any{"name": "Updated"}, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for room patch under expired sub, got %d: %s", resp.StatusCode, string(body))
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "SUBSCRIPTION_READ_ONLY" {
		t.Errorf("expected SUBSCRIPTION_READ_ONLY code, got %s", errResp.Error.Code)
	}

	// 4. PATCH /api/v1/assignments/{id} must return 403 SUBSCRIPTION_READ_ONLY for expired teacher
	resp, body = DoRequest(t, http.MethodPatch, h.BaseURL+"/api/v1/assignments/"+ExpiredAssignUUID, map[string]any{"title": "Updated title"}, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for assignment patch under expired sub, got %d: %s", resp.StatusCode, string(body))
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "SUBSCRIPTION_READ_ONLY" {
		t.Errorf("expected SUBSCRIPTION_READ_ONLY code, got %s", errResp.Error.Code)
	}

	// Active teacher CAN PATCH /api/v1/assignments/{id} -> 200 OK
	teacherToken := GenerateTestToken(TeacherUUID, "teacher", false)
	teacherCookie := MakeAccessCookie(teacherToken)
	resp, body = DoRequest(t, http.MethodPatch, h.BaseURL+"/api/v1/assignments/"+TestAssignUUID, map[string]any{"title": "Updated Active Title"}, []*http.Cookie{teacherCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for active teacher assignment patch, got %d: %s", resp.StatusCode, string(body))
	}

	// 5. POST /api/v1/teacher/tasks must return 403 SUBSCRIPTION_READ_ONLY for expired teacher
	teacherTaskPayload := map[string]any{
		"kim_numbers": []int{1},
		"topic_id":    101,
		"difficulty":  2,
		"source":      "Авторская задача",
		"text_md":     "Найдите значение выражения.",
		"answer_config": map[string]any{
			"answer_type": "single",
			"max_score":   1,
		},
		"correct_answer": map[string]any{
			"answer_type": "single",
			"accepted":    []string{"5"},
		},
	}
	resp, body = DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/teacher/tasks", teacherTaskPayload, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for teacher task creation under expired sub, got %d: %s", resp.StatusCode, string(body))
	}
	_ = json.Unmarshal(body, &errResp)
	if errResp.Error.Code != "SUBSCRIPTION_READ_ONLY" {
		t.Errorf("expected SUBSCRIPTION_READ_ONLY code, got %s", errResp.Error.Code)
	}

	// Active teacher CAN POST /api/v1/teacher/tasks -> 201 Created
	resp, body = DoRequest(t, http.MethodPost, h.BaseURL+"/api/v1/teacher/tasks", teacherTaskPayload, []*http.Cookie{teacherCookie}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 Created for active teacher task creation, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestExpiredSubscription_ReadsAllowed(t *testing.T) {
	h := GetTestHarness(t)
	expiredToken := GenerateTestToken(ExpiredTeacherUUID, "teacher", false)
	expiredCookie := MakeAccessCookie(expiredToken)

	// 1. GET /api/v1/assignments/{id}/matrix must return 200 OK (Live matrix remains accessible!)
	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/assignments/"+ExpiredAssignUUID+"/matrix", nil, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for matrix read under expired sub, got %d: %s", resp.StatusCode, string(body))
	}

	// 2. GET /api/v1/sessions/{id}/inspect must return 200 OK
	resp, body = DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/sessions/"+ExpiredSessionUUID+"/inspect", nil, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for inspect read under expired sub, got %d: %s", resp.StatusCode, string(body))
	}

	// 3. GET /api/v1/rooms must return 200 OK with is_read_only: true
	resp, body = DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/rooms", nil, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for rooms read under expired sub, got %d: %s", resp.StatusCode, string(body))
	}
	var roomsResp struct {
		Items []struct {
			IsReadOnly bool `json:"is_read_only"`
		} `json:"items"`
	}
	_ = json.Unmarshal(body, &roomsResp)
	if len(roomsResp.Items) > 0 && !roomsResp.Items[0].IsReadOnly {
		t.Errorf("expected is_read_only=true on room listing for expired teacher")
	}

	// 4. DELETE /api/v1/rooms/{id} must return 200 OK (Deletion permitted under expired subscription)
	resp, body = DoRequest(t, http.MethodDelete, h.BaseURL+"/api/v1/rooms/"+ExpiredRoomUUID, nil, []*http.Cookie{expiredCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for room deletion under expired sub, got %d: %s", resp.StatusCode, string(body))
	}
}

// =============================================================================
// 7. Topics, Student Dashboard, User Onboarding, and Admin Exam Scale
// =============================================================================

func TestTopics_List(t *testing.T) {
	h := GetTestHarness(t)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/topics", nil, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /api/v1/topics, got %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		Items []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			KimNumbers []int  `json:"kim_numbers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal topics error: %v", err)
	}
	if len(res.Items) == 0 {
		t.Errorf("expected non-empty topics list")
	}
}

func TestDashboard_Student(t *testing.T) {
	h := GetTestHarness(t)
	studentToken := GenerateTestToken(StudentUUID, "student", false)
	studentCookie := MakeAccessCookie(studentToken)

	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/dashboard/student", nil, []*http.Cookie{studentCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /api/v1/dashboard/student, got %d: %s", resp.StatusCode, string(body))
	}

	var dash struct {
		ActiveAssignments []any `json:"active_assignments"`
		KimMatrix         []any `json:"kim_matrix"`
		WeakTopics        []any `json:"weak_topics"`
		RecommendedTasks  []any `json:"recommended_tasks"`
	}
	if err := json.Unmarshal(body, &dash); err != nil {
		t.Fatalf("unmarshal dashboard error: %v", err)
	}
	if len(dash.KimMatrix) != 27 {
		t.Errorf("expected 27 entries in kim_matrix, got %d", len(dash.KimMatrix))
	}
	if len(dash.ActiveAssignments) == 0 {
		t.Errorf("expected non-empty active_assignments for seeded student")
	}
	if len(dash.RecommendedTasks) == 0 {
		t.Errorf("expected non-empty recommended_tasks for student dashboard")
	}
}

func TestUsers_Onboarding(t *testing.T) {
	h := GetTestHarness(t)
	targetToken := GenerateTestToken(TargetStudentUUID, "student", false)
	targetCookie := MakeAccessCookie(targetToken)

	payload := map[string]string{
		"first_name": "Иван",
		"last_name":  "Иванов",
		"username":   "ivan_ege_2026",
	}

	resp, body := DoRequest(t, http.MethodPatch, h.BaseURL+"/api/v1/users/me/onboarding", payload, []*http.Cookie{targetCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for PATCH /api/v1/users/me/onboarding, got %d: %s", resp.StatusCode, string(body))
	}

	var dto struct {
		ID          string `json:"id"`
		Username    string `json:"username"`
		FirstName   string `json:"first_name"`
		LastName    string `json:"last_name"`
		IsOnboarded bool   `json:"is_onboarded"`
	}
	if err := json.Unmarshal(body, &dto); err != nil {
		t.Fatalf("unmarshal onboarding response error: %v", err)
	}
	if !dto.IsOnboarded || dto.Username != "ivan_ege_2026" {
		t.Errorf("expected onboarded user with username ivan_ege_2026, got %+v", dto)
	}
}

func TestAdmin_ExamScale(t *testing.T) {
	h := GetTestHarness(t)
	superToken := GenerateTestToken(SuperuserUUID, "superuser", false)
	superCookie := MakeAccessCookie(superToken)

	teacherToken := GenerateTestToken(TeacherUUID, "teacher", false)
	teacherCookie := MakeAccessCookie(teacherToken)

	// 1. Non-superuser must get 403 Forbidden on GET and PUT
	resp, body := DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/admin/exam-scale", nil, []*http.Cookie{teacherCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-superuser GET /admin/exam-scale, got %d: %s", resp.StatusCode, string(body))
	}
	resp, body = DoRequest(t, http.MethodPut, h.BaseURL+"/api/v1/admin/exam-scale", map[string]any{
		"items": []map[string]int{{"primary_score": 1, "secondary_score": 7}},
	}, []*http.Cookie{teacherCookie}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for non-superuser PUT /admin/exam-scale, got %d: %s", resp.StatusCode, string(body))
	}

	// 2. Superuser GET /api/v1/admin/exam-scale -> 200 OK
	resp, body = DoRequest(t, http.MethodGet, h.BaseURL+"/api/v1/admin/exam-scale", nil, []*http.Cookie{superCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for superuser GET /admin/exam-scale, got %d: %s", resp.StatusCode, string(body))
	}
	var scaleGet struct {
		Items []struct {
			PrimaryScore   int `json:"primary_score"`
			SecondaryScore int `json:"secondary_score"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &scaleGet); err != nil {
		t.Fatalf("unmarshal scale get error: %v", err)
	}
	if len(scaleGet.Items) == 0 {
		t.Fatalf("expected non-empty exam scale items")
	}

	// 3. Superuser PUT /api/v1/admin/exam-scale -> 200 OK (preserve entry 1->7 for other tests, modify 28->99 then restore)
	resp, body = DoRequest(t, http.MethodPut, h.BaseURL+"/api/v1/admin/exam-scale", map[string]any{
		"items": []map[string]int{{"primary_score": 28, "secondary_score": 99}},
	}, []*http.Cookie{superCookie}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for superuser PUT /admin/exam-scale, got %d: %s", resp.StatusCode, string(body))
	}
	// Restore 28 -> 98 so full default scale stays intact
	_, _ = DoRequest(t, http.MethodPut, h.BaseURL+"/api/v1/admin/exam-scale", map[string]any{
		"items": scaleGet.Items,
	}, []*http.Cookie{superCookie}, nil)
}
