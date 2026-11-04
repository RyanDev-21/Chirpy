package groups

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"RyanDev-21.com/Chirpy/internal/testutil"
	"github.com/google/uuid"
)

// fakeRepo applies the same rules the postgres transactions enforce.
type fakeRepo struct {
	mu      sync.Mutex
	groups  map[uuid.UUID]*GroupSnapshot
	codes   map[uuid.UUID]string
	reads   int
	updated [][2]*string
}

func (f *fakeRepo) ByInviteCode(_ context.Context, code string) (GroupPreview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, c := range f.codes {
		if c == code {
			return GroupPreview{ID: id, Name: f.groups[id].Name}, nil
		}
	}
	return GroupPreview{}, ErrNotFoundGroup
}
func (f *fakeRepo) InviteCode(_ context.Context, id uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.codes[id], nil
}
func (f *fakeRepo) RegenerateInviteCode(_ context.Context, id uuid.UUID) (string, error) {
	c, _ := NewInviteCode()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes[id] = c
	return c, nil
}

func (f *fakeRepo) Create(_ context.Context, id, creator uuid.UUID, req createGroupRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups[id] = &GroupSnapshot{Name: req.GroupName, MaxMem: req.MaxMems, AdminID: creator, Public: req.Visibility == Public, Members: []uuid.UUID{creator}}
	f.codes[id], _ = NewInviteCode()
	return nil
}

func (f *fakeRepo) Snapshot(_ context.Context, id uuid.UUID) (GroupSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	g, ok := f.groups[id]
	if !ok {
		return GroupSnapshot{}, ErrNotFoundGroup
	}
	cp := *g
	cp.Members = slices.Clone(g.Members)
	if g.Mods != nil {
		cp.Mods = map[uuid.UUID][]string{}
		for k, v := range g.Mods {
			cp.Mods[k] = slices.Clone(v)
		}
	}
	return cp, nil
}

func (f *fakeRepo) Join(_ context.Context, id, user uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[id]
	switch {
	case !ok:
		return ErrNotFoundGroup
	case slices.Contains(g.Members, user):
		return ErrAlreadyMember
	case len(g.Members) >= int(g.MaxMem):
		return ErrGroupFull
	}
	g.Members = append(g.Members, user)
	return nil
}

func (f *fakeRepo) Leave(_ context.Context, id, user, successor uuid.UUID) (Leaving, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[id]
	if !ok {
		return Leaving{}, ErrNotFoundGroup
	}
	i := slices.Index(g.Members, user)
	if i == -1 {
		return Leaving{}, ErrNotMember
	}
	g.Members = slices.Delete(g.Members, i, i+1)
	var out Leaving
	if user == g.AdminID {
		if len(g.Members) == 0 {
			delete(f.groups, id)
			return Leaving{Deleted: true}, nil
		}
		g.AdminID = g.Members[0]
		if successor != uuid.Nil {
			g.AdminID = successor
		}
		out.NewAdmin = g.AdminID
	}
	return out, nil
}
func (f *fakeRepo) Search(context.Context, uuid.UUID, string) ([]GroupPreview, error) {
	return nil, nil
}
func (f *fakeRepo) SetRole(_ context.Context, id, _, target uuid.UUID, role string, perms []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.groups[id]
	if g.Mods == nil {
		g.Mods = map[uuid.UUID][]string{}
	}
	delete(g.Mods, target)
	if role == RoleModerator {
		g.Mods[target] = perms
	}
	return nil
}

func (f *fakeRepo) ListForUser(context.Context, uuid.UUID) ([]GroupSummary, error) { return nil, nil }
func (f *fakeRepo) Detail(_ context.Context, id, viewer uuid.UUID) (GroupDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[id]
	if !ok {
		return GroupDetail{}, ErrNotFoundGroup
	}
	return GroupDetail{ID: id, Name: g.Name, IsMember: slices.Contains(g.Members, viewer)}, nil
}

func (f *fakeRepo) Kick(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeRepo) Update(_ context.Context, id, _ uuid.UUID, name, desc, _ *string) (GroupPreview, error) {
	f.updated = append(f.updated, [2]*string{name, desc})
	return GroupPreview{ID: id}, nil
}
func (f *fakeRepo) TransferAdmin(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeRepo) Delete(context.Context, uuid.UUID, uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (f *fakeRepo) AddMember(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeRepo) SetAvatar(context.Context, uuid.UUID, uuid.UUID, []byte, time.Time) error {
	return nil
}
func (f *fakeRepo) DeleteAvatar(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeRepo) Avatar(context.Context, uuid.UUID, uuid.UUID) ([]byte, time.Time, error) {
	return nil, time.Time{}, ErrNoAvatar
}

func newSvc(t *testing.T) (GroupService, *fakeRepo) {
	t.Helper()
	rdb, _ := testutil.Redis(t)
	repo := &fakeRepo{groups: map[uuid.UUID]*GroupSnapshot{}, codes: map[uuid.UUID]string{}}
	return NewGroupService(repo, NewStore(rdb), nil, testutil.Logger()), repo
}

func TestCreateValidation(t *testing.T) {
	svc, _ := newSvc(t)
	bad := []createGroupRequest{
		{GroupName: "", MaxMems: 5},
		{GroupName: "   ", MaxMems: 5},
		{GroupName: strings.Repeat("x", maxGroupNameLen+1), MaxMems: 5},
		{GroupName: "ok", MaxMems: 1},
		{GroupName: "ok", MaxMems: maxGroupSize + 1},
		{GroupName: "ok", MaxMems: 5, Description: strings.Repeat("d", maxDescLen+1)},
	}
	for _, req := range bad {
		if _, err := svc.CreateGroup(context.Background(), uuid.New(), req); !errors.Is(err, ErrInvalidGroupReq) {
			t.Errorf("CreateGroup(%q, %d) err = %v", req.GroupName, req.MaxMems, err)
		}
	}
}

func TestMembershipCacheFollowsWrites(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	leader, a := uuid.New(), uuid.New()

	g, err := svc.CreateGroup(ctx, leader, createGroupRequest{GroupName: "devs", MaxMems: 3})
	if err != nil {
		t.Fatal(err)
	}
	// warm the cache, then change membership and check it is not stale
	if ok, _ := svc.IsMember(ctx, g.ChatID, a); ok {
		t.Fatal("a is not a member yet")
	}
	if err := svc.JoinGroup(ctx, g.ChatID, a); err != nil {
		t.Fatal(err)
	}
	if ok, _ := svc.IsMember(ctx, g.ChatID, a); !ok {
		t.Fatal("join not visible: cache was not invalidated")
	}
	if err := svc.LeaveGroup(ctx, g.ChatID, a, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if ok, _ := svc.IsMember(ctx, g.ChatID, a); ok {
		t.Fatal("leave not visible: cache was not invalidated")
	}

	reads := repo.reads
	for range 3 {
		_, _ = svc.Members(ctx, g.ChatID)
	}
	if repo.reads != reads {
		t.Fatalf("repeated reads hit postgres %d times", repo.reads-reads)
	}
}

func TestErrorsFromRepoArePassedThrough(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	leader := uuid.New()
	g, _ := svc.CreateGroup(ctx, leader, createGroupRequest{GroupName: "Team", MaxMems: 2})

	// names don't have to be unique
	if _, err := svc.CreateGroup(ctx, leader, createGroupRequest{GroupName: "team", MaxMems: 2}); err != nil {
		t.Fatalf("same name: %v", err)
	}
	if err := svc.JoinGroup(ctx, g.ChatID, leader); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("rejoin: %v", err)
	}
	_ = svc.JoinGroup(ctx, g.ChatID, uuid.New())
	if err := svc.JoinGroup(ctx, g.ChatID, uuid.New()); !errors.Is(err, ErrGroupFull) {
		t.Fatalf("full: %v", err)
	}
	if _, err := svc.Members(ctx, uuid.New()); !errors.Is(err, ErrNotFoundGroup) {
		t.Fatalf("unknown group: %v", err)
	}
}

func TestInviteCodeFormat(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		c, err := NewInviteCode()
		if err != nil || len(c) != inviteLen || strings.ContainsAny(c, "01IO") {
			t.Fatalf("bad code %q: %v", c, err)
		}
		seen[c] = true
	}
	if len(seen) < 199 {
		t.Fatalf("codes repeat: %d unique of 200", len(seen))
	}
	for in, want := range map[string]string{"K7M9-QX2P": "K7M9QX2P", "k7m9 qx2p": "K7M9QX2P", "K7M9QX2P": "K7M9QX2P"} {
		if got, err := NormalizeInviteCode(in); err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "K7M9-QX2", "K7M9-QX2PZ", "K7M0-QX2P", "K7M9_QX2P", "../../etc"} {
		if _, err := NormalizeInviteCode(bad); !errors.Is(err, ErrInvalidInviteCode) {
			t.Errorf("Normalize(%q) accepted", bad)
		}
	}
	if FormatInviteCode("K7M9QX2P") != "K7M9-QX2P" {
		t.Fatal("display form must be XXXX-XXXX")
	}
}

func TestInvites(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	leader, member, outsider := uuid.New(), uuid.New(), uuid.New()
	g, _ := svc.CreateGroup(ctx, leader, createGroupRequest{GroupName: "devs", MaxMems: 5})

	code, err := svc.InviteCode(ctx, g.ChatID, leader)
	if err != nil || len(code) != 9 || code[4] != '-' {
		t.Fatalf("leader's code = %q, %v", code, err)
	}
	if _, err := svc.InviteCode(ctx, g.ChatID, outsider); !errors.Is(err, ErrNotFoundGroup) {
		t.Fatalf("outsider must not see the code: %v", err)
	}
	if _, err := svc.GroupDetail(ctx, g.ChatID, outsider); !errors.Is(err, ErrNotFoundGroup) {
		t.Fatalf("outsider must not see a private group: %v", err)
	}
	// typed loosely, the code still works
	id, err := svc.JoinByInvite(ctx, member, strings.ToLower(strings.ReplaceAll(code, "-", " ")))
	if err != nil || id != g.ChatID {
		t.Fatalf("join by code = %v, %v", id, err)
	}
	if c, err := svc.InviteCode(ctx, g.ChatID, member); err != nil || c != code {
		t.Fatalf("member's code = %q, %v", c, err)
	}
	if _, err := svc.RegenerateInvite(ctx, g.ChatID, member); !errors.Is(err, ErrNoPermission) {
		t.Fatalf("member regenerated the code: %v", err)
	}
	fresh, err := svc.RegenerateInvite(ctx, g.ChatID, leader)
	if err != nil || fresh == code {
		t.Fatalf("regenerate = %q, %v", fresh, err)
	}
	if _, err := svc.JoinByInvite(ctx, outsider, code); !errors.Is(err, ErrNotFoundGroup) {
		t.Fatalf("old code still works: %v", err)
	}
	if _, err := svc.JoinByInvite(ctx, outsider, "nope"); !errors.Is(err, ErrInvalidInviteCode) {
		t.Fatalf("garbage code: %v", err)
	}
}

func TestUpdateValidation(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	str := func(s string) *string { return &s }
	cases := []struct {
		req  updateGroupRequest
		want error
	}{
		{updateGroupRequest{}, ErrNothingToUpdate},
		{updateGroupRequest{GroupName: str("   ")}, ErrInvalidGroupInfo},
		{updateGroupRequest{GroupName: str(strings.Repeat("x", maxGroupNameLen+1))}, ErrInvalidGroupInfo},
		{updateGroupRequest{Description: str(strings.Repeat("x", maxDescLen+1))}, ErrInvalidGroupInfo},
	}
	for _, c := range cases {
		if _, err := svc.Update(ctx, uuid.New(), uuid.New(), c.req); !errors.Is(err, c.want) {
			t.Errorf("%+v: got %v, want %v", c.req, err, c.want)
		}
	}
	if _, err := svc.Update(ctx, uuid.New(), uuid.New(), updateGroupRequest{GroupName: str("  team  ")}); err != nil {
		t.Fatal(err)
	}
	if got := repo.updated[0][0]; got == nil || *got != "team" || repo.updated[0][1] != nil {
		t.Fatalf("repo got %v", repo.updated[0])
	}
}

func TestCreateVisibility(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	if _, err := svc.CreateGroup(ctx, uuid.New(), createGroupRequest{GroupName: "x", MaxMems: 5, Visibility: "secret"}); !errors.Is(err, ErrInvalidGroupReq) {
		t.Fatalf("bad visibility: %v", err)
	}
	g, err := svc.CreateGroup(ctx, uuid.New(), createGroupRequest{GroupName: "x", MaxMems: 5})
	if err != nil || repo.groups[g.ChatID].Public {
		t.Fatalf("default must be private: %v", err)
	}
}

func TestModeratorPermissions(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin, mod, mod2, member := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g, _ := svc.CreateGroup(ctx, admin, createGroupRequest{GroupName: "devs", MaxMems: 10})
	for _, u := range []uuid.UUID{mod, mod2, member} {
		if err := svc.JoinGroup(ctx, g.ChatID, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.SetRole(ctx, g.ChatID, admin, mod, roleRequest{Role: "admin"}); !errors.Is(err, ErrBadRole) {
		t.Fatalf("a second admin: %v", err)
	}
	if err := svc.SetRole(ctx, g.ChatID, admin, mod, roleRequest{Role: RoleModerator, Permissions: []string{"ban_everyone"}}); !errors.Is(err, ErrBadRole) {
		t.Fatalf("unknown permission: %v", err)
	}
	if err := svc.SetRole(ctx, g.ChatID, admin, mod, roleRequest{Role: RoleModerator, Permissions: []string{PermDeleteMessages, PermManageInvite, PermDeleteMessages}}); err != nil {
		t.Fatal(err)
	}
	_ = svc.SetRole(ctx, g.ChatID, admin, mod2, roleRequest{Role: RoleModerator})

	can := func(actor, sender uuid.UUID) bool {
		ok, err := svc.CanDeleteMessage(ctx, g.ChatID, actor, sender)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	switch {
	case !can(admin, mod), !can(admin, member):
		t.Fatal("the admin deletes anyone's messages")
	case !can(mod, member):
		t.Fatal("a moderator with delete_messages deletes members' messages")
	case can(mod, admin), can(mod, mod2):
		t.Fatal("moderators can't delete the admin's or other moderators' messages")
	case can(mod2, member), can(member, mod):
		t.Fatal("no permission, no deleting")
	}
	if _, err := svc.RegenerateInvite(ctx, g.ChatID, mod); err != nil {
		t.Fatalf("mod with manage_invite: %v", err)
	}
	if _, err := svc.RegenerateInvite(ctx, g.ChatID, mod2); !errors.Is(err, ErrNoPermission) {
		t.Fatalf("mod without it: %v", err)
	}

	// taking the permission away works at once (the cache is invalidated)
	_ = svc.SetRole(ctx, g.ChatID, admin, mod, roleRequest{Role: RoleMember, Permissions: []string{PermDeleteMessages}})
	if can(mod, member) {
		t.Fatal("a demoted moderator kept delete_messages")
	}
}

func TestAdminLeaving(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()
	admin, a, b := uuid.New(), uuid.New(), uuid.New()
	g, _ := svc.CreateGroup(ctx, admin, createGroupRequest{GroupName: "devs", MaxMems: 10})
	_ = svc.JoinGroup(ctx, g.ChatID, a)
	_ = svc.JoinGroup(ctx, g.ChatID, b)
	if err := svc.LeaveGroup(ctx, g.ChatID, admin, b); err != nil || repo.groups[g.ChatID].AdminID != b {
		t.Fatalf("hand over to b: %v", err)
	}
	gone := false
	svc.OnDelete(func(context.Context, uuid.UUID) { gone = true })
	_ = svc.LeaveGroup(ctx, g.ChatID, a, uuid.Nil)
	if err := svc.LeaveGroup(ctx, g.ChatID, b, uuid.Nil); err != nil || !gone {
		t.Fatalf("last one out deletes the group: %v %v", err, gone)
	}
}

func TestEscapeLike(t *testing.T) {
	if got, want := escapeLike("50%_off"+string(rune(92))), "50"+string(rune(92))+"%"+string(rune(92))+"_off"+string(rune(92))+string(rune(92)); got != want {
		t.Fatalf("escapeLike = %q, want %q", got, want)
	}
}
