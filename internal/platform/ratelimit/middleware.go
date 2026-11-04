package ratelimit

import (
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"context"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

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
	// RetryAfter is how long until the current window ends (when denied).
	RetryAfter time.Duration
}

// ClientKey identifies the caller: the authenticated user when available,
// otherwise the client IP (without the port).
func ClientKey(r *http.Request) string {
	if userID, err := reqctx.User(r.Context()); err == nil {
		return "user:" + userID.String()
	}
	if ip := reqctx.ClientIP(r.Context()); ip != "" {
		return "ip:" + limitKeyForIP(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

// limitKeyForIP groups IPv6 addresses by /64: one phone or home network
// usually gets a whole /64 and can rotate through its addresses freely, so
// limiting single IPv6 addresses would be trivial to dodge.
func limitKeyForIP(s string) string {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() != nil {
		return s
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
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
				if secs := int(math.Ceil(res.RetryAfter.Seconds())); secs > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(secs))
				}
				telemetry.RateLimited(r.Context(), name)
				logger.WarnContext(r.Context(), "rate limit exceeded", "policy", name, "route", httpx.RoutePattern(r))
				httpx.Error(w, http.StatusTooManyRequests, "too many requests, please try again later")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
