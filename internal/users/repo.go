package users

import (
	"context"
	"errors"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type UserRepo interface {
	Create(ctx context.Context, input CreateUserInput) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, string, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, string, error)
	UpdateUserPassword(ctx context.Context, payload UpdateUserPassword) (*User, error)
	RevokeAllRefreshTokens(ctx context.Context, userID uuid.UUID) error
	// SearchByName leaves out users with a block either way with me.
	SearchByName(ctx context.Context, me uuid.UUID, q string) ([]PublicUser, error)
}

type userRepo struct {
	queries *database.Queries
}

func NewUserRepo(queries *database.Queries) UserRepo {
	return &userRepo{queries: queries}
}

func toUser(u database.User) *User {
	return &User{
		ID:        u.ID,
		Name:      u.Name,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
		Email:     u.Email,
		IsRED:     u.IsChirpyRed.Bool,
		Verified:  u.EmailVerifiedAt.Valid,
	}
}

func (r *userRepo) Create(ctx context.Context, input CreateUserInput) (*User, error) {
	u, err := r.queries.CreateUser(ctx, database.CreateUserParams{
		Name:     input.Name,
		Email:    input.Email,
		Password: pgtype.Text{String: input.Password, Valid: input.Password != ""},
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "users_name_key" {
				return nil, ErrDuplicateName
			}
			return nil, ErrDuplicateEmail
		}
		return nil, err
	}
	return toUser(u), nil
}

// GetUserByEmail returns the user and its password hash.
func (r *userRepo) GetUserByEmail(ctx context.Context, email string) (*User, string, error) {
	u, err := r.queries.GetUserInfoByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrUserNotFound
		}
		return nil, "", err
	}
	return toUser(u), u.Password.String, nil
}

// GetUserByID returns the user and its password hash.
func (r *userRepo) GetUserByID(ctx context.Context, id uuid.UUID) (*User, string, error) {
	u, err := r.queries.GetUserInfoByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrUserNotFound
		}
		return nil, "", err
	}
	return toUser(u), u.Password.String, nil
}

func (r *userRepo) UpdateUserPassword(ctx context.Context, p UpdateUserPassword) (*User, error) {
	if err := r.queries.UpdatePassword(ctx, database.UpdatePasswordParams{Password: pgtype.Text{String: p.Password, Valid: true}, ID: p.UserID}); err != nil {
		return nil, err
	}
	u, _, err := r.GetUserByID(ctx, p.UserID)
	return u, err
}

func (r *userRepo) RevokeAllRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	return r.queries.RevokeAllUserRefreshTokens(ctx, userID)
}

func (r *userRepo) SearchByName(ctx context.Context, me uuid.UUID, q string) ([]PublicUser, error) {
	rows, err := r.queries.SearchNameSiml(ctx, database.SearchNameSimlParams{Q: q, Me: me})
	if err != nil {
		return nil, err
	}
	out := make([]PublicUser, 0, len(rows))
	for _, v := range rows {
		out = append(out, PublicUser{ID: v.ID, Name: v.Name})
	}
	return out, nil
}
