package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

// Without GOOGLE_CLIENT_IDS the server must say so (503), not answer like
// a bad token would: otherwise a missing setting looks like user error.
func TestGoogleUnconfigured(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth := NewAuthService(nil, nil, "secret", 0, 0, logger)
	svc := NewAccountService(nil, nil, nil, nil, auth, NewGoogleVerifier(nil), nil, "secret", logger)
	if _, err := svc.Google(context.Background(), "any-token"); !errors.Is(err, ErrGoogleUnavailable) {
		t.Fatalf("got %v, want ErrGoogleUnavailable", err)
	}
}
