package chat

import (
	"testing"
	"unicode/utf8"
)

func FuzzValidateEmoji(f *testing.F) {
	for _, s := range []string{"👍", "❤️", "👩‍💻", "a", "", "👍👍"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if ValidateEmoji(s) == nil && (!utf8.ValidString(s) || len(s) > 64) {
			t.Fatalf("accepted %q", s)
		}
	})
}
