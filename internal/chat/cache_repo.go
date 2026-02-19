package chat

import (
	"context"
	//	"errors"
	"fmt"
	"log/slog"

	chatmodel "RyanDev-21.com/Chirpy/internal/chatModel"
	rediscache "RyanDev-21.com/Chirpy/internal/redisCache"
	"RyanDev-21.com/Chirpy/pkg/encoder"
	"github.com/google/uuid"
)

type chatCache struct {
	rediscache rediscache.RedisCacheImpl
	chatRepo   ChatRepo
	logger     *slog.Logger
}

type ChatRepoCache interface {
	addMessage(ctx context.Context, key string, payload *chatmodel.MessageMetaData) error
	generateRedisKey(userID uuid.UUID, chatID string) string
	getMessages(ctx context.Context, key string) (*[]chatmodel.MessageMetaDataRes, error)
	LoadMessagesForStartUp(ctx context.Context) error
}

func NewChatCache(rediscache rediscache.RedisCacheImpl, chatRepo ChatRepo, logger *slog.Logger) ChatRepoCache {
	return &chatCache{
		rediscache: rediscache,
		chatRepo:   chatRepo,
		logger:     logger,
	}
}

func (c *chatCache) generateRedisKey(userID uuid.UUID, chatID string) string {
	return fmt.Sprintf("%v:%v", userID, chatID)
}

func (c *chatCache) LoadMessagesForStartUp(ctx context.Context) error {
	privateMsgs, err := c.chatRepo.GetAllPrivateMessages(ctx)
	if err != nil {
		return err
	}
	for _, v := range *privateMsgs {

		// i don't like this convert stuff
		userID, err := encoder.ConvertToUUID(v.FromID.Bytes[:])
		if err != nil {
			return err
		}
		toID, err := encoder.ConvertToUUID(v.ToID.Bytes[:])
		if err != nil {
			return err
		}
		chatID := getChatKey(userID, toID)
		key := c.generateRedisKey(userID, chatID)
		err = c.addMessage(ctx, key, convertFromMessageToMeta(v))
		if err != nil {
			return err
		}
	}

	publicMsgs, err := c.chatRepo.GetAllPublicMessages(ctx)
	if err != nil {
		return err
	}

	for _, v := range *publicMsgs {
		userID, err := encoder.ConvertToUUID(v.FromID.Bytes[:])
		if err != nil {
			return err
		}
		key := c.generateRedisKey(userID, v.GroupID.String())
		err = c.addMessage(ctx, key, convertFromGroupMessageToMeta(v))
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *chatCache) addMessage(ctx context.Context, key string, payload *chatmodel.MessageMetaData) error {
	payloadBytes, err := marshallBinary(payload.MsgInfo)
	if err != nil {
		c.logger.Error("failed to marshal message into binary", "err", err)
		return err
	}
	err = c.rediscache.XAdd(ctx, key, payload.ID.String(), payloadBytes)
	if err != nil {
		c.logger.Error("failed to add message to cache", "err", err, "key", key)
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
		msgIDStr, ok := v.Values["message_id"].(string)
		if !ok {
			continue
		}
		payloadStr, ok := v.Values["payload"].(string)
		if !ok {
			continue
		}
		payload, err := unmarshalBinary([]byte(payloadStr))
		if err != nil {
			return nil, err
		}
		messageList = append(messageList, chatmodel.MessageMetaDataRes{
			ID:      uuid.MustParse(msgIDStr),
			MsgInfo: *payload,
		})
	}
	return &messageList, nil
}
