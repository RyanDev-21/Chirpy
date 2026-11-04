package users

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
)

type UserHandler struct {
	svc UserService
}

func NewUserHandler(svc UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

func (h *UserHandler) UpdatePassword(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var p PasswordUpdateStruct
	if err := httpx.Decode(w, r, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid params")
		return
	}
	user, err := h.svc.UpdatePassword(r.Context(), userID, p.OldPass, p.NewPass)
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, user)
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, http.StatusBadRequest, "password must be 8-128 characters with at least one of @$!%*?&")
	case errors.Is(err, secure.ErrPassNotMatch):
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, ErrNoPassword):
		httpx.Error(w, http.StatusConflict, "this account signs in with Google and has no password; set one with password reset")
	case errors.Is(err, ErrUserNotFound):
		httpx.Error(w, http.StatusNotFound, "no user found")
	default:
		httpx.ServerError(w, r, err)
	}
}

func (h *UserHandler) SearchUser(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	list, err := h.svc.SearchUser(r.Context(), me, r.URL.Query().Get("q"))
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, FoundUserListRes{UserList: list})
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, http.StatusBadRequest, "q must be 1-25 characters")
	default:
		httpx.ServerError(w, r, err)
	}
}

// Me handles GET /api/users/me: the signed-in user's own profile.
func (h *UserHandler) Me(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	u, err := h.svc.Me(r.Context(), userID)
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, u)
	case errors.Is(err, ErrUserNotFound):
		// the account was deleted while the token was still valid
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
	default:
		httpx.ServerError(w, r, err)
	}
}

// UpdateMe handles PATCH /api/users/me {"name"?, "bio"?}.
func (h *UserHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req UpdateProfileRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	u, err := h.svc.UpdateProfile(r.Context(), me, req)
	var soon *RenameTooSoonError
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, u)
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, http.StatusBadRequest, "send name and/or bio; name is 3-25 characters without spaces")
	case errors.Is(err, ErrBioTooLong):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrDuplicateName):
		httpx.Error(w, http.StatusConflict, "username taken")
	case errors.As(err, &soon):
		httpx.Error(w, http.StatusForbidden, soon.Error())
	case errors.Is(err, ErrUserNotFound):
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
	default:
		httpx.ServerError(w, r, err)
	}
}

// Profile handles GET /api/users/{user_id}: another user's public profile.
func (h *UserHandler) Profile(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	p, err := h.svc.Profile(r.Context(), me, id)
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, p)
	case errors.Is(err, ErrUserNotFound):
		httpx.Error(w, http.StatusNotFound, "no user found")
	default:
		httpx.ServerError(w, r, err)
	}
}

// UploadAvatar handles PUT /api/users/me/avatar; the body is the image
// itself (JPEG, PNG or WebP, at most 5 MB).
func (h *UserHandler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxAvatarUpload))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		httpx.Error(w, http.StatusRequestEntityTooLarge, ErrImageTooLarge.Error())
		return
	case err != nil:
		httpx.Error(w, http.StatusBadRequest, "could not read the upload")
		return
	}
	u, err := h.svc.SetAvatar(r.Context(), me, raw)
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, u)
	case errors.Is(err, ErrBadImage):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrImageTooLarge):
		httpx.Error(w, http.StatusRequestEntityTooLarge, err.Error())
	default:
		httpx.ServerError(w, r, err)
	}
}

// DeleteAvatar handles DELETE /api/users/me/avatar.
func (h *UserHandler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.svc.DeleteAvatar(r.Context(), me); err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Avatar handles GET /api/users/{user_id}/avatar: the JPEG itself.
func (h *UserHandler) Avatar(w http.ResponseWriter, r *http.Request) {
	me, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id, err := httpx.PathUUID(r, "user_id")
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid user id")
		return
	}
	img, at, err := h.svc.Avatar(r.Context(), me, id)
	if errors.Is(err, ErrUserNotFound) {
		httpx.Error(w, http.StatusNotFound, "no avatar")
		return
	}
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	etag := `"` + strconv.FormatInt(at.UnixMilli(), 10) + `"`
	w.Header().Set("ETag", etag)
	// the URL carries the version, so a cached copy never goes stale; it is
	// private because it is only served to signed-in users
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(img)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(img)
}
