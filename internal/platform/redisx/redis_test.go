package redisx

import (
	"context"
	"strings"
	"testing"
)

func TestBadURLErrorHidesPassword(t *testing.T) {
	_, err := NewClient(context.Background(), Options{URL: "rediss://default:hunter2-secret@bad host:6379"})
	if err == nil {
		t.Fatal("expected an error for a malformed URL")
	}
	if strings.Contains(err.Error(), "hunter2-secret") {
		t.Fatalf("error leaks the password: %v", err)
	}
}
