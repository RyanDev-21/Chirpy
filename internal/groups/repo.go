package groups

import (
	"context"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

type GroupRepo interface {
	createGroup(groupID uuid.UUID, groupInfo createGroupStruct) error
	createGroupLeader(payload creatorPublishStruct) error
	getGroupInfoByName(ctx context.Context, name string) error
	getGroupInfoByID(ctx context.Context, id uuid.UUID) (*database.ChatGroup, error)
	getAllGroupInfo(ctx context.Context) (*[]database.GetAllGroupInfoRow, error)
	getMemsByID(ctx context.Context, groupID uuid.UUID) (*[]uuid.UUID, error)
	getMemberRole(ctx context.Context, groupID, userID uuid.UUID) (string, error)
	addMember(ctx context.Context, payload *database.AddMemberParams) error
	deleteMember(ctx context.Context, payload *database.DeleteMemFromGroupParams) error
}

type groupRepo struct {
	queries *database.Queries
}

func NewGroupRepo(queries *database.Queries) GroupRepo {
	return &groupRepo{queries: queries}
}

func (r *groupRepo) createGroup(groupID uuid.UUID, groupInfo createGroupStruct) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.queries.CreateGroup(ctx, database.CreateGroupParams{
		ID:            groupID,
		Name:          groupInfo.GroupName,
		Description:   groupInfo.Description,
		MaxMember:     groupInfo.MaxMems,
		CurrentMember: groupInfo.CurrentMem,
	})
	return err
}

func (r *groupRepo) createGroupLeader(payload creatorPublishStruct) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := r.queries.CreateGroupLeaderRole(ctx, database.CreateGroupLeaderRoleParams{
		GroupID:  payload.GroupID,
		MemberID: payload.UserID,
		Role:     payload.Role,
	})
	return err
}

func (r *groupRepo) getGroupInfoByName(ctx context.Context, name string) error {
	_, err := r.queries.SearchInfoByName(ctx, name)
	return err
}

func (r *groupRepo) getGroupInfoByID(ctx context.Context, id uuid.UUID) (*database.ChatGroup, error) {
	groupInfo, err := r.queries.GetGroupInfoByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &groupInfo, nil
}

func (r *groupRepo) getAllGroupInfo(ctx context.Context) (*[]database.GetAllGroupInfoRow, error) {
	groupInfo, err := r.queries.GetAllGroupInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &groupInfo, nil
}

func (r *groupRepo) getMemsByID(ctx context.Context, groupID uuid.UUID) (*[]uuid.UUID, error) {
	groupMems, err := r.queries.GetMemberListByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return &groupMems, nil
}

func (r *groupRepo) getMemberRole(ctx context.Context, groupID, userID uuid.UUID) (string, error) {
	row, err := r.queries.GetMemFromGroup(ctx, database.GetMemFromGroupParams{GroupID: groupID, MemberID: userID})
	if err != nil {
		return "", err
	}
	return row.Role, nil
}

func (r *groupRepo) addMember(ctx context.Context, payload *database.AddMemberParams) error {
	return r.queries.AddMember(ctx, *payload)
}

func (r *groupRepo) deleteMember(ctx context.Context, payload *database.DeleteMemFromGroupParams) error {
	return r.queries.DeleteMemFromGroup(ctx, *payload)
}
