package auth

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"RyanDev-21.com/Chirpy/pkg/auth"
	"RyanDev-21.com/Chirpy/pkg/response"
)

type authHandler struct {
	authService AuthService
}

func NewAuthHandler(authService AuthService) *authHandler {
	return &authHandler{
		authService: authService,
	}
}

func (h *authHandler) Login(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(r.Body)
	params := &parameters{}
	err := decoder.Decode(params)
	if err != nil {
		response.Error(w, 400, "invalid params")
		return
	}
	accessToken, refreshToken, user, err := h.authService.Login(r.Context(), params.Email, params.Password)
	if err != nil {
		if err == InvalidCredentailErr {
			response.Error(w, 401, InvalidCredentailErr.Error())
			return
		}
		if err == NoUserFoundErr {
			response.Error(w, 404, "no user found")
			return
		}
		log.Printf("failed to get the tokens :%s", err)
		response.Error(w, 500, "somthing went wrong")
		return
	}

	response.JSON(w, 200, responseType{
		ID:           user.ID,
		Email:        user.Email,
		CreatedAt:    user.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    user.UpdatedAt.Format(time.RFC3339),
		IsChirpyRed:  user.IsRED,
		Token:        accessToken,
		RefreshToken: refreshToken,
	})

}
func (h *authHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		response.Error(w, 401, "unauthorized")
		return
	}
	err = h.authService.Revoke(r.Context(), token)
	if err != nil {
		if err == ErrNotAuthorized {
			response.Error(w, 401, "unauthorized")
			return
		}
		response.Error(w, 500, "internal server error")
		return
	}
	w.WriteHeader(204)
}
func (h *authHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		response.Error(w, 401, "unauthorized")
		return
	}
	accessToken, refreshToken, err := h.authService.Refresh(r.Context(), token)
	if err != nil {
		if err == ErrNotAuthorized {
			response.Error(w, 401, "unauthorized")
			return
		}
		log.Printf("failed to get accessToken #%s#", err)
		response.Error(w, 500, "internal server error")
		return
	}
	response.JSON(w, 200, refreshResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	})
}
