package users

import (
	"strings"
	"testing"
)

func TestUsernameRules(t *testing.T) {
	for _, ok := range []string{"alice", "a_b.c", "user123", "abc", strings.Repeat("a", 25)} {
		if ValidateName(NormalizeUsername(ok)) != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	if NormalizeUsername("  Alice.W ") != "alice.w" {
		t.Error("not normalised")
	}
	for _, bad := range []string{"ab", strings.Repeat("a", 26), "has space", "dash-ed", "émile", "ထက်လင်း", "аlice" /* Cyrillic а */, "a\u200bb", ""} {
		if ValidateName(NormalizeUsername(bad)) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDisplayNameRules(t *testing.T) {
	for _, ok := range []string{"Alice Wong", "ထက်လင်း", "🌸", "Émile", strings.Repeat("x", 50)} {
		if ValidateDisplayName(ok) != nil {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", strings.Repeat("x", 51), "a\u200bb", "line\nbreak", "tab\tname", "rtl\u202e"} {
		if ValidateDisplayName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
