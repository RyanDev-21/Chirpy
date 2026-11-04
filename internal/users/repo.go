package users

import (
	"context"
	"errors"
	"time"

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
	// Rename changes the username unless it was changed after cutoff
	// (RenameTooSoonError); ErrDuplicateName if taken.
	Rename(ctx context.Context, id uuid.UUID, name string, now, cutoff time.Time) (*User, error)
	SetBio(ctx context.Context, id uuid.UUID, bio string) (*User, error)
	// PublicProfile is ErrUserNotFound if either user blocked the other.
	PublicProfile(ctx context.Context, id, viewer uuid.UUID) (Profile, error)
	SetAvatar(ctx context.Context, id uuid.UUID, image []byte, at time.Time) error
	DeleteAvatar(ctx context.Context, id uuid.UUID) error
	// Avatar is ErrUserNotFound without a picture or across a block.
	Avatar(ctx context.Context, id, viewer uuid.UUID) ([]byte, time.Time, error)
	// SetLocation stores a catalog place ("" clears it).
	SetLocation(ctx context.Context, id uuid.UUID, place string, country string) error
	SetDiscoverable(ctx context.Context, id uuid.UUID, on bool) error
	SetInterests(ctx context.Context, id uuid.UUID, ids []string) error
	Interests(ctx context.Context, id uuid.UUID) ([]string, error)
}

type userRepo struct {
	queries *database.Queries
}

func NewUserRepo(queries *database.Queries) UserRepo {
	return &userRepo{queries: queries}
}

func toUser(u database.User) *User {
	out := &User{
		ID:        u.ID,
		Name:      u.Name,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
		Email:     u.Email,
		IsRED:     u.IsChirpyRed.Bool,
		Verified:  u.EmailVerifiedAt.Valid,
		Bio:       u.Bio,
	}
	if u.AvatarAt.Valid {
		out.AvatarURL = avatarURL(u.ID, u.AvatarAt.Time)
	}
	out.Location = PlaceOf(u.LocationID.String)
	out.Discoverable = u.Discoverable
	if u.NameChangedAt.Valid {
		if next := u.NameChangedAt.Time.UTC().Add(RenameEvery); next.After(time.Now()) {
			out.NameChangeAvailableAt = &next
		}
	}
	return out
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

func (r *userRepo) Rename(ctx context.Context, id uuid.UUID, name string, now, cutoff time.Time) (*User, error) {
	u, err := r.queries.RenameUser(ctx, database.RenameUserParams{
		ID: id, Name: name, Now: pgtype.Timestamp{Time: now, Valid: true}, Cutoff: pgtype.Timestamp{Time: cutoff, Valid: true},
	})
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return nil, ErrDuplicateName
	case errors.Is(err, pgx.ErrNoRows):
		cur, _, gerr := r.GetUserByID(ctx, id)
		if gerr != nil {
			return nil, gerr
		}
		until := now.Add(RenameEvery)
		if cur.NameChangeAvailableAt != nil {
			until = *cur.NameChangeAvailableAt
		}
		return nil, &RenameTooSoonError{Until: until}
	case err != nil:
		return nil, err
	}
	return toUser(u), nil
}

func (r *userRepo) SetBio(ctx context.Context, id uuid.UUID, bio string) (*User, error) {
	u, err := r.queries.SetBio(ctx, database.SetBioParams{ID: id, Bio: bio})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return toUser(u), nil
}

func (r *userRepo) PublicProfile(ctx context.Context, id, viewer uuid.UUID) (Profile, error) {
	p, err := r.queries.GetPublicProfile(ctx, database.GetPublicProfileParams{ID: id, Viewer: viewer})
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrUserNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	out := Profile{ID: p.ID, Name: p.Name, Bio: p.Bio, Interests: []string{}}
	if p.AvatarAt.Valid {
		out.AvatarURL = avatarURL(p.ID, p.AvatarAt.Time)
	}
	visible := p.Discoverable || id == viewer
	if !visible {
		if visible, err = r.queries.AreFriendsSQL(ctx, database.AreFriendsSQLParams{A: id, B: viewer}); err != nil {
			return Profile{}, err
		}
	}
	if visible {
		out.Location = PlaceOf(p.LocationID.String)
		if out.Interests, err = r.Interests(ctx, id); err != nil {
			return Profile{}, err
		}
	}
	return out, nil
}

func (r *userRepo) SetAvatar(ctx context.Context, id uuid.UUID, image []byte, at time.Time) error {
	return r.queries.SetAvatar(ctx, database.SetAvatarParams{UserID: id, Image: image, At: pgtype.Timestamp{Time: at, Valid: true}})
}

func (r *userRepo) DeleteAvatar(ctx context.Context, id uuid.UUID) error {
	return r.queries.DeleteAvatar(ctx, id)
}

func (r *userRepo) Avatar(ctx context.Context, id, viewer uuid.UUID) ([]byte, time.Time, error) {
	a, err := r.queries.GetAvatar(ctx, database.GetAvatarParams{ID: id, Viewer: viewer})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, ErrUserNotFound
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	return a.Image, a.UpdatedAt.Time.UTC(), nil
}

func (r *userRepo) SetLocation(ctx context.Context, id uuid.UUID, place, country string) error {
	return r.queries.SetLocation(ctx, database.SetLocationParams{ID: id, LocationID: pgtype.Text{String: place, Valid: place != ""}, Country: country})
}

func (r *userRepo) SetDiscoverable(ctx context.Context, id uuid.UUID, on bool) error {
	return r.queries.SetDiscoverable(ctx, database.SetDiscoverableParams{ID: id, Discoverable: on})
}

// SetInterests replaces the list: two statements, each idempotent, so a
// retry or a concurrent update still ends with one of the requested lists.
func (r *userRepo) SetInterests(ctx context.Context, id uuid.UUID, ids []string) error {
	if err := r.queries.RemoveOtherInterests(ctx, database.RemoveOtherInterestsParams{UserID: id, Keep: ids}); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return r.queries.AddInterests(ctx, database.AddInterestsParams{UserID: id, Ids: ids})
}

func (r *userRepo) Interests(ctx context.Context, id uuid.UUID) ([]string, error) {
	ids, err := r.queries.GetInterests(ctx, id)
	if ids == nil {
		ids = []string{}
	}
	return ids, err
}
