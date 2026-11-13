package rooms

import (
	"strings"
	"testing"
)

func TestGenerateInviteCode(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		code, err := GenerateInviteCode()
		if err != nil {
			t.Fatalf("unexpected error generating invite code: %v", err)
		}
		if len(code) != 8 {
			t.Errorf("expected length 8, got %d for code %s", len(code), code)
		}
		if !IsValidInviteCode(code) {
			t.Errorf("generated code %s is invalid", code)
		}
		// Check that ambiguous characters are never present
		for _, forbidden := range []rune{'0', 'O', '1', 'I', 'L', 'o', 'i', 'l'} {
			if strings.ContainsRune(code, forbidden) {
				t.Errorf("code %s contains forbidden ambiguous character %c", code, forbidden)
			}
		}
		if _, exists := seen[code]; exists {
			t.Errorf("duplicate invite code generated: %s", code)
		}
		seen[code] = struct{}{}
	}
}

func TestIsValidInviteCode(t *testing.T) {
	tests := []struct {
		code  string
		valid bool
	}{
		{"ABC12XYZ", false}, // '1' is forbidden
		{"ABC23XYZ", true},
		{"K7X9M2P4", true},
		{"K7X9M2P", false},   // length 7
		{"K7X9M2P4A", false}, // length 9
		{"K7X9M2PO", false},  // 'O' is forbidden
		{"K7X9M2PI", false},  // 'I' is forbidden
		{"K7X9M2PL", false},  // 'L' is forbidden
		{"K7X9M2P0", false},  // '0' is forbidden
		{"k7x9m2p4", false},  // lowercase
	}

	for _, tt := range tests {
		got := IsValidInviteCode(tt.code)
		if got != tt.valid {
			t.Errorf("IsValidInviteCode(%q) = %v; want %v", tt.code, got, tt.valid)
		}
	}
}
