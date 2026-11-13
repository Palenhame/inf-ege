package auth

import (
	"testing"
	"time"
)

func TestJWTManager_GenerateAndValidate(t *testing.T) {
	secret := "super-secret-key-for-testing-123456"
	mgr := NewJWTManager(secret, 15*time.Minute)

	userID := "550e8400-e29b-41d4-a716-446655440000"
	role := "teacher"

	token, err := mgr.GenerateAccessToken(userID, role)
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := mgr.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("unexpected error validating token: %v", err)
	}

	if claims.Subject != userID {
		t.Errorf("expected subject %s, got %s", userID, claims.Subject)
	}
	if claims.Role != role {
		t.Errorf("expected role %s, got %s", role, claims.Role)
	}
}

func TestJWTManager_ExpiredToken(t *testing.T) {
	secret := "secret-key"
	mgr := NewJWTManager(secret, -1*time.Minute) // already expired

	token, err := mgr.GenerateAccessToken("user-1", "student")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = mgr.ValidateAccessToken(token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !mgrExpired(err) {
		t.Fatalf("expected ErrTokenExpired, got %v", err)
	}
}

func mgrExpired(err error) bool {
	return err == ErrTokenExpired || (err != nil && err.Error() == "token expired")
}

func TestJWTManager_InvalidSecret(t *testing.T) {
	mgr1 := NewJWTManager("secret-1", 15*time.Minute)
	mgr2 := NewJWTManager("secret-2", 15*time.Minute)

	token, err := mgr1.GenerateAccessToken("user-1", "student")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = mgr2.ValidateAccessToken(token)
	if err == nil {
		t.Fatal("expected error validating token with wrong secret, got nil")
	}
}

func TestJWTManager_EmptyInputs(t *testing.T) {
	mgr := NewJWTManager("secret", 15*time.Minute)

	if _, err := mgr.GenerateAccessToken("", "student"); err == nil {
		t.Error("expected error for empty userID")
	}
	if _, err := mgr.GenerateAccessToken("user-1", ""); err == nil {
		t.Error("expected error for empty role")
	}
}
