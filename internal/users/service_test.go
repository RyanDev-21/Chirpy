package users

import (
	"context"
	"errors"
	"strings"
	"testing"

	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"RyanDev-21.com/Chirpy/internal/testutil"
	"github.com/google/uuid"
)

// generated so no credential-shaped literal is committed
var testPassword = "Pw-" + uuid.NewString()[:8] + "!"

type fakeRepo struct {
	users   map[uuid.UUID]*User
	hashes  map[uuid.UUID]string
	revoked int
	lookups int
	config  []ElementCustom
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: map[uuid.UUID]*User{}, hashes: map[uuid.UUID]string{}}
}

func (f *fakeRepo) Create(_ context.Context, in CreateUserInput) (*User, error) {
	for _, u := range f.users {
		if u.Email == in.Email {
			return nil, ErrDuplicateEmail
		}
	}
	u := &User{ID: uuid.New(), Name: in.Name, Email: in.Email}
	f.users[u.ID], f.hashes[u.ID] = u, in.Password
	return u, nil
}
func (f *fakeRepo) GetUserByEmail(context.Context, string) (*User, string, error) {
	return nil, "", ErrUserNotFound
}
func (f *fakeRepo) GetUserByID(_ context.Context, id uuid.UUID) (*User, string, error) {
	f.lookups++
	u, ok := f.users[id]
	if !ok {
		return nil, "", ErrUserNotFound
	}
	return u, f.hashes[id], nil
}
func (f *fakeRepo) UpdateUserPassword(_ context.Context, p UpdateUserPassword) (*User, error) {
	f.hashes[p.UserID] = p.Password
	return f.users[p.UserID], nil
}
func (f *fakeRepo) RevokeAllRefreshTokens(context.Context, uuid.UUID) error { f.revoked++; return nil }
func (f *fakeRepo) SearchByName(context.Context, string) ([]PublicUser, error) {
	return []PublicUser{}, nil
}
func (f *fakeRepo) SaveConfig(context.Context, uuid.UUID, []ElementCustom) error { return nil }
func (f *fakeRepo) GetConfig(context.Context, uuid.UUID) ([]ElementCustom, error) {
	return f.config, nil
}

type nopQueue struct{ n int }

func (q *nopQueue) Publish(context.Context, string, any) error { q.n++; return nil }

func newSvc(t *testing.T) (UserService, *fakeRepo, *nopQueue) {
	rdb, _ := testutil.Redis(t)
	repo, q := newFakeRepo(), &nopQueue{}
	return NewUserService(repo, NewStore(rdb), q, testutil.Logger()), repo, q
}

func TestRegisterValidation(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	cases := []struct{ name, email, pass string }{
		{"ab", "a@b.io", testPassword},
		{"has space", "a@b.io", testPassword},
		{strings.Repeat("n", 26), "a@b.io", testPassword},
		{"alice", "not-an-email", testPassword},
		{"alice", "Alice <a@b.io>", testPassword},
		{"alice", "a@b.io", "short!"},
		{"alice", "a@b.io", "nospecialchars"},
	}
	for _, c := range cases {
		if _, err := svc.Register(ctx, c.name, c.email, c.pass); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Register(%q,%q,%q) err = %v", c.name, c.email, c.pass, err)
		}
	}
	u, err := svc.Register(ctx, "alice", " Alice@Example.io ", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.io" {
		t.Fatalf("email not normalised: %q", u.Email)
	}
}

func TestGetNameIsCached(t *testing.T) {
	svc, repo, _ := newSvc(t)
	ctx := context.Background()
	id := uuid.New()
	repo.users[id] = &User{ID: id, Name: "bob"}
	for range 3 {
		name, err := svc.GetName(ctx, id)
		if err != nil || name != "bob" {
			t.Fatalf("GetName = %q, %v", name, err)
		}
	}
	if repo.lookups != 1 {
		t.Fatalf("postgres hit %d times, want 1", repo.lookups)
	}
	if _, err := svc.GetName(ctx, uuid.New()); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestUpdatePasswordRevokesSessions(t *testing.T) {
	svc, repo, _ := newSvc(t)
	ctx := context.Background()
	u, err := svc.Register(ctx, "carol", "c@x.io", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdatePassword(ctx, u.ID, "wrong-"+testPassword, testPassword+"2"); !errors.Is(err, secure.ErrPassNotMatch) {
		t.Fatalf("wrong old password: %v", err)
	}
	if _, err := svc.UpdatePassword(ctx, u.ID, testPassword, "weak"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("weak new password: %v", err)
	}
	if _, err := svc.UpdatePassword(ctx, u.ID, testPassword, testPassword+"2"); err != nil {
		t.Fatal(err)
	}
	if repo.revoked != 1 {
		t.Fatalf("refresh tokens revoked %d times", repo.revoked)
	}
}

func TestConfigCacheAndQueue(t *testing.T) {
	svc, repo, q := newSvc(t)
	ctx := context.Background()
	id := uuid.New()
	repo.config = []ElementCustom{{Label: "from-db"}}

	got, err := svc.GetConfig(ctx, id)
	if err != nil || len(got) != 1 || got[0].Label != "from-db" {
		t.Fatalf("GetConfig = %v %v", got, err)
	}
	if err := svc.SaveConfig(ctx, id, []ElementCustom{{Label: "new"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.GetConfig(ctx, id)
	if got[0].Label != "new" || q.n != 1 {
		t.Fatalf("after save: %v (queued %d)", got, q.n)
	}
	if err := svc.SaveConfig(ctx, id, make([]ElementCustom, maxConfigItems+1)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("too many items: %v", err)
	}
}
