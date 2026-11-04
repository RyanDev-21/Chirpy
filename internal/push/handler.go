package push

import (
	"errors"
	"net/http"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"github.com/google/uuid"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) user(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return id, false
	}
	return id, true
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrBadDevice) || errors.Is(err, ErrBadMute) {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.ServerError(w, r, err)
}

// The device token travels in the body, never the URL, so it doesn't end
// up in access logs.
type deviceBody struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

// RegisterDevice handles POST /api/devices {"token","platform"?}.
func (h *Handler) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	me, ok := h.user(w, r)
	if !ok {
		return
	}
	var b deviceBody
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	if err := h.svc.RegisterDevice(r.Context(), me, b.Token, b.Platform); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RemoveDevice handles DELETE /api/devices {"token"} (on logout).
func (h *Handler) RemoveDevice(w http.ResponseWriter, r *http.Request) {
	me, ok := h.user(w, r)
	if !ok {
		return
	}
	var b deviceBody
	if err := httpx.Decode(w, r, &b); err != nil || b.Token == "" {
		httpx.Error(w, http.StatusBadRequest, "token is required")
		return
	}
	if err := h.svc.RemoveDevice(r.Context(), me, b.Token); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetPrefs handles GET /api/users/me/notifications.
func (h *Handler) GetPrefs(w http.ResponseWriter, r *http.Request) {
	me, ok := h.user(w, r)
	if !ok {
		return
	}
	p, err := h.svc.Prefs(r.Context(), me)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// SetPrefs handles PUT /api/users/me/notifications with all four switches.
func (h *Handler) SetPrefs(w http.ResponseWriter, r *http.Request) {
	me, ok := h.user(w, r)
	if !ok {
		return
	}
	var p Prefs
	if err := httpx.Decode(w, r, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	if err := h.svc.SetPrefs(r.Context(), me, p); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

// ListMutes handles GET /api/mutes.
func (h *Handler) ListMutes(w http.ResponseWriter, r *http.Request) {
	me, ok := h.user(w, r)
	if !ok {
		return
	}
	list, err := h.svc.Mutes(r.Context(), me)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string][]Mute{"mutes": list})
}

// Mute handles PUT /api/mutes {"type","id","until"?}.
func (h *Handler) Mute(w http.ResponseWriter, r *http.Request) {
	me, ok := h.user(w, r)
	if !ok {
		return
	}
	var m Mute
	if err := httpx.Decode(w, r, &m); err != nil {
		httpx.Error(w, http.StatusBadRequest, ErrBadMute.Error())
		return
	}
	if err := h.svc.Mute(r.Context(), me, m); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Unmute handles DELETE /api/mutes {"type","id"}.
func (h *Handler) Unmute(w http.ResponseWriter, r *http.Request) {
	me, ok := h.user(w, r)
	if !ok {
		return
	}
	var m Mute
	if err := httpx.Decode(w, r, &m); err != nil || m.ID == uuid.Nil {
		httpx.Error(w, http.StatusBadRequest, ErrBadMute.Error())
		return
	}
	if err := h.svc.Unmute(r.Context(), me, m.Type, m.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
