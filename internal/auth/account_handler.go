package auth

import (
	"RyanDev-21.com/Chirpy/internal/platform/telemetry"
	"context"
	"errors"
	"net/http"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/httpx"
	"RyanDev-21.com/Chirpy/internal/platform/reqctx"
	"RyanDev-21.com/Chirpy/internal/users"
)

type AccountHandler struct {
	svc AccountService
}

func NewAccountHandler(svc AccountService) *AccountHandler { return &AccountHandler{svc: svc} }

func writeSession(w http.ResponseWriter, s *Session) {
	httpx.JSON(w, http.StatusOK, responseType{
		ID:           s.User.ID,
		Email:        s.User.Email,
		Name:         s.User.Name,
		CreatedAt:    s.User.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    s.User.UpdatedAt.Format(time.RFC3339),
		IsChirpyRed:  s.User.IsRED,
		Token:        s.AccessToken,
		RefreshToken: s.RefreshToken,
	})
}

func (h *AccountHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, users.ErrInvalidInput):
		httpx.Error(w, http.StatusBadRequest, "username must be 3-25 characters: a-z, 0-9, _ and . (email must be valid, and password 8-128 characters with one of @$!%*?&)")
	case errors.Is(err, users.ErrDuplicateName):
		httpx.Error(w, http.StatusConflict, "the user name already exists")
	case errors.Is(err, ErrInvalidCode):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrSuspended):
		httpx.Error(w, http.StatusForbidden, "account suspended")
	case errors.Is(err, ErrInvalidGoogle), errors.Is(err, ErrReauthRequired):
		httpx.Error(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, ErrMailUnavailable), errors.Is(err, ErrGoogleUnavailable):
		httpx.Error(w, http.StatusServiceUnavailable, err.Error())
	default:
		httpx.ServerError(w, r, err)
	}
}

var accepted = map[string]string{"status": "check_your_email"}

// Register handles POST /api/users. The answer is the same whether or not
// the email already has an account.
func (h *AccountHandler) Register(w http.ResponseWriter, r *http.Request) {
	var p users.DefaultUsersParameters
	if err := httpx.Decode(w, r, &p); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid params")
		return
	}
	if err := h.svc.Register(r.Context(), p.Name, p.Email, p.Password); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, accepted)
}

type emailBody struct {
	Email       string `json:"email"`
	Code        string `json:"code"`
	NewPassword string `json:"new_password"`
}

// VerifyEmail handles POST /api/auth/verify-email {"email","code"} and signs in.
func (h *AccountHandler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var b emailBody
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid params")
		return
	}
	s, err := h.svc.VerifyEmail(r.Context(), b.Email, b.Code)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	telemetry.Auth(r.Context(), "signup", "ok")
	writeSession(w, s)
}

// ResendVerification handles POST /api/auth/resend-verification {"email"}.
func (h *AccountHandler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	h.emailOnly(w, r, h.svc.ResendVerification)
}

// RequestPasswordReset handles POST /api/auth/password-reset {"email"}.
func (h *AccountHandler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	h.emailOnly(w, r, h.svc.RequestPasswordReset)
}

func (h *AccountHandler) emailOnly(w http.ResponseWriter, r *http.Request, fn func(context.Context, string) error) {
	var b emailBody
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid params")
		return
	}
	if err := fn(r.Context(), b.Email); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, accepted)
}

// ResetPassword handles POST /api/auth/password-reset/confirm
// {"email","code","new_password"}.
func (h *AccountHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var b emailBody
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid params")
		return
	}
	if err := h.svc.ResetPassword(r.Context(), b.Email, b.Code, b.NewPassword); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Google handles POST /api/auth/google {"id_token"}.
func (h *AccountHandler) Google(w http.ResponseWriter, r *http.Request) {
	var b struct {
		IDToken string `json:"id_token"`
	}
	if err := httpx.Decode(w, r, &b); err != nil || b.IDToken == "" {
		httpx.Error(w, http.StatusBadRequest, "id_token is required")
		return
	}
	res, err := h.svc.Google(r.Context(), b.IDToken)
	if err != nil {
		telemetry.Auth(r.Context(), "google", "failed")
		h.writeErr(w, r, err)
		return
	}
	telemetry.Auth(r.Context(), "google", "ok")
	if res.Session == nil {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "username_required", "signup_token": res.SignupToken})
		return
	}
	writeSession(w, res.Session)
}

// CompleteGoogle handles POST /api/auth/google/complete {"signup_token","name"}.
func (h *AccountHandler) CompleteGoogle(w http.ResponseWriter, r *http.Request) {
	var b struct {
		SignupToken string `json:"signup_token"`
		Name        string `json:"name"`
	}
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid params")
		return
	}
	s, err := h.svc.CompleteGoogleSignup(r.Context(), b.SignupToken, b.Name)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	telemetry.Auth(r.Context(), "signup", "google")
	writeSession(w, s)
}

// DeleteAccount handles DELETE /api/users/me {"password"} or
// {"google_id_token"} for accounts without a password.
func (h *AccountHandler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	userID, err := reqctx.User(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var b struct {
		Password      string `json:"password"`
		GoogleIDToken string `json:"google_id_token"`
	}
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid params")
		return
	}
	if err := h.svc.DeleteAccount(r.Context(), userID, b.Password, b.GoogleIDToken); err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminLogin handles POST /api/admin/login {"email","password"}: emails an
// admin sign-in code. Wrong passwords and non-admins get the same 401.
func (h *AccountHandler) AdminLogin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	switch err := h.svc.AdminStart(r.Context(), b.Email, b.Password); {
	case err == nil:
		httpx.JSON(w, http.StatusAccepted, accepted)
	case errors.Is(err, ErrInvalidCredentials):
		httpx.Error(w, http.StatusUnauthorized, "invalid credentials")
	default:
		h.writeErr(w, r, err)
	}
}

// AdminVerify handles POST /api/admin/verify {"email","code"}: returns an
// admin-page token valid for 30 minutes.
func (h *AccountHandler) AdminVerify(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := httpx.Decode(w, r, &b); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	tok, err := h.svc.AdminVerify(r.Context(), b.Email, b.Code)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"token": tok, "expires_in": int(AdminSessionTTL.Seconds())})
}
