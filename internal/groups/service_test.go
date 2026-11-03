package groups

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"RyanDev-21.com/Chirpy/internal/testutil"
	"github.com/google/uuid"
)

// fakeRepo applies the same rules the postgres transactions enforce.
type fakeRepo struct {
	mu     sync.Mutex
	groups map[uuid.UUID]*GroupSnapshot
	codes  map[uuid.UUID]string
	reads  int
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
	for _, g := range f.groups {
		if strings.EqualFold(g.Name, req.GroupName) {
			return ErrDuplicateName
		}
	}
	f.groups[id] = &GroupSnapshot{Name: req.GroupName, MaxMem: req.MaxMems, LeaderID: creator, Members: []uuid.UUID{creator}}
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

func (f *fakeRepo) Leave(_ context.Context, id, user uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[id]
	if !ok {
		return ErrNotFoundGroup
	}
	i := slices.Index(g.Members, user)
	switch {
	case i == -1:
		return ErrNotMember
	case user == g.LeaderID:
		return ErrLeaderCannotLeave
	}
	g.Members = slices.Delete(g.Members, i, i+1)
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

func newSvc(t *testing.T) (GroupService, *fakeRepo) {
	t.Helper()
	rdb, _ := testutil.Redis(t)
	repo := &fakeRepo{groups: map[uuid.UUID]*GroupSnapshot{}, codes: map[uuid.UUID]string{}}
	return NewGroupService(repo, NewStore(rdb), testutil.Logger()), repo
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
	if err := svc.LeaveGroup(ctx, g.ChatID, a); err != nil {
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

	if _, err := svc.CreateGroup(ctx, leader, createGroupRequest{GroupName: "team", MaxMems: 2}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := svc.JoinGroup(ctx, g.ChatID, leader); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("rejoin: %v", err)
	}
	_ = svc.JoinGroup(ctx, g.ChatID, uuid.New())
	if err := svc.JoinGroup(ctx, g.ChatID, uuid.New()); !errors.Is(err, ErrGroupFull) {
		t.Fatalf("full: %v", err)
	}
	if err := svc.LeaveGroup(ctx, g.ChatID, leader); !errors.Is(err, ErrLeaderCannotLeave) {
		t.Fatalf("leader leave: %v", err)
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
	if _, err := svc.RegenerateInvite(ctx, g.ChatID, member); !errors.Is(err, ErrNotLeader) {
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
