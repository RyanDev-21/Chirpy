package chat

import (
	"context"
	//	"errors"
	"fmt"
	"log"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"
	"github.com/google/uuid"
)

type chatCache struct {
	rediscache rediscache.RedisCacheImpl
}

type ChatRepoCache interface {
	addMessage(ctx context.Context, key string, payload *chatmodel.MessageMetaData) error
	generateRedisKey(userID uuid.UUID, chatID string) string
	getMessages(ctx context.Context, key string) (*[]chatmodel.MessageMetaDataRes, error)
}

func NewChatCache(rediscache rediscache.RedisCacheImpl) ChatRepoCache {
	return &chatCache{
		rediscache: rediscache,
	}
}

func (c *chatCache) generateRedisKey(userID uuid.UUID, chatID string) string {
	return fmt.Sprintf("%v:%v", userID, chatID)
}

func (c *chatCache) addMessage(ctx context.Context, key string, payload *chatmodel.MessageMetaData) error {
	payloadBytes, err := marshallBinary(payload.MsgInfo)
	if err != nil {
		log.Printf("failed to marshal into binary #%s#", err)
		return err
	}
	err = c.rediscache.XAdd(ctx, key, payload.ID.String(), payloadBytes)
	if err != nil {
		log.Printf("failed to set in the cache #%s#", err)
		return err
	}
	return nil
}

func (c *chatCache) getMessages(ctx context.Context, key string) (*[]chatmodel.MessageMetaDataRes, error) {
	var messageList []chatmodel.MessageMetaDataRes
	list, err := c.rediscache.XRangeN(ctx, key)
	if err != nil {
		return nil, err
	}
	for _, v := range list {
		for k, v := range v.Values {
			payload, err := unmarshalBinary(v.([]byte))
			if err != nil {
				return nil, err
			}
			messageList = append(messageList, chatmodel.MessageMetaDataRes{
				ID:      uuid.MustParse(k),
				MsgInfo: *payload,
			})
		}
	}
	return &messageList, nil
}
