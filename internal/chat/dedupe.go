package chat

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DedupeWindow: retries of one send (same sender and client_msg_id) that
// arrive at the same time share one attempt here, in memory. The lasting
// record is the sent_ids table (7 days, survives restarts), checked first;
// a small table pruned by the janitor instead of a unique column on every
// message (~15% of message storage).
const DedupeWindow = 10 * time.Minute

type dedupeKey struct{ from, client uuid.UUID }

type dedupeEntry struct {
	done chan struct{}
	id   uuid.UUID
	err  error
	at   time.Time
}

type dedupe struct {
	mu      sync.Mutex
	m       map[dedupeKey]*dedupeEntry
	now     func() time.Time
	lastGC  time.Time
	maxKeys int
}

func newDedupe() *dedupe {
	return &dedupe{m: map[dedupeKey]*dedupeEntry{}, now: time.Now, maxKeys: 200_000}
}

// do runs send once per key: a repeat (or one arriving while the first is
// still running) gets the first result. Failed attempts are forgotten, so
// the client can retry them.
func (d *dedupe) do(ctx context.Context, k dedupeKey, send func() (uuid.UUID, error)) (uuid.UUID, bool, error) {
	for {
		d.mu.Lock()
		now := d.now()
		d.gc(now)
		e, ok := d.m[k]
		if !ok || now.Sub(e.at) > DedupeWindow {
			e = &dedupeEntry{done: make(chan struct{}), at: now}
			d.m[k] = e
			d.mu.Unlock()
			id, err := send()
			e.id, e.err = id, err
			close(e.done)
			if err != nil {
				d.forget(k, e)
			}
			return id, false, err
		}
		d.mu.Unlock()
		select {
		case <-e.done:
		case <-ctx.Done():
			return uuid.Nil, false, ctx.Err()
		}
		if e.err == nil {
			return e.id, true, nil
		}
		// the first attempt failed: this one is a real retry
	}
}

func (d *dedupe) forget(k dedupeKey, e *dedupeEntry) {
	d.mu.Lock()
	if d.m[k] == e {
		delete(d.m, k)
	}
	d.mu.Unlock()
}

// gc drops expired keys now and then (under mu), and everything if the map
// somehow grows past maxKeys.
func (d *dedupe) gc(now time.Time) {
	if len(d.m) > d.maxKeys {
		clear(d.m)
	}
	if now.Sub(d.lastGC) < time.Minute {
		return
	}
	d.lastGC = now
	for k, e := range d.m {
		select {
		case <-e.done:
			if now.Sub(e.at) > DedupeWindow {
				delete(d.m, k)
			}
		default: // still running
		}
	}
}
