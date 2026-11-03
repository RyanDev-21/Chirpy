package httpx

import (
	"log/slog"
	"net/http"
	"slices"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// RequestLogger assigns a request id and logs one line per request.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := r.Header.Get("X-Request-Id")
			if reqID == "" || len(reqID) > 64 {
				reqID = uuid.NewString()
			}
			w.Header().Set("X-Request-Id", reqID)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r.WithContext(reqctx.WithRequestID(r.Context(), reqID)))
			logger.Info("request",
				"reqID", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Authenticate requires a valid bearer token. With allowQuery, ?token= is
// also accepted (for websocket upgrades from browsers).
func Authenticate(secret string, allowQuery bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := secure.GetBearerToken(r.Header)
			if err != nil && allowQuery {
				token = r.URL.Query().Get("token")
			}
			if token == "" {
				Error(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			userID, err := secure.ValidateJWT(token, secret)
			if err != nil {
				Error(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r.WithContext(reqctx.WithUser(r.Context(), userID)))
		})
	}
}

// CORS allows the configured origins ("*" allows any).
func CORS(allowed []string) func(http.Handler) http.Handler {
	any := slices.Contains(allowed, "*")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (any || slices.Contains(allowed, origin)) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
