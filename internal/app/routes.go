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
	// joining and previewing invites share a small budget so codes can't
	// be brute-forced (40-bit codes at 20/min would take ~100,000 years)
	limitGroupJoin = limitPolicy{"group_join", 20, time.Minute}
	// verification, reset and Google sign-in; codes also die after 5 wrong tries
	limitAccount = limitPolicy{"account", 10, time.Minute}
	limitReact   = limitPolicy{"react", 60, time.Minute}
	limitEdit    = limitPolicy{"edit_message", 60, time.Minute}
	// each upload decodes and resizes an image
	limitAvatar = limitPolicy{"avatar_upload", 10, time.Minute}
	// every authenticated route also shares one per-user budget, so routes
	// without a tighter policy (reads, seen markers) can't be hammered
	limitAPI = limitPolicy{"api", 300, time.Minute}
	// each change-password request runs argon2 (deliberately slow)
	limitPassword    = limitPolicy{"password", 5, time.Minute}
	limitGroupCreate = limitPolicy{"group_create", 10, time.Minute}
	limitInviteReset = limitPolicy{"invite_reset", 10, time.Minute}
	limitGroupAdmin  = limitPolicy{"group_admin", 30, time.Minute}
	// readyz pings Postgres and Redis; per IP so it can't be used to keep
	// the free-tier database awake or burn Redis commands
	limitHealth = limitPolicy{"health", 30, time.Minute}
)

// Router builds the HTTP handler. Middleware is attached per route group so
// what protects a route is visible next to the route itself.
func (a *App) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestLogger(a.logger, a.cfg.TrustedProxies))
	r.Use(chimw.Recoverer)
	r.Use(httpx.CORS(a.cfg.CORSOrigins))

	limit := func(p limitPolicy) func(http.Handler) http.Handler {
		return ratelimit.Middleware(a.limiter(p), p.name, a.logger)
	}
	authed := httpx.Authenticate(a.cfg.JWTSecret)

	// livez only proves the process is up and touches nothing else, so an
	// external keep-alive ping does not keep the database awake. readyz
	// checks dependencies (healthz is kept as an alias).
	r.Get("/livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r.With(limit(limitHealth)).Get("/readyz", a.health)
	r.With(limit(limitHealth)).Get("/healthz", a.health)

	r.Route("/api", func(r chi.Router) {
		// public
		r.With(limit(limitRegister)).Post("/users", a.handlers.account.Register)
		r.Route("/auth", func(r chi.Router) {
			r.Use(limit(limitAccount))
			r.Post("/verify-email", a.handlers.account.VerifyEmail)
			r.Post("/resend-verification", a.handlers.account.ResendVerification)
			r.Post("/password-reset", a.handlers.account.RequestPasswordReset)
			r.Post("/password-reset/confirm", a.handlers.account.ResetPassword)
			r.Post("/google", a.handlers.account.Google)
			r.Post("/google/complete", a.handlers.account.CompleteGoogle)
		})
		r.Group(func(r chi.Router) {
			r.Use(limit(limitAuth))
			r.Post("/login", a.handlers.auth.Login)
			r.Post("/refresh", a.handlers.auth.RefreshToken)
			r.Post("/revoke", a.handlers.auth.Revoke)
		})

		// websocket: the Android client sends the Authorization header on the
		// upgrade request (OkHttp/Ktor support it); ?token= is not accepted
		r.With(authed, limit(limitAPI), limit(limitWS)).Get("/chats/ws", a.handlers.chat.ServeWs)

		// authenticated
		r.Group(func(r chi.Router) {
			r.Use(authed)
			r.Use(limit(limitAPI))

			r.Get("/users/me", a.handlers.users.Me)
			r.With(limit(limitAccount)).Patch("/users/me", a.handlers.users.UpdateMe)
			r.With(limit(limitAccount)).Delete("/users/me", a.handlers.account.DeleteAccount)
			r.With(limit(limitPassword)).Post("/users/password", a.handlers.users.UpdatePassword)
			r.With(limit(limitSearch)).Get("/users/search", a.handlers.users.SearchUser)
			r.Get("/users/blocks", a.handlers.friends.Blocked)
			r.With(limit(limitFriendUp)).Post("/users/blocks", a.handlers.friends.Block)
			r.With(limit(limitFriendUp)).Delete("/users/blocks/{user_id}", a.handlers.friends.Unblock)
			r.Get("/users/{user_id}", a.handlers.users.Profile)
			r.Get("/users/{user_id}/avatar", a.handlers.users.Avatar)
			r.With(limit(limitAvatar)).Put("/users/me/avatar", a.handlers.users.UploadAvatar)
			r.With(limit(limitAvatar)).Delete("/users/me/avatar", a.handlers.users.DeleteAvatar)

			r.Route("/friends", func(r chi.Router) {
				r.Get("/", a.handlers.friends.List)
				r.Get("/requests", a.handlers.friends.Requests)
				r.With(limit(limitFriendRq)).Post("/requests", a.handlers.friends.Send)
				r.With(limit(limitFriendUp)).Put("/requests/{request_id}", a.handlers.friends.Update)
				r.With(limit(limitFriendUp)).Delete("/requests/{request_id}", a.handlers.friends.Cancel)
				r.With(limit(limitFriendUp)).Delete("/{user_id}", a.handlers.friends.Unfriend)
			})

			r.Route("/chats/groups", func(r chi.Router) {
				r.Get("/", a.handlers.groups.MyGroups)
				r.With(limit(limitGroupCreate)).Post("/", a.handlers.groups.CreateGroup)
				r.Get("/{group_id}", a.handlers.groups.Detail)
				r.Delete("/{group_id}/members", a.handlers.groups.LeaveGroup)
				// leader only
				r.With(limit(limitGroupAdmin)).Patch("/{group_id}", a.handlers.groups.UpdateGroup)
				r.With(limit(limitGroupAdmin)).Delete("/{group_id}", a.handlers.groups.DeleteGroup)
				r.With(limit(limitGroupAdmin)).Delete("/{group_id}/members/{user_id}", a.handlers.groups.KickMember)
				r.With(limit(limitGroupAdmin)).Put("/{group_id}/leader", a.handlers.groups.TransferLeader)
				// joining needs the invite code: groups are private
				r.With(limit(limitGroupJoin)).Post("/join", a.handlers.groups.JoinByInvite)
				r.With(limit(limitGroupJoin)).Get("/invites/{code}", a.handlers.groups.PreviewInvite)
				r.Get("/{group_id}/invite", a.handlers.groups.InviteCode)
				r.With(limit(limitInviteReset)).Post("/{group_id}/invite", a.handlers.groups.RegenerateInvite)
			})
			r.With(limit(limitSend)).Post("/chats/message", a.handlers.chat.SendMessage)
			r.Get("/chats", a.handlers.chat.Conversations)

			r.Route("/chat", func(r chi.Router) {
				r.Get("/{otherUser_id}/messages", a.handlers.chat.GetMesagesForPrivateID)
				r.Post("/{otherUser_id}/messages", a.handlers.chat.UpdateSeen)
				r.Get("/groups/{group_id}/messages", a.handlers.chat.GetMessagesForPublicID)
				r.Post("/groups/{group_id}/messages", a.handlers.chat.MarkGroupRead)
				r.With(limit(limitReact)).Put("/messages/{msg_id}/reaction", a.handlers.chat.React)
				r.With(limit(limitReact)).Delete("/messages/{msg_id}/reaction", a.handlers.chat.Unreact)
				r.Get("/messages/{msg_id}/reactions", a.handlers.chat.Reactors)
				r.With(limit(limitEdit)).Patch("/messages/{msg_id}", a.handlers.chat.EditMessage)
				r.With(limit(limitEdit)).Delete("/messages/{msg_id}", a.handlers.chat.DeleteMessage)
			})
		})
	})

	// admin routes only exist on dev and still require a logged-in user
	if a.cfg.IsDev() {
		r.With(authed).Post("/admin/reset", a.handlers.admin.ResetUsers)
	}
	return r
}

func (a *App) limiter(p limitPolicy) ratelimit.Limiter {
	return ratelimit.NewRedis(a.redis, p.limit, p.period)
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
