package groups

import (
	"context"
//	"database/sql"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

type GroupRepo interface {
	createGroup(groupID uuid.UUID,groupInfo createGroupRequest)error
	createGroupLeader(payload creatorPublishStruct)error
	getGroupInfoByName(ctx context.Context,name string)error
getGroupInfoByID(ctx context.Context,id uuid.UUID)(*database.ChatGroup,error)
	//joinGroup(groupID uuid.UUID,userID uuid.UUID)error
	getAllGroupInfo(ctx context.Context)(*[]database.GetAllGroupInfoRow,error)
	getMemsByID(ctx context.Context,groupID uuid.UUID)(*[]uuid.UUID,error)
	addMemberList(ctx context.Context,payload *[]database.AddMemberListParams)error
	addMember(ctx context.Context,payload *database.AddMemberParams)error
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


func (r *groupRepo)getGroupInfoByName(ctx context.Context,name string)error{
	_,err :=r.queries.SearchInfoByName(ctx,name)
	
	if err !=nil{
		return  err
	}
	return nil
}

func (r *groupRepo)getGroupInfoByID(ctx context.Context,id uuid.UUID)(*database.ChatGroup,error){
	groupInfo, err := r.queries.GetGroupInfoByID(ctx,id)
	if err !=nil{
		return nil,	err
	}
	return &groupInfo,nil
}


//this one will return all the member id list too
func (r *groupRepo)getAllGroupInfo(ctx context.Context)(*[]database.GetAllGroupInfoRow,error){
	groupInfo, err := r.queries.GetAllGroupInfo(ctx)
	if err !=nil{
		return nil,err
	}
	return &groupInfo,nil

}


//if i do something like add all the member id to the db like line by line that would really slow me down i need to find a way to make this sure it keep storing the db fast
//func (r *groupRepo)addMember(ctx context.Context,members *)
func(r *groupRepo) getMemsByID(ctx context.Context,groupID uuid.UUID)(*[]uuid.UUID,error){
	groupMems , err := 	r.queries.GetMemberListByID(ctx,groupID)
	if err !=nil{
		return nil,err
	}
	return &groupMems,nil
}


func(r *groupRepo)addMemberList(ctx context.Context,payload *[]database.AddMemberListParams)error{
	_,err := r.queries.AddMemberList(ctx,*payload)
	if err !=nil{
		return err
	}
	return nil
}


func (r *groupRepo)addMember(ctx context.Context,payload *database.AddMemberParams)error{
	err := r.queries.AddMember(ctx,*payload)
	if err !=nil{
		return err
	}
	return nil
}

