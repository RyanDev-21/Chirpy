package users

import (
	"RyanDev-21.com/Chirpy/internal/catalog"
	"context"
	"errors"
	"log/slog"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"github.com/google/uuid"
)

const (
	minNameLen     = 3
	maxNameLen     = 25 // users.name VARCHAR(25)
	maxEmailLen    = 125
	minPasswordLen = 8
	maxPasswordLen = 128
)

const passwordSpecials = "@$!%*?&"

type UserService interface {
	Register(ctx context.Context, name, email, password string) (*User, error)
	UpdatePassword(ctx context.Context, userID uuid.UUID, oldPass, newPass string) (*User, error)
	SearchUser(ctx context.Context, me uuid.UUID, q string) ([]PublicUser, error)
	// Me returns the caller's own profile.
	Me(ctx context.Context, userID uuid.UUID) (*User, error)
	// GetName returns a user's display name (cached).
	GetName(ctx context.Context, userID uuid.UUID) (string, error)
	// UpdateProfile changes the caller's username (at most once per
	// RenameEvery) and/or bio.
	UpdateProfile(ctx context.Context, me uuid.UUID, req UpdateProfileRequest) (*User, error)
	// Profile is another user's public profile (hidden when blocked).
	Profile(ctx context.Context, viewer, id uuid.UUID) (Profile, error)
	// OnRename registers cleanup after a username or avatar change (friend
	// lists cache names and avatar links).
	OnRename(fn func(ctx context.Context, userID uuid.UUID))
	// LogoutAll ends every session of the user (tokens, sockets, phones).
	LogoutAll(ctx context.Context, me uuid.UUID) error
	// DisplayName is what others see (the username if none was set).
	DisplayName(ctx context.Context, id uuid.UUID) (string, error)
	// SetAvatar stores raw (a JPEG, PNG or WebP upload) as a 256x256 JPEG.
	SetAvatar(ctx context.Context, me uuid.UUID, raw []byte) (*User, error)
	DeleteAvatar(ctx context.Context, me uuid.UUID) error
	Avatar(ctx context.Context, viewer, id uuid.UUID) ([]byte, time.Time, error)
}

type userService struct {
	repo     UserRepo
	store    Store
	onRename []func(ctx context.Context, userID uuid.UUID)
	logger   *slog.Logger
}

func NewUserService(repo UserRepo, store Store, logger *slog.Logger) UserService {
	return &userService{repo: repo, store: store, logger: logger}
}

// ValidatePassword enforces the password policy.
func ValidatePassword(p string) error {
	if len(p) < minPasswordLen || len(p) > maxPasswordLen || !strings.ContainsAny(p, passwordSpecials) {
		return ErrInvalidInput
	}
	return nil
}

// ValidateName enforces the username rules (3-25 characters, no spaces).
// usernamePattern: lowercase letters, digits, "_" and ".", 3-25 long
// (users.name CHECK). Only ASCII, so no look-alike letters from other
// alphabets can imitate someone.
var usernamePattern = regexp.MustCompile(`^[a-z0-9_.]{3,25}$`)

// NormalizeUsername is how a typed username is stored and compared.
func NormalizeUsername(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// ValidateName checks a username (after NormalizeUsername).
func ValidateName(name string) error {
	if !usernamePattern.MatchString(name) {
		return ErrInvalidInput
	}
	return nil
}

const maxDisplayNameLen = 50 // users.display_name VARCHAR(50)

// ErrBadDisplayName: empty, too long, or with invisible characters.
var ErrBadDisplayName = errors.New("display name must be 1-50 characters without line breaks or invisible characters")

// ValidateDisplayName allows spaces and emoji, but not control or invisible
// formatting characters (zero-width space, direction overrides, ...).
func ValidateDisplayName(name string) error {
	if n := utf8.RuneCountInString(name); n == 0 || n > maxDisplayNameLen {
		return ErrBadDisplayName
	}
	for _, r := range name {
		if (unicode.IsSpace(r) && r != ' ') || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) ||
			unicode.In(r, unicode.Zl, unicode.Zp) || r == utf8.RuneError {
			return ErrBadDisplayName
		}
	}
	return nil
}

func validateRegister(name, email, password string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if len(email) > maxEmailLen {
		return ErrInvalidInput
	}
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		return ErrInvalidInput
	}
	return ValidatePassword(password)
}

func (s *userService) Register(ctx context.Context, name, email, password string) (*User, error) {
	name = NormalizeUsername(name)
	email = strings.ToLower(strings.TrimSpace(email))
	if err := validateRegister(name, email, password); err != nil {
		return nil, err
	}
	hash, err := secure.HashPassword(password)
	if err != nil {
		return nil, err
	}
	user, err := s.repo.Create(ctx, CreateUserInput{Name: name, DisplayName: name, Email: email, Password: hash})
	if err != nil {
		return nil, err
	}
	if err := s.store.SetNames(ctx, PublicUser{ID: user.ID, Name: user.Name}); err != nil {
		s.logger.WarnContext(ctx, "failed to cache user name", "error", err)
	}
	return user, nil
}

func (s *userService) UpdatePassword(ctx context.Context, userID uuid.UUID, oldPassword, newPassword string) (*User, error) {
	if err := ValidatePassword(newPassword); err != nil {
		return nil, err
	}
	_, hash, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if hash == "" {
		secure.BurnPasswordCheck(oldPassword)
		return nil, ErrNoPassword
	}
	ok, err := secure.CheckPassword(oldPassword, hash)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, secure.ErrPassNotMatch
	}
	newHash, err := secure.HashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	user, err := s.repo.UpdateUserPassword(ctx, UpdateUserPassword{UserID: userID, Password: newHash})
	if err != nil {
		return nil, err
	}
	// a password change signs out every session
	if err := s.repo.RevokeAllRefreshTokens(ctx, userID); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *userService) SearchUser(ctx context.Context, me uuid.UUID, q string) ([]PublicUser, error) {
	q = strings.TrimSpace(q)
	if q == "" || utf8.RuneCountInString(q) > maxNameLen {
		return nil, ErrInvalidInput
	}
	list, err := s.repo.SearchByName(ctx, me, q)
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (s *userService) Me(ctx context.Context, userID uuid.UUID) (*User, error) {
	u, _, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids, err := s.repo.Interests(ctx, userID)
	if err != nil {
		return nil, err
	}
	u.Interests = &ids
	return u, nil
}

func (s *userService) GetName(ctx context.Context, userID uuid.UUID) (string, error) {
	name, ok, err := s.store.Name(ctx, userID)
	if err != nil {
		s.logger.WarnContext(ctx, "name cache read failed", "error", err)
	}
	if ok {
		return name, nil
	}
	user, _, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if err := s.store.SetNames(ctx, PublicUser{ID: user.ID, Name: user.Name}); err != nil {
		s.logger.WarnContext(ctx, "failed to cache user name", "error", err)
	}
	return user.Name, nil
}

func (s *userService) UpdateProfile(ctx context.Context, me uuid.UUID, req UpdateProfileRequest) (*User, error) {
	if req.Name == nil && req.DisplayName == nil && req.Bio == nil && req.Location == nil && req.Interests == nil && req.Discoverable == nil {
		return nil, ErrInvalidInput
	}
	if req.DisplayName != nil {
		d := strings.TrimSpace(*req.DisplayName)
		if err := ValidateDisplayName(d); err != nil {
			return nil, err
		}
		req.DisplayName = &d
	}
	// validate everything before changing anything
	if req.Bio != nil {
		bio := strings.TrimSpace(*req.Bio)
		if utf8.RuneCountInString(bio) > maxBioLen {
			return nil, ErrBioTooLong
		}
		req.Bio = &bio
	}
	var name string
	if req.Name != nil {
		name = NormalizeUsername(*req.Name)
		if err := ValidateName(name); err != nil {
			return nil, err
		}
	}
	var country string
	if req.Location != nil && *req.Location != "" {
		p, ok := catalog.LookupPlace(*req.Location)
		if !ok {
			return nil, ErrBadPlace
		}
		country = p.Country
	}
	var interests []string
	if req.Interests != nil {
		seen := map[string]bool{}
		for _, id := range *req.Interests {
			if !catalog.ValidInterest(id) {
				return nil, ErrBadInterests
			}
			if !seen[id] {
				seen[id] = true
				interests = append(interests, id)
			}
		}
		if len(interests) > catalog.MaxInterests {
			return nil, ErrBadInterests
		}
	}

	if req.Name != nil {
		cur, _, err := s.repo.GetUserByID(ctx, me)
		if err != nil {
			return nil, err
		}
		if name != cur.Name {
			now := time.Now().UTC()
			if _, err := s.repo.Rename(ctx, me, name, now, now.Add(-RenameEvery)); err != nil {
				return nil, err
			}
			if err := s.store.SetNames(ctx, PublicUser{ID: me, Name: name}); err != nil {
				s.logger.WarnContext(ctx, "failed to cache new name", "error", err)
			}
			for _, fn := range s.onRename {
				fn(ctx, me)
			}
		}
	}
	if req.DisplayName != nil {
		if err := s.repo.SetDisplayName(ctx, me, *req.DisplayName); err != nil {
			return nil, err
		}
		s.profileChanged(ctx, me)
	}
	if req.Bio != nil {
		if _, err := s.repo.SetBio(ctx, me, *req.Bio); err != nil {
			return nil, err
		}
	}
	if req.Location != nil {
		if err := s.repo.SetLocation(ctx, me, *req.Location, country); err != nil {
			return nil, err
		}
	}
	if req.Interests != nil {
		if err := s.repo.SetInterests(ctx, me, interests); err != nil {
			return nil, err
		}
	}
	if req.Discoverable != nil {
		if err := s.repo.SetDiscoverable(ctx, me, *req.Discoverable); err != nil {
			return nil, err
		}
	}
	return s.Me(ctx, me)
}

func (s *userService) Profile(ctx context.Context, viewer, id uuid.UUID) (Profile, error) {
	return s.repo.PublicProfile(ctx, id, viewer)
}

func (s *userService) OnRename(fn func(ctx context.Context, userID uuid.UUID)) {
	s.onRename = append(s.onRename, fn)
}

func (s *userService) SetAvatar(ctx context.Context, me uuid.UUID, raw []byte) (*User, error) {
	img, err := ProcessAvatar(raw)
	if err != nil {
		return nil, err
	}
	// millisecond precision: the time is also the URL version
	if err := s.repo.SetAvatar(ctx, me, img, time.Now().UTC().Truncate(time.Millisecond)); err != nil {
		return nil, err
	}
	s.profileChanged(ctx, me)
	return s.Me(ctx, me)
}

func (s *userService) DeleteAvatar(ctx context.Context, me uuid.UUID) error {
	if err := s.repo.DeleteAvatar(ctx, me); err != nil {
		return err
	}
	s.profileChanged(ctx, me)
	return nil
}

func (s *userService) Avatar(ctx context.Context, viewer, id uuid.UUID) ([]byte, time.Time, error) {
	return s.repo.Avatar(ctx, id, viewer)
}

func (s *userService) profileChanged(ctx context.Context, id uuid.UUID) {
	for _, fn := range s.onRename {
		fn(ctx, id)
	}
}

// LogoutAll revokes the refresh tokens; the repository wrapper in app also
// ends access tokens, closes the socket and forgets the user's phones.
func (s *userService) LogoutAll(ctx context.Context, me uuid.UUID) error {
	return s.repo.RevokeAllRefreshTokens(ctx, me)
}

func (s *userService) DisplayName(ctx context.Context, id uuid.UUID) (string, error) {
	return s.repo.DisplayName(ctx, id)
}
