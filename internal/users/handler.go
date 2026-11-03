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

func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	var p DefaultUsersParameters
	if err := httpx.Decode(w, r, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid params")
		return
	}
	user, err := h.svc.Register(r.Context(), p.Name, p.Email, p.Password)
	switch {
	case err == nil:
		httpx.JSON(w, http.StatusCreated, user)
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, http.StatusBadRequest, "name must be 3-25 characters without spaces, email must be valid, and password 8-128 characters with one of @$!%*?&")
	case errors.Is(err, DuplicateKeyErr):
		httpx.Error(w, http.StatusConflict, "the user already exists")
	case errors.Is(err, DuplicateNameKeyErr):
		httpx.Error(w, http.StatusConflict, "the user name already exists")
	default:
		httpx.Error(w, http.StatusInternalServerError, "something went wrong")
	}
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
	case errors.Is(err, NoUserFoundErr):
		httpx.Error(w, http.StatusNotFound, "no user found")
	default:
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
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
		httpx.Error(w, http.StatusInternalServerError, "internal server error")
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
		httpx.Error(w, http.StatusInternalServerError, "something went wrong")
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
		httpx.Error(w, http.StatusInternalServerError, "something went wrong")
		return
	}
	httpx.JSON(w, http.StatusOK, ConfigList{List: list})
}
