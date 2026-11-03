package ratelimit

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
)

// Limiter decides whether one more request for key is allowed.
type Limiter interface {
	Allow(ctx context.Context, key string) (Result, error)
}

// Result describes the outcome of a single Allow call.
type Result struct {
	Allowed   bool
	Remaining int
	Limit     int
}

// ClientKey identifies the caller: the authenticated user when available,
// otherwise the client IP (without the port).
func ClientKey(r *http.Request) string {
	if userID, err := reqctx.User(r.Context()); err == nil {
		return "user:" + userID.String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

// Middleware rejects requests over the limit with 429. name scopes the
// counter so different routes have separate budgets. If the limiter
// itself fails the request is allowed (fail open) and the error is logged.
func Middleware(l Limiter, name string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := name + ":" + ClientKey(r)
			res, err := l.Allow(r.Context(), key)
			if err != nil {
				logger.ErrorContext(r.Context(), "rate limiter failed, allowing request", "key", key, "error", err)
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(max(res.Remaining, 0)))
			if !res.Allowed {
				logger.WarnContext(r.Context(), "rate limit exceeded", "policy", name, "route", httpx.RoutePattern(r))
				httpx.Error(w, http.StatusTooManyRequests, "too many requests, please try again later")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
