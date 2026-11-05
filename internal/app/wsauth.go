package app

import (
	"net/http"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/ws"
)

// wsTicket handles POST /api/chats/ws/ticket (authenticated): a one-time,
// 30-second ticket for opening the websocket from a browser.
func (a *App) wsTicket(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	t, err := a.tickets.Issue(me)
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"ticket": t, "expires_in": int(ws.TicketTTL.Seconds())})
}

// wsAuth admits the websocket upgrade with the Authorization header (apps)
// or a ?ticket= (browsers, which can't set headers there).
func (a *App) wsAuth(authed func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		withHeader := authed(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ticket := r.URL.Query().Get("ticket")
			if r.Header.Get("Authorization") != "" || ticket == "" {
				withHeader.ServeHTTP(w, r)
				return
			}
			user, issued, ok := a.tickets.Redeem(ticket)
			if !ok {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			// sessions ended after the ticket was made must not connect
			if gone, err := a.revoked.Revoked(r.Context(), user, issued); err != nil || gone {
				if err != nil {
					a.logger.ErrorContext(r.Context(), "revocation check failed for websocket ticket", "error", err)
				}
				httpx.Error(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r.WithContext(reqctx.WithUser(r.Context(), user)))
		})
	}
}
