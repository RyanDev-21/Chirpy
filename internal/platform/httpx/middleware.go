package httpx

import (
	"log/slog"
	"net/http"
	"slices"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// RequestLogger assigns a request id and writes one access-log line per
// request: route pattern, status, size, latency and (once authenticated)
// the user. 5xx responses are logged at error level.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := r.Header.Get("X-Request-Id")
			if reqID == "" || len(reqID) > 64 {
				reqID = uuid.NewString()
			}
			w.Header().Set("X-Request-Id", reqID)
			ctx, _ := reqctx.Start(r.Context(), reqID)
			r = r.WithContext(ctx)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK // hijacked (websocket) or no explicit WriteHeader
			}
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelError
			}
			logger.LogAttrs(ctx, level, "request",
				slog.String("method", r.Method),
				slog.String("route", RoutePattern(r)),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("remote", r.RemoteAddr),
			)
		})
	}
}

// RoutePattern is the matched chi route (e.g. /api/chat/{otherUser_id}/messages),
// which keeps logs and metrics low-cardinality. Unmatched requests return "unmatched".
func RoutePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "unmatched"
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
