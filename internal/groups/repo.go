package groups

import (
	"context"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

type GroupRepo interface {
	createGroup(groupID uuid.UUID,groupInfo createGroupRequest)error
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
