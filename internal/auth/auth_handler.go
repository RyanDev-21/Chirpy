package auth

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
)

type AuthHandler struct {
	authService AuthService
}

func NewAuthHandler(authService AuthService) *AuthHandler {
	return &AuthHandler{
		authService: authService,
	}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(r.Body)
	params := &parameters{}
	err := decoder.Decode(params)
	if err != nil {
		httpx.Error(w, 400, "invalid params")
		return
	}
	accessToken, refreshToken, user, err := h.authService.Login(r.Context(), params.Email, params.Password)
	if err != nil {
		if err == ErrInvalidCredentials {
			httpx.Error(w, 401, ErrInvalidCredentials.Error())
			return
		}
		log.Printf("failed to get the tokens :%s", err)
		httpx.Error(w, 500, "somthing went wrong")
		return
	}

	httpx.JSON(w, 200, responseType{
		ID:           user.ID,
		Email:        user.Email,
		Name:         user.Name,
		CreatedAt:    user.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    user.UpdatedAt.Format(time.RFC3339),
		IsChirpyRed:  user.IsRED,
		Token:        accessToken,
		RefreshToken: refreshToken,
	})
}

func (h *AuthHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	token, err := secure.GetBearerToken(r.Header)
	if err != nil {
		httpx.Error(w, 401, "unauthorized")
		return
	}
	err = h.authService.Revoke(r.Context(), token)
	if err != nil {
		if err == ErrNotAuthorized {
			httpx.Error(w, 401, "unauthorized")
			return
		}
		httpx.Error(w, 500, "internal server error")
		return
	}
	w.WriteHeader(204)
}

func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	token, err := secure.GetBearerToken(r.Header)
	if err != nil {

		httpx.Error(w, 401, "unauthorized")
		return
	}
	accessToken, refreshToken, err := h.authService.Refresh(r.Context(), token)
	if err != nil {
		if err == ErrNotAuthorized {
			httpx.Error(w, 401, "unauthorized")
			return
		}
		log.Printf("failed to get accessToken #%s#", err)
		httpx.Error(w, 500, "internal server error")
		return
	}
	httpx.JSON(w, 200, refreshResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}
