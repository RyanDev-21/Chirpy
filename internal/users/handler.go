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
	case errors.Is(err, ErrUserNotFound):
		httpx.Error(w, http.StatusNotFound, "no user found")
	default:
		httpx.ServerError(w, r, err)
	}
}

func (h *UserHandler) SearchUser(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.SearchUser(r.Context(), r.URL.Query().Get("q"))
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusOK, FoundUserListRes{UserList: list})
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, http.StatusBadRequest, "q must be 1-25 characters")
	default:
		httpx.ServerError(w, r, err)
	}
}

func (h *UserHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var p ConfigList
	if err := httpx.Decode(w, r, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	switch err := h.svc.SaveConfig(r.Context(), userID, p.List); {
	case err == nil:
		w.WriteHeader(http.StatusCreated)
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, http.StatusBadRequest, "too many config items")
	default:
		httpx.ServerError(w, r, err)
	}
}

func (h *UserHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	list, err := h.svc.GetConfig(r.Context(), userID)
	if err != nil {
		httpx.ServerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, ConfigList{List: list})
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
