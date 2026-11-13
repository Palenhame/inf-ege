package rooms

import (
	"crypto/rand"
	"math/big"
	"strings"
)

// InviteCodeCharset contains uppercase letters and numbers excluding ambiguous characters:
// 0, O, 1, I, L.
const InviteCodeCharset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
const InviteCodeLength = 8

// GenerateInviteCode produces a cryptographically secure 8-character uppercase invite code.
func GenerateInviteCode() (string, error) {
	charsetLen := big.NewInt(int64(len(InviteCodeCharset)))
	var sb strings.Builder
	sb.Grow(InviteCodeLength)

	for i := 0; i < InviteCodeLength; i++ {
		idx, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", err
		}
		sb.WriteByte(InviteCodeCharset[idx.Int64()])
	}

	return sb.String(), nil
}

// IsValidInviteCode checks whether the given code matches length and allowed charset.
func IsValidInviteCode(code string) bool {
	if len(code) != InviteCodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !strings.ContainsRune(InviteCodeCharset, rune(code[i])) {
			return false
		}
	}
	return true
}
