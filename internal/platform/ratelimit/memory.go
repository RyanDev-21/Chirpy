package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Memory is a fixed-window limiter for a single process. Use Redis in
// production so limits hold across instances.
type Memory struct {
	mu       sync.Mutex
	windows  map[string]*window
	limit    int
	period   time.Duration
	lastScan time.Time
}

type window struct {
	count int
	start time.Time
}

func NewMemory(limit int, period time.Duration) *Memory {
	return &Memory{windows: make(map[string]*window), limit: limit, period: period}
}

func (m *Memory) Allow(_ context.Context, key string) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if now.Sub(m.lastScan) > m.period {
		for k, w := range m.windows {
			if now.Sub(w.start) > m.period {
				delete(m.windows, k)
			}
		}
		m.lastScan = now
	}
	w, ok := m.windows[key]
	if !ok || now.Sub(w.start) > m.period {
		w = &window{start: now}
		m.windows[key] = w
	}
	if w.count >= m.limit {
		return Result{Allowed: false, Remaining: 0, Limit: m.limit}, nil
	}
	w.count++
	return Result{Allowed: true, Remaining: m.limit - w.count, Limit: m.limit}, nil
}
