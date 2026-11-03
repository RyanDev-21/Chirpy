package httpx

import (
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
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
//
// trustedProxies is how many reverse proxies sit in front of the server
// (Render: 1). The client IP is then the entry that many places from the
// right of X-Forwarded-For: the one added by the outermost trusted proxy.
// Anything a client writes into the header itself lands further left and is
// ignored, so it cannot be spoofed. 0 means use the TCP peer address.
func RequestLogger(logger *slog.Logger, trustedProxies int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqID := r.Header.Get("X-Request-Id")
			if reqID == "" || len(reqID) > 64 {
				reqID = uuid.NewString()
			}
			w.Header().Set("X-Request-Id", reqID)
			ctx, info := reqctx.Start(r.Context(), reqID)
			info.ClientIP = ClientIP(r, trustedProxies)
			r = r.WithContext(ctx)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK // hijacked (websocket) or no explicit WriteHeader
			}
			level := slog.LevelInfo
			switch route := RoutePattern(r); {
			case status >= 500:
				level = slog.LevelError
			case route == "/livez":
				// the platform health check and keep-alive ping hit this
				// every few seconds; keep it out of production logs
				level = slog.LevelDebug
			}
			logger.LogAttrs(ctx, level, "request",
				slog.String("method", r.Method),
				slog.String("route", RoutePattern(r)),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("remote", r.RemoteAddr),
				slog.String("client_ip", info.ClientIP),
			)
		})
	}
}

// ClientIP resolves the caller's address; see RequestLogger.
func ClientIP(r *http.Request, trustedProxies int) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	if trustedProxies <= 0 {
		return peer
	}
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, p := range strings.Split(h, ",") {
			if p = strings.TrimSpace(p); p != "" {
				hops = append(hops, p)
			}
		}
	}
	if len(hops) < trustedProxies {
		return peer // not behind the expected proxies (e.g. a direct health check)
	}
	if ip := net.ParseIP(hops[len(hops)-trustedProxies]); ip != nil {
		return ip.String()
	}
	return peer
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

// Authenticate requires a valid "Authorization: Bearer <token>" header.
// Tokens in the URL are never accepted: URLs end up in proxy, CDN and crash
// reporter logs.
func Authenticate(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := secure.GetBearerToken(r.Header)
			if err != nil {
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
