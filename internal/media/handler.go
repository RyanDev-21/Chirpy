package media

import (
	"errors"
	"net/http"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrDisabled):
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrBadMedia):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrDailyQuota):
		httpx.Error(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, ErrStorageFull):
		httpx.Error(w, http.StatusInsufficientStorage, err.Error())
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrExpired):
		httpx.Error(w, http.StatusGone, err.Error())
	default:
		httpx.ServerError(w, r, err)
	}
}

// Create handles POST /api/media {"kind","content_type","size"}: returns a
// link to upload the file to.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var b struct {
		Kind        string `json:"kind"`
		ContentType string `json:"content_type"`
		Size        int64  `json:"size"`
	}
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, ErrBadMedia.Error())
		return
	}
	up, err := h.svc.NewUpload(r.Context(), me, b.Kind, b.ContentType, b.Size)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, up)
}

// Get handles GET /api/media/{media_id}: a short-lived download link.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := httpx.PathUUID(r, "media_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid media id")
		return
	}
	d, err := h.svc.Download(r.Context(), me, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}
