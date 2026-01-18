package chat

import (
	"context"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
)


type chatRepo struct{
	queries *database.Queries	
}


type ChatRepo interface{
	AddMessage(payload *database.AddMessageParams)(*database.Message,error)
}


func NewChatRepo(queries *database.Queries)ChatRepo{
	return &chatRepo{
		queries: queries,
	}
}

func (r *chatRepo)AddMessage(payload *database.AddMessageParams)(*database.Message,error){
	ctx,cancel := context.WithTimeout(context.Background(),1*time.Second)	
	defer cancel()
	res, err:= r.queries.AddMessage(ctx,*payload)
	return &res,err	
}


