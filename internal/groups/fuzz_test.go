package groups

import (
	"strings"
	"testing"
)

// Whatever comes in, a normalized invite code is 8 characters from the
// code alphabet.
func FuzzNormalizeInviteCode(f *testing.F) {
	f.Add("K7M9-QX2P")
	f.Add("k7m9 qx2p")
	f.Add("../../etc")
	f.Fuzz(func(t *testing.T, s string) {
		c, err := NormalizeInviteCode(s)
		if err != nil {
			return
		}
		if len(c) != inviteLen || strings.ContainsAny(c, "01IO") || strings.ToUpper(c) != c {
			t.Fatalf("%q normalized to %q", s, c)
		}
	})
}
