package users

import (
	"errors"
	"net/http"

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
