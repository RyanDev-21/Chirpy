package ratelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/testutil"
	"github.com/google/uuid"
)

func exhaust(t *testing.T, l Limiter, key string, n int) {
	t.Helper()
	for i := range n {
		res, err := l.Allow(context.Background(), key)
		if err != nil || !res.Allowed {
			t.Fatalf("request %d rejected early: %+v %v", i+1, res, err)
		}
	}
	res, err := l.Allow(context.Background(), key)
	if err != nil || res.Allowed {
		t.Fatalf("request %d allowed over the limit: %+v %v", n+1, res, err)
	}
}

func TestMemoryLimiter(t *testing.T) {
	l := NewMemory(3, time.Minute)
	exhaust(t, l, "a", 3)
	if res, _ := l.Allow(context.Background(), "b"); !res.Allowed {
		t.Fatal("keys must be independent")
	}
}

func TestRedisLimiter(t *testing.T) {
	rdb, _ := testutil.Redis(t)
	l := NewRedis(rdb, 5, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return now }

	exhaust(t, l, "k", 5)

	// two windows later the previous window no longer counts
	now = now.Add(2 * time.Minute)
	if res, err := l.Allow(context.Background(), "k"); err != nil || !res.Allowed {
		t.Fatalf("expected a fresh window: %+v %v", res, err)
	}
}

func TestRedisLimiterSlidesAcrossWindows(t *testing.T) {
	rdb, _ := testutil.Redis(t)
	l := NewRedis(rdb, 10, time.Minute)
	start := time.UnixMilli(60_000 * 1000) // aligned to a window
	l.now = func() time.Time { return start.Add(59 * time.Second) }
	exhaust(t, l, "k", 10)

	// 1s into the next window the previous one still weighs 59/60, so the
	// estimate is ~9.8: one more request fits, the next does not
	l.now = func() time.Time { return start.Add(61 * time.Second) }
	if res, _ := l.Allow(context.Background(), "k"); !res.Allowed {
		t.Fatal("expected exactly one request to fit after the boundary")
	}
	if res, _ := l.Allow(context.Background(), "k"); res.Allowed {
		t.Fatal("sliding window should block a burst right after the boundary")
	}
}

func TestMiddleware(t *testing.T) {
	l := NewMemory(1, time.Minute)
	h := Middleware(l, "test", testutil.Logger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	call := func(r *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	if rec := call(req); rec.Code != 200 || rec.Header().Get("X-RateLimit-Limit") != "1" {
		t.Fatalf("first call: %d %v", rec.Code, rec.Header())
	}
	// same IP, different port: still the same caller
	req.RemoteAddr = "10.0.0.1:9999"
	if rec := call(req); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second call code = %d", rec.Code)
	}
	// an authenticated user gets their own budget
	authed := req.WithContext(reqctx.WithUser(req.Context(), uuid.New()))
	if rec := call(authed); rec.Code != 200 {
		t.Fatalf("authenticated call code = %d", rec.Code)
	}
}

type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string) (Result, error) {
	return Result{}, context.DeadlineExceeded
}

func TestMiddlewareFailsOpen(t *testing.T) {
	h := Middleware(failingLimiter{}, "t", testutil.Logger())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d, want 200 when the limiter is down", rec.Code)
	}
}

func TestLimitKeyGroupsIPv6By64(t *testing.T) {
	a := limitKeyForIP("2001:fb1:59:52b3:f5ee:4b03:e22f:ca7d")
	b := limitKeyForIP("2001:fb1:59:52b3::1")
	c := limitKeyForIP("2001:fb1:59:52b4::1")
	if a != b || a != "2001:fb1:59:52b3::/64" {
		t.Fatalf("same /64 must share a key: %q %q", a, b)
	}
	if a == c {
		t.Fatal("different /64s must not share a key")
	}
	if got := limitKeyForIP("171.96.138.164"); got != "171.96.138.164" {
		t.Fatalf("IPv4 must stay as-is, got %q", got)
	}
}
