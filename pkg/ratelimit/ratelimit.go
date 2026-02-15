package ratelimit

import (
	"sync"
	"time"
)

type Window struct {
	Count     int
	StartTime time.Time
}

type RateLimiter struct {
	requests map[string]*Window
	limit    int
	window   time.Duration
	mu       sync.Mutex
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string]*Window),
		limit:    limit,
		window:   window,
	}
}

func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	if window, exists := r.requests[key]; exists {
		if now.Sub(window.StartTime) > r.window {
			window.Count = 1
			window.StartTime = now
			return true
		}

		if window.Count >= r.limit {
			return false
		}

		window.Count++
		return true
	}

	r.requests[key] = &Window{
		Count:     1,
		StartTime: now,
	}
	return true
}

func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.requests, key)
}

func (r *RateLimiter) GetRemaining(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	if window, exists := r.requests[key]; exists {
		if now.Sub(window.StartTime) > r.window {
			return r.limit
		}
		return r.limit - window.Count
	}

	return r.limit
}
