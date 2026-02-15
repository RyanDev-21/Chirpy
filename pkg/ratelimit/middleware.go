package ratelimit

import (
	"log/slog"
	"net/http"

	"RyanDev-21.com/Chirpy/pkg/middleware"
)

func RateLimitMiddleware(limiter *RateLimiter, next http.HandlerFunc, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := middleware.GetContextKey(r.Context(), "user")
		if err != nil {
			userIDStr := r.RemoteAddr
			if userID != nil {
				userIDStr = userID.String()
			}
			if !limiter.Allow(userIDStr) {
				logger.Warn("rate limit exceeded", "userID", userIDStr, "path", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error": "too many requests, please try again later"}`))
				return
			}
		} else {
			if !limiter.Allow(userID.String()) {
				logger.Warn("rate limit exceeded", "userID", userID.String(), "path", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error": "too many requests, please try again later"}`))
				return
			}
		}

		next.ServeHTTP(w, r)
	}
}
