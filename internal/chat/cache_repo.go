package chat

import (
	"context"
	"errors"
	"fmt"
	"log"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"
	"github.com/google/uuid"
)

type chatCache struct{
	rediscache rediscache.RedisCacheImpl		
}

type ChatRepoCache interface{
	addMessage(ctx context.Context,key string,payload *chatmodel.MessageMetaData)error
	getChatKey(firstID,secondID uuid.UUID)string		
//	getMessages(ctx context.Context,key string)([]*chatmodel.MessageMetaData,error)
}


func NewChatCache(rediscache rediscache.RedisCacheImpl)ChatRepoCache{
	return &chatCache{
		rediscache: rediscache,
	}
}

func (c *chatCache)getChatKey(firstID,secondID uuid.UUID)string{
	if firstID.String()<secondID.String(){
		return fmt.Sprintf("%v_%v",firstID,secondID)
	}	
	return fmt.Sprintf("%v_%v",secondID,firstID)
}

func (c *chatCache)addMessage(ctx context.Context,key string,payload *chatmodel.MessageMetaData)error{
	payloadBytes,err:= marshallBinary(payload)
		if err !=nil{
			log.Printf("failed to marshal into binary #%s#",err)
		return err
	}
	err= c.rediscache.XAdd(ctx,key,payloadBytes)
	if err !=nil{
		log.Printf("failed to set in the cache #%s#",err)
		return errors.New("failed to set in the cache")
	}
	return nil
}

// func (c *chatCache)getMessages(ctx context.Context,key string)([]*chatmodel.MessageMetaData,error){
//
// }
