package chat

import (
	"context"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
//	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"
)


type chatRepo struct{
	queries *database.Queries	
}


type ChatRepo interface{
	AddMessagePrivate(payload *database.AddMessagePrivateParams)(*database.Message,error)
	AddMessagePublic(payload *database.AddMessagePublicParams)(*database.Groupmessage,error)
}


func NewChatRepo(queries *database.Queries)ChatRepo{
	return &chatRepo{
		queries: queries,
	}
}

func (r *chatRepo)AddMessagePrivate(payload *database.AddMessagePrivateParams)(*database.Message,error){
	ctx,cancel := context.WithTimeout(context.Background(),1*time.Second)	
	defer cancel()
	res, err:= r.queries.AddMessagePrivate(ctx,*payload)
	return &res,err	
}

func (r *chatRepo)AddMessagePublic(payload *database.AddMessagePublicParams)(*database.Groupmessage,error){
	ctx,cancel := context.WithTimeout(context.Background(),1*time.Second)
	defer cancel()
	res,err :=r.queries.AddMessagePublic(ctx,*payload)
	return &res,err
}


