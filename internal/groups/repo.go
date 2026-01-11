package groups

import (
	"context"
	"database/sql"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

type GroupRepo interface {
	createGroup(groupID uuid.UUID,groupInfo createGroupRequest)error
	createGroupLeader(payload creatorPublishStruct)error
	getGroupInfoByName(ctx context.Context,name string)(bool,error)
getGroupInfoByID(ctx context.Context,id uuid.UUID)(*database.ChatGroup,error)
	//joinGroup(groupID uuid.UUID,userID uuid.UUID)error
	getAllGroupInfo(ctx context.Context)(*[]database.GetAllGroupInfoRow,error)
}

type groupRepo struct {
	queries *database.Queries
}

func NewGroupRepo(queries *database.Queries)GroupRepo{
	return &groupRepo{
		queries: queries,
	}
}


// func (r *groupRepo)createChatRecord(ctx context.Context,chatID uuid.UUID,groupInfo *createGroupRequest)error{
// 	fmt.Println("saved into db")
// 	return nil
// }
func (r *groupRepo)createGroup(groupID uuid.UUID,groupInfo createGroupRequest)error{
	context,cancel := context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	_,err := r.queries.CreateGroup(context,database.CreateGroupParams{
		ID: groupID,
		Name: groupInfo.GroupName,
		Description: groupInfo.Description,
		MaxMember: groupInfo.MaxMems,
	})
	if err !=nil{
		return err
	}

	return nil
}

func (r *groupRepo)createGroupLeader(payload creatorPublishStruct)error{
	context,cancel := context.WithTimeout(context.Background(),5*time.Second)
	defer cancel()
	_,err := r.queries.CreateGroupLeaderRole(context,database.CreateGroupLeaderRoleParams{
		GroupID: payload.GroupID,
		MemberID: payload.UserID,
		Role: payload.Role,
	})
	if err !=nil{
		return err
	}
	return nil
}


func (r *groupRepo)getGroupInfoByName(ctx context.Context,name string)(bool,error){
	_,err :=r.queries.SearchInfoByName(ctx,name)

	if err == sql.ErrNoRows{
		return true,nil
	}
	return false, err
}

func (r *groupRepo)getGroupInfoByID(ctx context.Context,id uuid.UUID)(*database.ChatGroup,error){
	groupInfo, err := r.queries.GetGroupInfoByID(ctx,id)
	if err !=nil{
		return nil,	err
	}
	return &groupInfo,nil
}

func (r *groupRepo)getAllGroupInfo(ctx context.Context)(*[]database.GetAllGroupInfoRow,error){
	groupInfo, err := r.queries.GetAllGroupInfo(ctx)
	if err !=nil{
		return nil,err
	}
	return &groupInfo,nil

}

