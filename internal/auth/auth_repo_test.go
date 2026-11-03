package auth

import (
	"strings"
	"testing"
)

func TestHashTokenIsStableAndNotTheToken(t *testing.T) {
	tok := strings.Repeat("ab", 32)
	h := hashToken(tok)
	if h == tok || len(h) != 64 || h != hashToken(tok) {
		t.Fatalf("hashToken(%q) = %q", tok, h)
	}
	if hashToken(tok+"x") == h {
		t.Fatal("different tokens must hash differently")
	}
}
