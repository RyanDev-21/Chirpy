package groups

import (
	"context"
	"errors"
	"testing"

	"RyanDev-21.com/Chirpy/internal/testutil"
	"github.com/google/uuid"
)

type fakeRepo struct {
	snaps   map[uuid.UUID]GroupSnapshot
	names   map[string]bool
	deleted bool
}

func (f *fakeRepo) CreateWithLeader(context.Context, JobCreate) error { return nil }
func (f *fakeRepo) NameTaken(_ context.Context, n string) (bool, error) {
	return f.names[n], nil
}
func (f *fakeRepo) Snapshot(_ context.Context, id uuid.UUID) (GroupSnapshot, error) {
	s, ok := f.snaps[id]
	if !ok {
		return GroupSnapshot{}, ErrNotFoundGroup
	}
	return s, nil
}
func (f *fakeRepo) AddMember(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeRepo) RemoveMember(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return f.deleted, nil
}

type nopQueue struct{ n int }

func (q *nopQueue) Publish(context.Context, string, any) error { q.n++; return nil }

func newSvc(t *testing.T) (GroupService, *fakeRepo, *nopQueue) {
	t.Helper()
	rdb, _ := testutil.Redis(t)
	repo := &fakeRepo{snaps: map[uuid.UUID]GroupSnapshot{}, names: map[string]bool{}}
	q := &nopQueue{}
	return NewGroupService(repo, NewStore(rdb), q, testutil.Logger()), repo, q
}

func TestCreateJoinLeave(t *testing.T) {
	svc, _, q := newSvc(t)
	ctx := context.Background()
	leader, a, b := uuid.New(), uuid.New(), uuid.New()

	g, err := svc.CreateGroup(ctx, leader, createGroupRequest{GroupName: "devs", MaxMems: 2})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := svc.IsMember(ctx, g.ChatID, leader); !ok {
		t.Fatal("creator must be a member")
	}
	if err := svc.JoinGroup(ctx, g.ChatID, a); err != nil {
		t.Fatal(err)
	}
	if err := svc.JoinGroup(ctx, g.ChatID, a); !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("rejoin: %v", err)
	}
	if err := svc.JoinGroup(ctx, g.ChatID, b); !errors.Is(err, ErrGroupFull) {
		t.Fatalf("full: %v", err)
	}
	if err := svc.LeaveGroup(ctx, g.ChatID, leader); !errors.Is(err, ErrLeaderCannotLeave) {
		t.Fatalf("leader leave: %v", err)
	}
	if err := svc.LeaveGroup(ctx, g.ChatID, b); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member leave: %v", err)
	}
	if err := svc.LeaveGroup(ctx, g.ChatID, a); err != nil {
		t.Fatal(err)
	}
	members, _ := svc.Members(ctx, g.ChatID)
	if len(members) != 1 || members[0] != leader {
		t.Fatalf("members = %v", members)
	}
	if q.n != 3 { // create, join, leave
		t.Fatalf("published %d jobs, want 3", q.n)
	}
}

func TestCreateValidationAndNames(t *testing.T) {
	svc, repo, _ := newSvc(t)
	ctx := context.Background()
	u := uuid.New()

	bad := []createGroupRequest{
		{GroupName: "", MaxMems: 5},
		{GroupName: "   ", MaxMems: 5},
		{GroupName: "this name is far too long for a group", MaxMems: 5},
		{GroupName: "ok", MaxMems: 1},
		{GroupName: "ok", MaxMems: 5000},
	}
	for _, req := range bad {
		if _, err := svc.CreateGroup(ctx, u, req); !errors.Is(err, ErrInvalidGroupReq) {
			t.Errorf("CreateGroup(%+v) err = %v", req, err)
		}
	}

	if _, err := svc.CreateGroup(ctx, u, createGroupRequest{GroupName: "Team", MaxMems: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateGroup(ctx, u, createGroupRequest{GroupName: "team", MaxMems: 5}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	repo.names["inpg"] = true
	if _, err := svc.CreateGroup(ctx, u, createGroupRequest{GroupName: "inpg", MaxMems: 5}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("postgres duplicate: %v", err)
	}
}

func TestLoadsExistingGroupFromPostgres(t *testing.T) {
	svc, repo, _ := newSvc(t)
	ctx := context.Background()
	id, leader, member := uuid.New(), uuid.New(), uuid.New()
	repo.snaps[id] = GroupSnapshot{Name: "old", MaxMem: 3, LeaderID: leader, Members: []uuid.UUID{leader, member}}

	if ok, _ := svc.IsMember(ctx, id, member); !ok {
		t.Fatal("member from postgres not found")
	}
	if err := svc.LeaveGroup(ctx, id, leader); !errors.Is(err, ErrLeaderCannotLeave) {
		t.Fatalf("leader from postgres: %v", err)
	}
	if err := svc.JoinGroup(ctx, uuid.New(), member); !errors.Is(err, ErrNotFoundGroup) {
		t.Fatalf("unknown group: %v", err)
	}
}

func TestRemoveWorkerRetriesWhenNothingDeleted(t *testing.T) {
	svc, repo, _ := newSvc(t)
	if err := svc.HandleRemoveMember(context.Background(), JobMember{}); !errors.Is(err, errMemberNotPersisted) {
		t.Fatalf("err = %v", err)
	}
	repo.deleted = true
	if err := svc.HandleRemoveMember(context.Background(), JobMember{}); err != nil {
		t.Fatal(err)
	}
}
