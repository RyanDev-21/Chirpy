package ratelimit

import (
	"log/slog"
	"net"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/middleware"
)

// clientKey identifies the caller: the authenticated user when available,
// otherwise the client IP (without the port).
func clientKey(r *http.Request) string {
	if userID, err := middleware.GetContextKey(r.Context(), "user"); err == nil && *userID != [16]byte{} {
		return "user:" + userID.String()
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

func RateLimitMiddleware(limiter *RateLimiter, next http.HandlerFunc, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := clientKey(r)
		if !limiter.Allow(key) {
			logger.Warn("rate limit exceeded", "key", key, "path", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"too many requests, please try again later"}`))
			return
		}
		next.ServeHTTP(w, r)
	}
}
