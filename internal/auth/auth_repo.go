package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"time"

	"github.com/jackc/pgx/v5"

	"RyanDev-21.com/Chirpy/internal/database"
)

var ErrNotAuthorized = errors.New("not found matching token")
var ErrTokenExpired = errors.New("token expired")

type authRepo struct {
	queries *database.Queries
}

type AuthRepo interface {
	RevokeToken(ctx context.Context, token string) error
	GetRefreshToken(ctx context.Context, token string) (*RefreshToken, error)
	CreateRefreshToken(ctx context.Context, payload PayloadForRefresh) error
	// Suspension reports an active ban (zero until: permanent).
	Suspension(ctx context.Context, userID uuid.UUID) (banned bool, until time.Time, err error)
	TouchLastSeen(ctx context.Context, userID uuid.UUID, now time.Time) error
}

// ErrSuspended: the account is banned; no new sessions.
var ErrSuspended = errors.New("account suspended")

// hashToken is what the database stores instead of the refresh token, so a
// leaked table cannot be used to sign in. Refresh tokens are 32 random
// bytes, so a plain (unsalted, fast) SHA-256 is enough: there is nothing to
// brute-force, unlike passwords.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func NewAuthRepo(queries *database.Queries) AuthRepo {
	return &authRepo{
		queries: queries,
	}
}

func convertToDomainModel(payload database.RefreshToken) *RefreshToken {
	return &RefreshToken{
		UserID:    payload.UserID,
		ExpiresAt: payload.ExpireAt,
		UpdatedAt: payload.UpdatedAt,
	}
}

func (r *authRepo) RevokeToken(ctx context.Context, token string) error {
	return r.queries.RevokeRefreshToken(ctx, hashToken(token))
}

func (r *authRepo) GetRefreshToken(ctx context.Context, token string) (*RefreshToken, error) {
	response, err := r.queries.GetRefreshToken(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotAuthorized
		}
		return nil, err
	}
	return convertToDomainModel(response), nil
}

func (r *authRepo) CreateRefreshToken(ctx context.Context, payload PayloadForRefresh) error {
	_, err := r.queries.CreateARefreshToken(ctx, database.CreateARefreshTokenParams{
		Token:    hashToken(payload.token),
		UserID:   payload.userID,
		ExpireAt: payload.expiresAt,
	})
	return err
}

func (r *authRepo) Suspension(ctx context.Context, userID uuid.UUID) (bool, time.Time, error) {
	b, err := r.queries.UserBan(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, ErrNotAuthorized
	}
	if err != nil || !b.BannedAt.Valid {
		return false, time.Time{}, err
	}
	return true, b.BannedUntil.Time, nil
}

func (r *authRepo) TouchLastSeen(ctx context.Context, userID uuid.UUID, now time.Time) error {
	return r.queries.TouchLastSeen(ctx, database.TouchLastSeenParams{ID: userID, Now: pgtype.Timestamp{Time: now, Valid: true}})
}
