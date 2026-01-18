package chatmodel

import (
	"context"

	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"
)

type ChatRepoCache interface{
	AddMessage(ctx context.Context,key string, values ...interface{})error
}

type chatRepoCache struct{
	cache rediscache.RedisCacheImpl	
}


func NewChatRepoCache(cache rediscache.RedisCacheImpl)ChatRepoCache{
	return &chatRepoCache{
		cache: cache,
	}
}

func (c *chatRepoCache)AddMessage(ctx context.Context,key string,values ...interface{})error{
	err:=c.cache.HSet(ctx,key,values)
	return err
}







