package admin

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"github.com/google/uuid"
)

//go:embed web
var web embed.FS

// Revoked reports whether a user's sessions were ended after issuedAt.
type Revoked interface {
	Revoked(ctx context.Context, userID uuid.UUID, issuedAt time.Time) (bool, error)
}

type ctxKey struct{}

// Authenticate admits only a valid admin-page token (password + emailed
// code), of a user who is still an admin and whose sessions weren't ended.
func Authenticate(secret string, svc *Service, revoked Revoked, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok, err := secure.GetBearerToken(r.Header)
			if err != nil {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			id, issued, err := secure.ValidateAdminJWT(tok, secret)
			if err != nil {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if revoked != nil {
				if gone, err := revoked.Revoked(r.Context(), id, issued); err != nil || gone {
					if err != nil {
						logger.ErrorContext(r.Context(), "revocation check failed on admin request", "error", err)
					}
					httpx.Error(w, http.StatusUnauthorized, "unauthorized") // fail closed for admins
					return
				}
			}
			ok, err := svc.IsActiveAdmin(r.Context(), id)
			if err != nil {
				httpx.ServerError(w, r, err)
				return
			}
			if !ok {
				httpx.Error(w, http.StatusForbidden, "not an admin")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
		})
	}
}

func adminID(r *http.Request) uuid.UUID {
	id, _ := r.Context().Value(ctxKey{}).(uuid.UUID)
	return id
}

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrBanSelf), errors.Is(err, ErrBanAdmin), errors.Is(err, ErrBadBan), errors.Is(err, ErrNotBanned):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		httpx.ServerError(w, r, err)
	}
}

func intParam(r *http.Request, k string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(k))
	return n
}

// Overview handles GET /api/admin/overview.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.svc.Overview(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

// Users handles GET /api/admin/users?q=&limit=&offset= (name or email).
func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	list, err := h.svc.Users(r.Context(), q, intParam(r, "limit"), intParam(r, "offset"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if q != "" {
		h.svc.Log(r.Context(), adminID(r), "search_users", uuid.Nil, uuid.Nil, q)
	}
	httpx.JSON(w, http.StatusOK, map[string][]User{"users": list})
}

// User handles GET /api/admin/users/{user_id}.
func (h *Handler) User(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := h.svc.User(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.svc.Log(r.Context(), adminID(r), "view_user", id, uuid.Nil, "")
	httpx.JSON(w, http.StatusOK, u)
}

// Groups handles GET /api/admin/groups?q=&limit=&offset=.
func (h *Handler) Groups(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	list, err := h.svc.Groups(r.Context(), q, intParam(r, "limit"), intParam(r, "offset"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if q != "" {
		h.svc.Log(r.Context(), adminID(r), "search_groups", uuid.Nil, uuid.Nil, q)
	}
	httpx.JSON(w, http.StatusOK, map[string][]Group{"groups": list})
}

// Audit handles GET /api/admin/audit?limit=.
func (h *Handler) Audit(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.Audit(r.Context(), intParam(r, "limit"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]Action{"actions": list})
}

// Ban handles POST /api/admin/users/{user_id}/ban {"reason","until"?}.
func (h *Handler) Ban(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var b struct {
		Reason string     `json:"reason"`
		Until  *time.Time `json:"until"`
	}
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, ErrBadBan.Error())
		return
	}
	if err := h.svc.Ban(r.Context(), adminID(r), id, b.Reason, b.Until); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unban handles DELETE /api/admin/users/{user_id}/ban.
func (h *Handler) Unban(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.svc.Unban(r.Context(), adminID(r), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Page serves the admin web page (/admin, /admin/app.js, /admin/style.css)
// with a policy that only allows its own script, style and API calls.
func Page() http.Handler {
	sub, _ := fs.Sub(web, "web")
	files := http.FileServer(http.FS(sub))
	return http.StripPrefix("/admin", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	}))
}
