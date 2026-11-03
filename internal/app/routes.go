package app

import (
	"context"
	"net/http"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/ratelimit"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// limitPolicy is a named request budget per caller.
type limitPolicy struct {
	name   string
	limit  int
	period time.Duration
}

var (
	limitAuth     = limitPolicy{"auth", 10, time.Minute}
	limitRegister = limitPolicy{"register", 5, time.Minute}
	limitSearch   = limitPolicy{"search", 20, time.Minute}
	limitFriendRq = limitPolicy{"friend_request", 10, time.Minute}
	limitFriendUp = limitPolicy{"friend_update", 30, time.Minute}
	limitSend     = limitPolicy{"send_message", 120, time.Minute}
	limitWS       = limitPolicy{"ws_connect", 20, time.Minute}
	limitAI       = limitPolicy{"ai", 5, time.Minute}
)

// Router builds the HTTP handler. Middleware is attached per route group so
// what protects a route is visible next to the route itself.
func (a *App) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(httpx.RequestLogger(a.logger))
	r.Use(chimw.Recoverer)
	r.Use(httpx.CORS(a.cfg.CORSOrigins))

	limit := func(p limitPolicy) func(http.Handler) http.Handler {
		return ratelimit.Middleware(a.limiter(p), p.name, a.logger)
	}
	authed := httpx.Authenticate(a.cfg.JWTSecret, false)

	r.Get("/healthz", a.health)

	r.Route("/api", func(r chi.Router) {
		// public
		r.With(limit(limitRegister)).Post("/users", a.handlers.users.Register)
		r.Group(func(r chi.Router) {
			r.Use(limit(limitAuth))
			r.Post("/login", a.handlers.auth.Login)
			r.Post("/refresh", a.handlers.auth.RefreshToken)
			r.Post("/revoke", a.handlers.auth.Revoke)
		})

		// websocket: browsers cannot set headers on upgrade, so ?token= is allowed here only
		r.With(httpx.Authenticate(a.cfg.JWTSecret, true), limit(limitWS)).Get("/chats/ws", a.handlers.chat.ServeWs)

		// authenticated
		r.Group(func(r chi.Router) {
			r.Use(authed)

			r.Post("/users/password", a.handlers.users.UpdatePassword)
			r.With(limit(limitSearch)).Get("/users/search", a.handlers.users.SearchUser)
			r.Get("/users/configs", a.handlers.users.GetConfig)
			r.Post("/users/configs", a.handlers.users.SaveConfig)

			r.Route("/friends", func(r chi.Router) {
				r.Get("/", a.handlers.friends.List)
				r.Get("/requests", a.handlers.friends.Requests)
				r.With(limit(limitFriendRq)).Post("/requests", a.handlers.friends.Send)
				r.With(limit(limitFriendUp)).Put("/requests/{request_id}", a.handlers.friends.Update)
				r.With(limit(limitFriendUp)).Delete("/requests/{request_id}", a.handlers.friends.Cancel)
			})

			r.Route("/chats/groups", func(r chi.Router) {
				r.Post("/", a.handlers.groups.CreateGroup)
				r.Post("/{group_id}/members", a.handlers.groups.JoinGroup)
				r.Delete("/{group_id}/members", a.handlers.groups.LeaveGroup)
			})
			r.With(limit(limitSend)).Post("/chats/message", a.handlers.chat.SendMessage)

			r.Route("/chat", func(r chi.Router) {
				r.Get("/{otherUser_id}/messages", a.handlers.chat.GetMesagesForPrivateID)
				r.Post("/{otherUser_id}/messages", a.handlers.chat.UpdateSeen)
				r.Get("/groups/{group_id}/messages", a.handlers.chat.GetMessagesForPublicID)
			})

			if a.handlers.ai != nil {
				r.With(limit(limitAI)).Get("/ai/suggestions", a.handlers.ai.Suggest)
			}
		})
	})

	// admin routes only exist on dev and still require a logged-in user
	if a.cfg.IsDev() {
		r.With(authed).Post("/admin/reset", a.handlers.admin.ResetUsers)
	}
	return r
}

func (a *App) limiter(p limitPolicy) ratelimit.Limiter {
	return ratelimit.NewMemory(p.limit, p.period)
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	status := map[string]string{"postgres": "ok", "redis": "ok"}
	code := http.StatusOK
	if err := a.db.Ping(ctx); err != nil {
		status["postgres"], code = "down", http.StatusServiceUnavailable
	}
	if err := a.redis.Ping(ctx).Err(); err != nil {
		status["redis"], code = "down", http.StatusServiceUnavailable
	}
	httpx.JSON(w, code, status)
}
