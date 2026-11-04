package media

import (
	"errors"
	"io"
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
	case errors.Is(err, ErrBadMedia), errors.Is(err, ErrBadThumbnail):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrThumbnailSet):
		httpx.Error(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrDailyQuota):
		httpx.Error(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, ErrStorageFull):
		httpx.Error(w, http.StatusInsufficientStorage, err.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotVideo):
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
		Dims
	}
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, ErrBadMedia.Error())
		return
	}
	up, err := h.svc.NewUpload(r.Context(), me, b.Kind, b.ContentType, b.Size, b.Dims)
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

// PutThumbnail handles PUT /api/media/{media_id}/thumbnail: the body is a
// still frame (JPEG, PNG or WebP, at most 2 MB) of the caller's video.
func (h *Handler) PutThumbnail(w http.ResponseWriter, r *http.Request) {
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
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxThumbnailUpload))
	if err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, ErrBadThumbnail.Error())
		return
	}
	if err := h.svc.SetThumbnail(r.Context(), me, id, raw); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetThumbnail handles GET /api/media/{media_id}/thumbnail: a short-lived
// link to the still.
func (h *Handler) GetThumbnail(w http.ResponseWriter, r *http.Request) {
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
	d, err := h.svc.Thumbnail(r.Context(), me, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}
