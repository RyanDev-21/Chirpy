package queue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/testutil"
)

type job struct {
	N int `json:"n"`
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMemoryDeliversTypedJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewMemory(3, testutil.Logger())
	var sum atomic.Int64
	_ = q.Subscribe(ctx, "t", 2, Typed(func(_ context.Context, j job) error {
		sum.Add(int64(j.N))
		return nil
	}))
	for i := 1; i <= 4; i++ {
		if err := q.Publish(ctx, "t", job{N: i}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return sum.Load() == 10 })
}

func TestMemoryRetriesThenSucceeds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewMemory(5, testutil.Logger())
	var calls atomic.Int32
	_ = q.Subscribe(ctx, "t", 1, Typed(func(_ context.Context, _ job) error {
		if calls.Add(1) < 3 {
			return errors.New("transient")
		}
		return nil
	}))
	_ = q.Publish(ctx, "t", job{})
	waitFor(t, func() bool { return calls.Load() == 3 })
}

func TestMemoryDropsPermanentErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := NewMemory(5, testutil.Logger())
	var calls atomic.Int32
	_ = q.Subscribe(ctx, "t", 1, Typed(func(_ context.Context, _ job) error {
		calls.Add(1)
		return Permanent(errors.New("bad"))
	}))
	_ = q.Publish(ctx, "t", job{})
	waitFor(t, func() bool { return calls.Load() == 1 })
	time.Sleep(Backoff(1) + 100*time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("permanent error retried: %d calls", calls.Load())
	}
}

func TestTypedRejectsBadPayload(t *testing.T) {
	h := Typed(func(context.Context, job) error { return nil })
	if err := h(context.Background(), []byte(`{"n":"nope"}`)); !IsPermanent(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
}

func TestBackoffIsCapped(t *testing.T) {
	if Backoff(1) != 200*time.Millisecond {
		t.Fatalf("Backoff(1) = %v", Backoff(1))
	}
	if Backoff(100) != 30*time.Second {
		t.Fatalf("Backoff(100) = %v", Backoff(100))
	}
}

// Jobs still queued when the workers stop are run by Drain, not dropped.
func TestMemoryDrainRunsWaitingJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := NewMemory(3, testutil.Logger())
	var done atomic.Int64
	_ = q.Subscribe(ctx, "t", 1, Typed(func(_ context.Context, j job) error {
		done.Add(1)
		return nil
	}))
	cancel() // workers stop: nothing below is picked up by them
	q.Wait()
	for i := range 50 {
		if err := q.Publish(context.Background(), "t", job{N: i}); err != nil {
			t.Fatal(err)
		}
	}
	if left := q.Drain(context.Background()); left != 0 || done.Load() != 50 {
		t.Fatalf("left %d, done %d", left, done.Load())
	}
	// out of time: what's left is reported
	for i := range 5 {
		_ = q.Publish(context.Background(), "t", job{N: i})
	}
	over, stop := context.WithCancel(context.Background())
	stop()
	if left := q.Drain(over); left != 5 {
		t.Fatalf("expired drain left %d, want 5", left)
	}
}
