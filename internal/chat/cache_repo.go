package chat

import (
	"context"
	"errors"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"
)

type chatCache struct{
	rediscache rediscache.RedisCacheImpl		
}

type ChatRepoCache interface{
	AddMessage(ctx context.Context,key string,payload *chatmodel.MessageMetaData)error
}


func NewChatCache(rediscache rediscache.RedisCacheImpl)ChatRepoCache{
	return &chatCache{
		rediscache: rediscache,
	}
}


func (c *chatCache)AddMessage(ctx context.Context,key string,payload *chatmodel.MessageMetaData)error{
	err:= c.rediscache.HSet(ctx,key,&payload)
	if err !=nil{
		return errors.New("failed to set in the cache")
	}
	return nil
}


