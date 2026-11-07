package chat

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDedupe(t *testing.T) {
	d := newDedupe()
	now := time.Now()
	d.now = func() time.Time { return now }
	k := dedupeKey{uuid.New(), uuid.New()}
	var calls atomic.Int32
	send := func() (uuid.UUID, error) { calls.Add(1); time.Sleep(20 * time.Millisecond); return uuid.New(), nil }

	// many retries at once: one send, everyone gets its id
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) { defer wg.Done(); ids[i], _, _ = d.do(context.Background(), k, send) }(i)
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("sent %d times", calls.Load())
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("retries got different ids")
		}
	}
	if id, replayed, _ := d.do(context.Background(), k, send); !replayed || id != ids[0] {
		t.Fatal("a later retry wasn't recognised")
	}
	// another sender's identical client id is independent
	if _, replayed, _ := d.do(context.Background(), dedupeKey{uuid.New(), k.client}, send); replayed {
		t.Fatal("keys must be per sender")
	}
	// after the window it's a new message
	now = now.Add(DedupeWindow + time.Second)
	if _, replayed, _ := d.do(context.Background(), k, send); replayed {
		t.Fatal("remembered past the window")
	}

	// a failed send is forgotten, so the retry really sends
	k2 := dedupeKey{uuid.New(), uuid.New()}
	if _, _, err := d.do(context.Background(), k2, func() (uuid.UUID, error) { return uuid.Nil, errors.New("db down") }); err == nil {
		t.Fatal("error lost")
	}
	if id, replayed, err := d.do(context.Background(), k2, send); err != nil || replayed || id == uuid.Nil {
		t.Fatalf("retry after failure: %v %v %v", id, replayed, err)
	}
}
