package users

import (
	"context"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"RyanDev-21.com/Chirpy/internal/platform/queue"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"github.com/google/uuid"
)

const (
	minNameLen     = 3
	maxNameLen     = 25 // users.name VARCHAR(25)
	maxEmailLen    = 125
	minPasswordLen = 8
	maxPasswordLen = 128
	maxConfigItems = 100
)

const passwordSpecials = "@$!%*?&"

type UserService interface {
	Register(ctx context.Context, name, email, password string) (*User, error)
	UpdatePassword(ctx context.Context, userID uuid.UUID, oldPass, newPass string) (*User, error)
	SearchUser(ctx context.Context, q string) ([]PublicUser, error)
	SaveConfig(ctx context.Context, userID uuid.UUID, list []ElementCustom) error
	GetConfig(ctx context.Context, userID uuid.UUID) ([]ElementCustom, error)
	// GetName returns a user's display name (cached).
	GetName(ctx context.Context, userID uuid.UUID) (string, error)

	HandleSaveConfig(ctx context.Context, job JobSaveConfig) error
}

type userService struct {
	repo   UserRepo
	store  Store
	queue  queue.Publisher
	logger *slog.Logger
}

func NewUserService(repo UserRepo, store Store, q queue.Publisher, logger *slog.Logger) UserService {
	return &userService{repo: repo, store: store, queue: q, logger: logger}
}

// ValidatePassword enforces the password policy.
func ValidatePassword(p string) error {
	if len(p) < minPasswordLen || len(p) > maxPasswordLen || !strings.ContainsAny(p, passwordSpecials) {
		return ErrInvalidInput
	}
	return nil
}

func validateRegister(name, email, password string) error {
	n := utf8.RuneCountInString(name)
	if n < minNameLen || n > maxNameLen || strings.ContainsAny(name, " \t\n") {
		return ErrInvalidInput
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
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))
	if err := validateRegister(name, email, password); err != nil {
		return nil, err
	}
	hash, err := secure.HashPassword(password)
	if err != nil {
		return nil, err
	}
	user, err := s.repo.Create(ctx, CreateUserInput{Name: name, Email: email, Password: hash})
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

func (s *userService) SearchUser(ctx context.Context, q string) ([]PublicUser, error) {
	q = strings.TrimSpace(q)
	if q == "" || utf8.RuneCountInString(q) > maxNameLen {
		return nil, ErrInvalidInput
	}
	list, err := s.repo.SearchByName(ctx, q)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetNames(ctx, list...); err != nil {
		s.logger.WarnContext(ctx, "failed to cache searched names", "error", err)
	}
	return list, nil
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

func (s *userService) SaveConfig(ctx context.Context, userID uuid.UUID, list []ElementCustom) error {
	if len(list) > maxConfigItems {
		return ErrInvalidInput
	}
	if list == nil {
		list = []ElementCustom{}
	}
	if err := s.store.SetConfig(ctx, userID, list); err != nil {
		return err
	}
	pubCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.queue.Publish(pubCtx, TopicSaveConfig, JobSaveConfig{UserID: userID, List: list})
}

func (s *userService) GetConfig(ctx context.Context, userID uuid.UUID) ([]ElementCustom, error) {
	list, ok, err := s.store.Config(ctx, userID)
	if err != nil {
		s.logger.WarnContext(ctx, "config cache read failed", "error", err)
	}
	if ok {
		return list, nil
	}
	list, err = s.repo.GetConfig(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetConfig(ctx, userID, list); err != nil {
		s.logger.WarnContext(ctx, "failed to cache config", "error", err)
	}
	return list, nil
}

// HandleSaveConfig is the worker for TopicSaveConfig.
func (s *userService) HandleSaveConfig(ctx context.Context, job JobSaveConfig) error {
	return s.repo.SaveConfig(ctx, job.UserID, job.List)
}
