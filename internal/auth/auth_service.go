package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type AuthService interface {
	Login(ctx context.Context, email, password string) (accessToken, refreshToken string, user *users.User, err error)
	Revoke(ctx context.Context, token string) error
	Refresh(ctx context.Context, token string) (string, string, error)
}

type authService struct {
	authRepo AuthRepo
	userRepo users.UserRepo
	secret   string
	logger   *slog.Logger
}

func NewAuthService(userRepo users.UserRepo, authRepo AuthRepo, secret string, logger *slog.Logger) AuthService {
	return &authService{
		authRepo: authRepo,
		userRepo: userRepo,
		secret:   secret,
		logger:   logger,
	}
}

// Login checks the credentials. Unexpected errors are returned wrapped and
// logged once by the HTTP layer; failed attempts are logged here as
// security events (without the email, which is personal data).
func (s *authService) Login(ctx context.Context, email, password string) (accessToken, refreshToken string, user *users.User, err error) {
	dbUser, hashPassword, err := s.userRepo.GetUserByEmail(ctx, email)
	if errors.Is(err, users.ErrUserNotFound) {
		// same response and timing as a wrong password
		secure.BurnPasswordCheck(password)
		s.logger.WarnContext(ctx, "login failed", "reason", "unknown_email")
		return "", "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", "", nil, fmt.Errorf("load user: %w", err)
	}
	if hashPassword == "" {
		// Google-only account: no password sign-in until one is set via reset
		secure.BurnPasswordCheck(password)
		s.logger.WarnContext(ctx, "login failed", "reason", "no_password", "user_id", dbUser.ID)
		return "", "", nil, ErrInvalidCredentials
	}
	valid, err := secure.CheckPassword(password, hashPassword)
	if err != nil {
		return "", "", nil, fmt.Errorf("check password: %w", err)
	}
	if !valid {
		s.logger.WarnContext(ctx, "login failed", "reason", "wrong_password", "user_id", dbUser.ID)
		return "", "", nil, ErrInvalidCredentials
	}
	// only revealed to someone who knows the password
	if !dbUser.Verified {
		s.logger.InfoContext(ctx, "login refused", "reason", "email_not_verified", "user_id", dbUser.ID)
		return "", "", nil, ErrEmailNotVerified
	}
	accessToken, refreshToken, err = s.issue(ctx, dbUser.ID)
	if err != nil {
		return "", "", nil, err
	}
	s.logger.InfoContext(ctx, "login succeeded", "user_id", dbUser.ID)
	return accessToken, refreshToken, dbUser, nil
}

func (s *authService) Revoke(ctx context.Context, token string) error {
	return s.authRepo.RevokeToken(ctx, token)
}

// Refresh rotates a refresh token: the old one is revoked and a new pair
// is issued. Revoked, unknown and expired tokens return ErrNotAuthorized.
func (s *authService) Refresh(ctx context.Context, token string) (string, string, error) {
	rt, err := s.authRepo.GetRefreshToken(ctx, token)
	if errors.Is(err, ErrNotAuthorized) {
		s.logger.WarnContext(ctx, "refresh rejected", "reason", "unknown_or_revoked")
		return "", "", err
	}
	if err != nil {
		return "", "", fmt.Errorf("load refresh token: %w", err)
	}
	if time.Now().After(rt.ExpiresAt) {
		s.logger.InfoContext(ctx, "refresh rejected", "reason", "expired", "user_id", rt.UserID)
		return "", "", ErrNotAuthorized
	}
	if err := s.authRepo.RevokeToken(ctx, token); err != nil {
		return "", "", fmt.Errorf("revoke old refresh token: %w", err)
	}
	return s.issue(ctx, rt.UserID)
}

const (
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
)

func (s *authService) issue(ctx context.Context, userID uuid.UUID) (string, string, error) {
	accessToken, err := secure.MakeJWT(userID, s.secret, accessTokenTTL)
	if err != nil {
		return "", "", fmt.Errorf("make access token: %w", err)
	}
	refreshToken, err := secure.MakeRefreshToken()
	if err != nil {
		return "", "", fmt.Errorf("make refresh token: %w", err)
	}
	if err := s.authRepo.CreateRefreshToken(ctx, PayloadForRefresh{
		token:     refreshToken,
		userID:    userID,
		expiresAt: time.Now().Add(refreshTokenTTL),
	}); err != nil {
		return "", "", fmt.Errorf("store refresh token: %w", err)
	}
	return accessToken, refreshToken, nil
}
