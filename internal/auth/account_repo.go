package auth

import (
	"context"
	"errors"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type storedCode struct {
	Hash      string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type AccountRepo interface {
	GetCode(ctx context.Context, userID uuid.UUID, purpose string) (storedCode, error)
	PutCode(ctx context.Context, userID uuid.UUID, purpose, hash string, expires time.Time) error
	BumpAttempts(ctx context.Context, userID uuid.UUID, purpose string) (int32, error)
	DeleteCode(ctx context.Context, userID uuid.UUID, purpose string) error
	MarkVerified(ctx context.Context, userID uuid.UUID) error
	// SetPassword stores a hash; "" removes password sign-in.
	SetPassword(ctx context.Context, userID uuid.UUID, hash string) error
	Identity(ctx context.Context, provider, subject string) (uuid.UUID, error)
	LinkIdentity(ctx context.Context, provider, subject string, userID uuid.UUID, email string) error
	CreateGoogleUser(ctx context.Context, name, email, subject string) (*users.User, error)
	// DeleteUser removes the account and returns who and what it touched,
	// for cache invalidation.
	DeleteUser(ctx context.Context, userID uuid.UUID) (friends, groups []uuid.UUID, err error)
}

type accountRepo struct {
	pool *pgxpool.Pool
	q    *database.Queries
}

func NewAccountRepo(pool *pgxpool.Pool) AccountRepo {
	return &accountRepo{pool: pool, q: database.New(pool)}
}

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t.UTC(), Valid: true} }

func (r *accountRepo) GetCode(ctx context.Context, userID uuid.UUID, purpose string) (storedCode, error) {
	c, err := r.q.GetEmailCode(ctx, database.GetEmailCodeParams{UserID: userID, Purpose: purpose})
	if err != nil {
		return storedCode{}, err
	}
	return storedCode{Hash: c.CodeHash, ExpiresAt: c.ExpiresAt.Time, CreatedAt: c.CreatedAt.Time}, nil
}

func (r *accountRepo) PutCode(ctx context.Context, userID uuid.UUID, purpose, hash string, expires time.Time) error {
	return r.q.UpsertEmailCode(ctx, database.UpsertEmailCodeParams{UserID: userID, Purpose: purpose, CodeHash: hash, ExpiresAt: ts(expires)})
}

func (r *accountRepo) BumpAttempts(ctx context.Context, userID uuid.UUID, purpose string) (int32, error) {
	return r.q.BumpEmailCodeAttempts(ctx, database.BumpEmailCodeAttemptsParams{UserID: userID, Purpose: purpose})
}

func (r *accountRepo) DeleteCode(ctx context.Context, userID uuid.UUID, purpose string) error {
	return r.q.DeleteEmailCode(ctx, database.DeleteEmailCodeParams{UserID: userID, Purpose: purpose})
}

func (r *accountRepo) MarkVerified(ctx context.Context, userID uuid.UUID) error {
	return r.q.MarkEmailVerified(ctx, userID)
}

func (r *accountRepo) SetPassword(ctx context.Context, userID uuid.UUID, hash string) error {
	return r.q.SetPasswordHash(ctx, database.SetPasswordHashParams{ID: userID, Password: pgtype.Text{String: hash, Valid: hash != ""}})
}

func (r *accountRepo) Identity(ctx context.Context, provider, subject string) (uuid.UUID, error) {
	id, err := r.q.GetIdentity(ctx, database.GetIdentityParams{Provider: provider, Subject: subject})
	if err != nil {
		return uuid.Nil, err
	}
	return id.UserID, nil
}

func (r *accountRepo) LinkIdentity(ctx context.Context, provider, subject string, userID uuid.UUID, email string) error {
	return r.q.CreateIdentity(ctx, database.CreateIdentityParams{Provider: provider, Subject: subject, UserID: userID, Email: email})
}

func (r *accountRepo) CreateGoogleUser(ctx context.Context, name, email, subject string) (*users.User, error) {
	var out *users.User
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		u, err := q.CreateUserVerifiedNoPassword(ctx, database.CreateUserVerifiedNoPasswordParams{Name: name, Email: email})
		if err != nil {
			return err
		}
		if err := q.CreateIdentity(ctx, database.CreateIdentityParams{Provider: "google", Subject: subject, UserID: u.ID, Email: email}); err != nil {
			return err
		}
		out = &users.User{ID: u.ID, Name: u.Name, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, Email: u.Email, Verified: true}
		return nil
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "users_name_key":
			return nil, users.ErrDuplicateName
		case "user_identities_pkey":
			return nil, ErrInvalidGoogle // signup token replayed after the account exists
		}
		return nil, users.ErrDuplicateEmail
	}
	return out, err
}

func (r *accountRepo) DeleteUser(ctx context.Context, userID uuid.UUID) (friends, groups []uuid.UUID, err error) {
	err = pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if friends, err = q.FriendIDsOf(ctx, userID); err != nil {
			return err
		}
		if groups, err = q.GroupsOfUser(ctx, userID); err != nil {
			return err
		}
		led, err := q.GroupsLedBy(ctx, userID)
		if err != nil {
			return err
		}
		for _, g := range led {
			if _, err := q.LockGroup(ctx, g); err != nil { // serialise with joins/leaves
				return err
			}
			next, err := q.EarliestOtherMember(ctx, database.EarliestOtherMemberParams{GroupID: g, MemberID: userID})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				if err := q.DeleteGroup(ctx, g); err != nil { // nobody left
					return err
				}
			case err != nil:
				return err
			default:
				if err := q.PromoteToLeader(ctx, database.PromoteToLeaderParams{GroupID: g, MemberID: next}); err != nil {
					return err
				}
			}
		}
		// cascades: refresh tokens, relationships, memberships, identities,
		// codes; messages keep their content with from_id set to NULL
		if err := q.DeleteUserByID(ctx, userID); err != nil {
			return err
		}
		for _, g := range groups {
			if err := q.RecountGroupMembers(ctx, g); err != nil {
				return err
			}
		}
		return nil
	})
	return friends, groups, err
}
