package users

import (
	"regexp"
	"testing"
	"unicode"
	"unicode/utf8"
)

var usernameRE = regexp.MustCompile(`^[a-z0-9_.]{3,25}$`)

func FuzzUsernames(f *testing.F) {
	for _, s := range []string{"alice", "Al ice", "a\u200bb", "ünï", "..."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := NormalizeUsername(s)
		if ValidateName(n) == nil && !usernameRE.MatchString(n) {
			t.Fatalf("accepted username %q", n)
		}
		if ValidateDisplayName(s) == nil {
			if !utf8.ValidString(s) || utf8.RuneCountInString(s) > 50 {
				t.Fatalf("accepted display name %q", s)
			}
			for _, r := range s {
				if unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
					t.Fatalf("invisible/control character %U accepted in %q", r, s)
				}
			}
		}
	})
}
