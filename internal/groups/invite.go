package groups

import (
	"crypto/rand"
	"errors"
	"strings"
)

// Invite codes are 8 characters from a 32-letter alphabet without
// look-alikes (no 0/O, 1/I): 32^8 ≈ 1.1e12 codes, about 40 bits, so they
// can't be guessed within the join rate limit. Shown as XXXX-XXXX.
const (
	inviteAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	inviteLen      = 8
)

var ErrInvalidInviteCode = errors.New("invalid invite code")

// NewInviteCode returns a random code in storage form (no dash).
func NewInviteCode() (string, error) {
	buf := make([]byte, inviteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, inviteLen)
	for i, b := range buf {
		out[i] = inviteAlphabet[int(b)%len(inviteAlphabet)] // 256 % 32 == 0: no bias
	}
	return string(out), nil
}

// NormalizeInviteCode accepts what a user types ("k7m9-qx2p", "K7M9 QX2P")
// and returns the storage form, or ErrInvalidInviteCode.
func NormalizeInviteCode(in string) (string, error) {
	var b strings.Builder
	for _, r := range strings.ToUpper(in) {
		switch {
		case r == '-' || r == ' ':
			continue
		case strings.ContainsRune(inviteAlphabet, r):
			b.WriteRune(r)
		default:
			return "", ErrInvalidInviteCode
		}
	}
	if b.Len() != inviteLen {
		return "", ErrInvalidInviteCode
	}
	return b.String(), nil
}

// FormatInviteCode turns the storage form into the display form XXXX-XXXX.
func FormatInviteCode(code string) string {
	if len(code) != inviteLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}
