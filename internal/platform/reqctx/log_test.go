package reqctx

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestLogHandlerAddsRequestAndUser(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "json", "info")
	ctx, _ := Start(context.Background(), "req-123")
	user := uuid.New()
	ctx = WithUser(ctx, user)

	logger.InfoContext(ctx, "hello")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["req_id"] != "req-123" || rec["user_id"] != user.String() {
		t.Fatalf("record = %v", rec)
	}
}

func TestUserSetDeeperIsVisibleToOuterContext(t *testing.T) {
	// the access logger holds the outer ctx; auth runs later on a derived one
	outer, info := Start(context.Background(), "r")
	type otherKey struct{}
	inner := WithUser(context.WithValue(outer, otherKey{}, 1), uuid.New())
	if _, err := User(inner); err != nil {
		t.Fatal(err)
	}
	if _, ok := info.UserID(); !ok {
		t.Fatal("outer Info should see the user set on a derived context")
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "text", "warn")
	logger.Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("info logged at warn level: %s", buf.String())
	}
	logger.Warn("shown")
	if buf.Len() == 0 {
		t.Fatal("warn not logged")
	}
}
