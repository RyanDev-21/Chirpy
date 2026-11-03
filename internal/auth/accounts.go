package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/mail"
	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"RyanDev-21.com/Chirpy/internal/users"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrEmailNotVerified = errors.New("email not verified")
	ErrInvalidCode      = errors.New("invalid or expired code")
	ErrMailUnavailable  = errors.New("could not send email, try again shortly")
	ErrInvalidGoogle    = errors.New("invalid Google sign-in")
	ErrReauthRequired   = errors.New("confirm with your password or a fresh Google sign-in")
)

const (
	purposeVerify = "verify_email"
	purposeReset  = "reset_password"

	codeTTL         = 15 * time.Minute
	codeMaxAttempts = 5
	// a new code is not sent more often than this per user and purpose
	codeResendGap = time.Minute

	signupTokenTTL = 15 * time.Minute
	signupAudience = "chirpy-google-signup"
)

// Session is what a successful sign-in returns.
type Session struct {
	AccessToken  string
	RefreshToken string
	User         *users.User
}

// GoogleResult is either a session (existing or linked account) or a
// signup token when the user still has to pick a username.
type GoogleResult struct {
	Session     *Session
	SignupToken string
}

// GoogleClaims are the verified fields of a Google ID token.
type GoogleClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
}

// GoogleVerifier checks a Google ID token's signature, issuer, expiry and
// audience.
type GoogleVerifier interface {
	Verify(ctx context.Context, idToken string) (GoogleClaims, error)
}

// Invalidator drops cached data about users and groups after account
// deletion (implemented by the app with the friends/groups/users stores).
type Invalidator interface {
	AccountDeleted(ctx context.Context, userID uuid.UUID, friends, groups []uuid.UUID)
}

type AccountService interface {
	// Register always answers the same way whether or not the email is
	// already registered, so it cannot be used to discover accounts.
	Register(ctx context.Context, name, email, password string) error
	ResendVerification(ctx context.Context, email string) error
	VerifyEmail(ctx context.Context, email, code string) (*Session, error)
	RequestPasswordReset(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, email, code, newPassword string) error
	Google(ctx context.Context, idToken string) (GoogleResult, error)
	CompleteGoogleSignup(ctx context.Context, signupToken, name string) (*Session, error)
	DeleteAccount(ctx context.Context, userID uuid.UUID, password, googleIDToken string) error
}

type tokenIssuer interface {
	issue(ctx context.Context, userID uuid.UUID) (access, refresh string, err error)
}

type accountService struct {
	users      users.UserService
	userRepo   users.UserRepo
	repo       AccountRepo
	mail       mail.Sender
	tokens     tokenIssuer
	google     GoogleVerifier
	invalidate Invalidator
	secret     []byte
	logger     *slog.Logger
	now        func() time.Time
}

func NewAccountService(us users.UserService, ur users.UserRepo, repo AccountRepo, sender mail.Sender,
	auth AuthService, google GoogleVerifier, inv Invalidator, secret string, logger *slog.Logger) AccountService {
	return &accountService{
		users: us, userRepo: ur, repo: repo, mail: sender, tokens: auth.(tokenIssuer),
		google: google, invalidate: inv, secret: []byte(secret), logger: logger, now: time.Now,
	}
}

// --- codes ---

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// hashCode keys the hash with the server secret: 6-digit codes are too few
// to protect with a plain hash.
func (s *accountService) hashCode(userID uuid.UUID, purpose, code string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(userID.String() + "|" + purpose + "|" + code))
	return hex.EncodeToString(m.Sum(nil))
}

// sendCode issues a fresh code unless one was sent within codeResendGap.
func (s *accountService) sendCode(ctx context.Context, u *users.User, purpose string) error {
	if c, err := s.repo.GetCode(ctx, u.ID, purpose); err == nil && s.now().Sub(c.CreatedAt) < codeResendGap {
		return nil
	}
	code, err := newCode()
	if err != nil {
		return err
	}
	if err := s.repo.PutCode(ctx, u.ID, purpose, s.hashCode(u.ID, purpose, code), s.now().Add(codeTTL)); err != nil {
		return err
	}
	return s.send(ctx, codeEmail(u, purpose, code))
}

// checkCode consumes a code. Wrong guesses count; the code dies after
// codeMaxAttempts. Every failure is ErrInvalidCode.
func (s *accountService) checkCode(ctx context.Context, userID uuid.UUID, purpose, code string) error {
	c, err := s.repo.GetCode(ctx, userID, purpose)
	if err != nil || s.now().After(c.ExpiresAt) {
		return ErrInvalidCode
	}
	if !hmac.Equal([]byte(c.Hash), []byte(s.hashCode(userID, purpose, strings.TrimSpace(code)))) {
		if n, err := s.repo.BumpAttempts(ctx, userID, purpose); err == nil && n >= codeMaxAttempts {
			_ = s.repo.DeleteCode(ctx, userID, purpose)
		}
		return ErrInvalidCode
	}
	return s.repo.DeleteCode(ctx, userID, purpose)
}

func (s *accountService) send(ctx context.Context, m mail.Message) error {
	if err := s.mail.Send(ctx, m); err != nil {
		s.logger.ErrorContext(ctx, "email send failed", "subject", m.Subject, "error", err)
		return ErrMailUnavailable
	}
	return nil
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// --- registration and verification ---

func (s *accountService) Register(ctx context.Context, name, email, password string) error {
	u, err := s.users.Register(ctx, name, email, password)
	switch {
	case err == nil:
		return s.sendCode(ctx, u, purposeVerify)
	case errors.Is(err, users.ErrDuplicateEmail):
		existing, _, lerr := s.userRepo.GetUserByEmail(ctx, normEmail(email))
		if lerr != nil {
			return lerr
		}
		if existing.Verified {
			// tell the owner instead of the caller
			return s.send(ctx, alreadyRegisteredEmail(existing))
		}
		return s.sendCode(ctx, existing, purposeVerify)
	default:
		return err // invalid input, duplicate username, ...
	}
}

func (s *accountService) ResendVerification(ctx context.Context, email string) error {
	u, _, err := s.userRepo.GetUserByEmail(ctx, normEmail(email))
	if errors.Is(err, users.ErrUserNotFound) || (err == nil && u.Verified) {
		return nil // same answer either way
	}
	if err != nil {
		return err
	}
	return s.sendCode(ctx, u, purposeVerify)
}

func (s *accountService) VerifyEmail(ctx context.Context, email, code string) (*Session, error) {
	u, _, err := s.userRepo.GetUserByEmail(ctx, normEmail(email))
	if errors.Is(err, users.ErrUserNotFound) {
		return nil, ErrInvalidCode
	}
	if err != nil {
		return nil, err
	}
	if err := s.checkCode(ctx, u.ID, purposeVerify, code); err != nil {
		return nil, err
	}
	if err := s.repo.MarkVerified(ctx, u.ID); err != nil {
		return nil, err
	}
	u.Verified = true
	return s.session(ctx, u)
}

// --- password reset ---

func (s *accountService) RequestPasswordReset(ctx context.Context, email string) error {
	u, _, err := s.userRepo.GetUserByEmail(ctx, normEmail(email))
	if errors.Is(err, users.ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.sendCode(ctx, u, purposeReset)
}

// ResetPassword also proves the email, so it marks the account verified,
// and signs out every session.
func (s *accountService) ResetPassword(ctx context.Context, email, code, newPassword string) error {
	if err := users.ValidatePassword(newPassword); err != nil {
		return err
	}
	u, _, err := s.userRepo.GetUserByEmail(ctx, normEmail(email))
	if errors.Is(err, users.ErrUserNotFound) {
		return ErrInvalidCode
	}
	if err != nil {
		return err
	}
	if err := s.checkCode(ctx, u.ID, purposeReset, code); err != nil {
		return err
	}
	hash, err := secure.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.repo.SetPassword(ctx, u.ID, hash); err != nil {
		return err
	}
	if err := s.repo.MarkVerified(ctx, u.ID); err != nil {
		return err
	}
	return s.userRepo.RevokeAllRefreshTokens(ctx, u.ID)
}

// --- Google ---

func (s *accountService) Google(ctx context.Context, idToken string) (GoogleResult, error) {
	if s.google == nil {
		return GoogleResult{}, ErrInvalidGoogle
	}
	c, err := s.google.Verify(ctx, idToken)
	if err != nil || !c.EmailVerified || c.Subject == "" || c.Email == "" {
		return GoogleResult{}, ErrInvalidGoogle
	}
	email := normEmail(c.Email)

	// 1. already linked
	if id, err := s.repo.Identity(ctx, "google", c.Subject); err == nil {
		u, _, err := s.userRepo.GetUserByID(ctx, id)
		if err != nil {
			return GoogleResult{}, err
		}
		sess, err := s.session(ctx, u)
		return GoogleResult{Session: sess}, err
	}

	// 2. an account with this email exists: link it. Google has proven who
	// owns the email, so an unverified account (possibly registered by
	// someone squatting the address) loses its password and sessions.
	if u, _, err := s.userRepo.GetUserByEmail(ctx, email); err == nil {
		if !u.Verified {
			if err := s.repo.SetPassword(ctx, u.ID, ""); err != nil {
				return GoogleResult{}, err
			}
			if err := s.userRepo.RevokeAllRefreshTokens(ctx, u.ID); err != nil {
				return GoogleResult{}, err
			}
			if err := s.repo.MarkVerified(ctx, u.ID); err != nil {
				return GoogleResult{}, err
			}
			u.Verified = true
		}
		if err := s.repo.LinkIdentity(ctx, "google", c.Subject, u.ID, email); err != nil {
			return GoogleResult{}, err
		}
		sess, err := s.session(ctx, u)
		return GoogleResult{Session: sess}, err
	} else if !errors.Is(err, users.ErrUserNotFound) {
		return GoogleResult{}, err
	}

	// 3. new user: they must pick a username first
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": c.Subject, "email": email, "aud": signupAudience,
		"exp": s.now().Add(signupTokenTTL).Unix(), "iat": s.now().Unix(),
	}).SignedString(s.secret)
	return GoogleResult{SignupToken: tok}, err
}

func (s *accountService) CompleteGoogleSignup(ctx context.Context, signupToken, name string) (*Session, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(signupToken, claims, func(t *jwt.Token) (any, error) { return s.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(signupAudience), jwt.WithExpirationRequired())
	if err != nil {
		return nil, ErrInvalidGoogle
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	if sub == "" || email == "" {
		return nil, ErrInvalidGoogle
	}
	if err := users.ValidateName(name); err != nil {
		return nil, err
	}
	u, err := s.repo.CreateGoogleUser(ctx, strings.TrimSpace(name), email, sub)
	if errors.Is(err, users.ErrDuplicateEmail) {
		// the token was already used, or the email got an account meanwhile:
		// signing in with Google again will log in or link instead
		return nil, ErrInvalidGoogle
	}
	if err != nil {
		return nil, err
	}
	return s.session(ctx, u)
}

// --- deletion ---

// DeleteAccount needs the password, or for an account without one a fresh
// Google ID token for the same Google account.
func (s *accountService) DeleteAccount(ctx context.Context, userID uuid.UUID, password, googleIDToken string) error {
	_, hash, err := s.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	switch {
	case hash != "" && password != "":
		ok, err := secure.CheckPassword(password, hash)
		if err != nil || !ok {
			return ErrReauthRequired
		}
	case googleIDToken != "" && s.google != nil:
		c, err := s.google.Verify(ctx, googleIDToken)
		if err != nil {
			return ErrReauthRequired
		}
		if id, err := s.repo.Identity(ctx, "google", c.Subject); err != nil || id != userID {
			return ErrReauthRequired
		}
	default:
		return ErrReauthRequired
	}
	friends, groups, err := s.repo.DeleteUser(ctx, userID)
	if err != nil {
		return err
	}
	if s.invalidate != nil {
		s.invalidate.AccountDeleted(ctx, userID, friends, groups)
	}
	s.logger.InfoContext(ctx, "account deleted", "user_id", userID)
	return nil
}

func (s *accountService) session(ctx context.Context, u *users.User) (*Session, error) {
	access, refresh, err := s.tokens.issue(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &Session{AccessToken: access, RefreshToken: refresh, User: u}, nil
}

// --- email bodies ---

func codeEmail(u *users.User, purpose, code string) mail.Message {
	what, subject := "confirm your email", "Your Chirpy verification code"
	if purpose == purposeReset {
		what, subject = "reset your password", "Your Chirpy password reset code"
	}
	text := fmt.Sprintf("Hi %s,\n\nYour code to %s is %s.\nIt expires in 15 minutes.\n\nIf you didn't ask for this, you can ignore this email.\n", u.Name, what, code)
	html := fmt.Sprintf(`<p>Hi %s,</p><p>Your code to %s is</p><p style="font-size:28px;font-weight:bold;letter-spacing:4px">%s</p><p>It expires in 15 minutes. If you didn't ask for this, you can ignore this email.</p>`,
		htmlEscape(u.Name), what, code)
	return mail.Message{To: u.Email, Subject: subject, Text: text, HTML: html}
}

func alreadyRegisteredEmail(u *users.User) mail.Message {
	text := fmt.Sprintf("Hi %s,\n\nSomeone tried to create a new Chirpy account with this email address, which already has an account. If it was you, sign in instead or reset your password. If not, you can ignore this email.\n", u.Name)
	return mail.Message{To: u.Email, Subject: "Someone tried to sign up with your email", Text: text,
		HTML: "<p>" + strings.ReplaceAll(htmlEscape(text), "\n", "<br>") + "</p>"}
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}
