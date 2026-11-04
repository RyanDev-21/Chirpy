package users

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/secure"
	"RyanDev-21.com/Chirpy/internal/testutil"
	"github.com/google/uuid"
)

// generated so no credential-shaped literal is committed
var testPassword = "Pw-" + uuid.NewString()[:8] + "!"

type fakeRepo struct {
	interests map[uuid.UUID][]string
	users     map[uuid.UUID]*User
	hashes    map[uuid.UUID]string
	revoked   int
	lookups   int
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
func (f *fakeRepo) SearchByName(context.Context, uuid.UUID, string) ([]PublicUser, error) {
	return []PublicUser{}, nil
}

func (f *fakeRepo) Rename(_ context.Context, id uuid.UUID, name string, now, cutoff time.Time) (*User, error) {
	u := f.users[id]
	if u.NameChangeAvailableAt != nil && u.NameChangeAvailableAt.After(now) {
		return nil, &RenameTooSoonError{Until: *u.NameChangeAvailableAt}
	}
	next := now.Add(RenameEvery)
	u.Name, u.NameChangeAvailableAt = name, &next
	return u, nil
}
func (f *fakeRepo) SetBio(_ context.Context, id uuid.UUID, bio string) (*User, error) {
	f.users[id].Bio = bio
	return f.users[id], nil
}
func (f *fakeRepo) PublicProfile(_ context.Context, id, _ uuid.UUID) (Profile, error) {
	u, ok := f.users[id]
	if !ok {
		return Profile{}, ErrUserNotFound
	}
	return Profile{ID: id, Name: u.Name, Bio: u.Bio}, nil
}

func (f *fakeRepo) SetAvatar(context.Context, uuid.UUID, []byte, time.Time) error { return nil }
func (f *fakeRepo) DeleteAvatar(context.Context, uuid.UUID) error                 { return nil }
func (f *fakeRepo) Avatar(context.Context, uuid.UUID, uuid.UUID) ([]byte, time.Time, error) {
	return nil, time.Time{}, ErrUserNotFound
}

func (f *fakeRepo) SetLocation(_ context.Context, id uuid.UUID, place, _ string) error {
	f.users[id].Location = PlaceOf(place)
	return nil
}
func (f *fakeRepo) SetDiscoverable(_ context.Context, id uuid.UUID, on bool) error {
	f.users[id].Discoverable = on
	return nil
}
func (f *fakeRepo) SetInterests(_ context.Context, id uuid.UUID, ids []string) error {
	if f.interests == nil {
		f.interests = map[uuid.UUID][]string{}
	}
	f.interests[id] = ids
	return nil
}
func (f *fakeRepo) Interests(_ context.Context, id uuid.UUID) ([]string, error) {
	return append([]string{}, f.interests[id]...), nil
}

func (f *fakeRepo) SetDisplayName(_ context.Context, id uuid.UUID, n string) error {
	f.users[id].DisplayName = n
	return nil
}
func (f *fakeRepo) DisplayName(_ context.Context, id uuid.UUID) (string, error) {
	return ShownName(f.users[id].Name, f.users[id].DisplayName), nil
}

func newSvc(t *testing.T) (UserService, *fakeRepo) {
	rdb, _ := testutil.Redis(t)
	repo := newFakeRepo()
	return NewUserService(repo, NewStore(rdb), testutil.Logger()), repo
}

func TestRegisterValidation(t *testing.T) {
	svc, _ := newSvc(t)
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
	svc, repo := newSvc(t)
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
	svc, repo := newSvc(t)
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

func TestUpdateProfile(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	u, _ := repo.Create(ctx, CreateUserInput{Name: "before", Email: "p@x.io"})
	str := func(s string) *string { return &s }
	renamed := 0
	svc.OnRename(func(context.Context, uuid.UUID) { renamed++ })

	for _, req := range []UpdateProfileRequest{
		{},
		{Name: str("no spaces allowed")},
		{Name: str("ab")},
	} {
		if _, err := svc.UpdateProfile(ctx, u.ID, req); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: %v", req, err)
		}
	}
	if _, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Bio: str(strings.Repeat("b", maxBioLen+1))}); !errors.Is(err, ErrBioTooLong) {
		t.Fatalf("long bio: %v", err)
	}
	got, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Name: str(" after "), Bio: str(" hi ")})
	if err != nil || got.Name != "after" || got.Bio != "hi" || renamed != 1 {
		t.Fatalf("update = %+v %v (renamed %d)", got, err, renamed)
	}
	// same name again is not a rename; a different one is too soon
	if _, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Name: str("after")}); err != nil || renamed != 1 {
		t.Fatalf("same name: %v (renamed %d)", err, renamed)
	}
	var soon *RenameTooSoonError
	if _, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Name: str("again")}); !errors.As(err, &soon) {
		t.Fatalf("second rename: %v", err)
	}
	if name, _ := svc.GetName(ctx, u.ID); name != "after" {
		t.Fatalf("cached name %q", name)
	}
}

func TestUpdateDiscoveryProfile(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	u, _ := repo.Create(ctx, CreateUserInput{Name: "finder", Email: "f@x.io"})
	str := func(s string) *string { return &s }
	list := func(ids ...string) *[]string { return &ids }
	on := true

	if _, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Location: str("atlantis")}); !errors.Is(err, ErrBadPlace) {
		t.Fatalf("unknown place: %v", err)
	}
	if _, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Interests: list("gaming", "knitting-cats")}); !errors.Is(err, ErrBadInterests) {
		t.Fatalf("unknown interest: %v", err)
	}
	eleven := list("gaming", "football", "music", "kpop", "anime", "coding", "travel", "food", "pets", "reading", "art")
	if _, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Interests: eleven}); !errors.Is(err, ErrBadInterests) {
		t.Fatalf("eleven interests: %v", err)
	}
	// an invalid field means nothing is changed
	if _, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Bio: str("kept?"), Location: str("atlantis")}); err == nil || repo.users[u.ID].Bio != "" {
		t.Fatalf("partial update applied: %v %q", err, repo.users[u.ID].Bio)
	}

	got, err := svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{
		Location: str("mm-yangon"), Interests: list("gaming", "kpop", "gaming"), Discoverable: &on,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Location == nil || got.Location.Name != "Yangon" || !got.Discoverable || got.Interests == nil || len(*got.Interests) != 2 {
		t.Fatalf("profile %+v", got)
	}
	if got, _ = svc.UpdateProfile(ctx, u.ID, UpdateProfileRequest{Location: str("")}); got.Location != nil {
		t.Fatalf("location not cleared: %+v", got.Location)
	}
}
