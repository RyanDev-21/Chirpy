//go:build integration

package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"github.com/redis/go-redis/v9"
)

type job struct {
	ID int `json:"id"`
}

func newQueue(t *testing.T) (*queue.Redis, *redis.Client, context.Context) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	q := queue.NewRedis(rdb, queue.RedisOptions{MaxRetries: 3, BlockFor: 100 * time.Millisecond}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		cancel()
		q.Wait()
		_ = rdb.Close()
	})
	return q, rdb, ctx
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestQueueDeliversJobsPublishedBeforeSubscribe(t *testing.T) {
	q, _, ctx := newQueue(t)
	for i := range 5 {
		if err := q.Publish(ctx, "t", job{ID: i}); err != nil {
			t.Fatal(err)
		}
	}
	var n atomic.Int32
	if err := q.Subscribe(ctx, "t", 2, queue.Typed(func(context.Context, job) error { n.Add(1); return nil })); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return n.Load() == 5 })
}

func TestQueueRetriesWithBackoff(t *testing.T) {
	q, _, ctx := newQueue(t)
	var calls atomic.Int32
	_ = q.Subscribe(ctx, "t", 1, queue.Typed(func(context.Context, job) error {
		if calls.Add(1) < 3 {
			return errors.New("transient")
		}
		return nil
	}))
	_ = q.Publish(ctx, "t", job{ID: 1})
	waitUntil(t, func() bool { return calls.Load() == 3 })
}

func TestQueueDeadLetters(t *testing.T) {
	q, rdb, ctx := newQueue(t)
	var permanentCalls, failingCalls atomic.Int32
	_ = q.Subscribe(ctx, "perm", 1, queue.Typed(func(context.Context, job) error {
		permanentCalls.Add(1)
		return queue.Permanent(errors.New("bad data"))
	}))
	_ = q.Subscribe(ctx, "fail", 1, queue.Typed(func(context.Context, job) error {
		failingCalls.Add(1)
		return errors.New("always")
	}))
	_ = q.Publish(ctx, "perm", job{ID: 1})
	_ = q.Publish(ctx, "fail", job{ID: 2})

	waitUntil(t, func() bool { return rdb.XLen(ctx, "jobs:dead").Val() == 2 })
	if permanentCalls.Load() != 1 {
		t.Fatalf("permanent job ran %d times", permanentCalls.Load())
	}
	if failingCalls.Load() != 3 {
		t.Fatalf("failing job ran %d times, want MaxRetries=3", failingCalls.Load())
	}
	if n := rdb.ZCard(ctx, "jobs:delayed").Val(); n != 0 {
		t.Fatalf("%d jobs still delayed", n)
	}
}

func TestQueueReclaimsAbandonedJobs(t *testing.T) {
	q, rdb, ctx := newQueue(t)
	// simulate a consumer that read a job and crashed before acking
	if err := rdb.XGroupCreateMkStream(ctx, "jobs:t", "chirpy", "0").Err(); err != nil {
		t.Fatal(err)
	}
	_ = q.Publish(ctx, "t", job{ID: 7})
	if err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: "chirpy", Consumer: "dead", Streams: []string{"jobs:t", ">"}, Count: 1}).Err(); err != nil {
		t.Fatal(err)
	}

	ctx2, cancel2 := context.WithCancel(ctx)
	q2 := queue.NewRedis(rdb, queue.RedisOptions{ClaimIdle: 200 * time.Millisecond, BlockFor: 100 * time.Millisecond}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { cancel2(); q2.Wait() })
	var got atomic.Int32
	_ = q2.Subscribe(ctx2, "t", 1, queue.Typed(func(_ context.Context, j job) error { got.Store(int32(j.ID)); return nil }))
	waitUntil(t, func() bool { return got.Load() == 7 })
}
