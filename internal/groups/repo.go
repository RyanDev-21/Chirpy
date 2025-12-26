package groups

import (
	"context"
	"fmt"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
)

type GroupRepo interface {
	createChatRecord(ctx context.Context,chatID uuid.UUID,memberIDs []uuid.UUID)error
}

type groupRepo struct {
	queries *database.Queries
}

func NewGroupRepo(queries *database.Queries)GroupRepo{
	return &groupRepo{
		queries: queries,
	}
}


func (r *groupRepo)createChatRecord(ctx context.Context,chatID uuid.UUID,memberIDs []uuid.UUID)error{
	fmt.Println("saved into db")
	return nil
}
