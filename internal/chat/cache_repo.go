package chat

import (
	"context"
	"time"
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
	generateRedisKey(chatID string, chatType string) string
	generateRedisKeyForSeen(chatID string, userID uuid.UUID) string
	getMessages(ctx context.Context, key string) (*[]chatmodel.MessageMetaDataRes, error)

	getMessagesWithTime(ctx context.Context, key string,since time.Time) (*[]chatmodel.MessageMetaDataRes, error)
	UpdateLastSeen(ctx context.Context, key string, val uuid.UUID) error
	LoadMessagesForStartUp() error
}

func NewChatCache(rediscache rediscache.RedisCacheImpl, chatRepo ChatRepo, logger *slog.Logger) ChatRepoCache {
	return &chatCache{
		rediscache: rediscache,
		chatRepo:   chatRepo,
		logger:     logger,
	}
}

func (c *chatCache) generateRedisKey(chatID string, chatType string) string {
	switch chatType {
	case "private":
		return fmt.Sprintf("chat:%v", chatID)
	case "public":
		return fmt.Sprintf("groups:%v", chatID)
	default:
		return ""
	}
}

func (c *chatCache) generateRedisKeyForSeen(chatID string, userID uuid.UUID) string {
	return fmt.Sprintf("seen:%v:%v", chatID, userID)
}
func (c *chatCache) LoadMessagesForStartUp() error {
	privateCtx,cancel := context.WithTimeout(context.Background(),3*time.Second)
	defer cancel()
	privateMsgs, err := c.chatRepo.getAllPrivateMessages(privateCtx)
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
		key := c.generateRedisKey(chatID, "private")
		redisCtx,cancel:= context.WithTimeout(context.Background(),3*time.Second)
		err = c.addMessage(redisCtx, key, convertFromMessageToMeta(v))
		if err != nil {
			return err
		}
		cancel()
	}

	publicCtx,cancel := context.WithTimeout(context.Background(),3*time.Second)
	publicMsgs, err := c.chatRepo.getAllPublicMessages(publicCtx)
	if err != nil {
		cancel()
		return err
	}
	cancel()

	for _, v := range *publicMsgs {
		key := c.generateRedisKey(v.GroupID.String(), "public")
		groupCtx,cancel:= context.WithTimeout(context.Background(),3*time.Second)
		err = c.addMessage(groupCtx, key, convertFromGroupMessageToMeta(v))
		if err != nil {
			cancel()
			return err
		}
		cancel()
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
func (c *chatCache) UpdateLastSeen(ctx context.Context, key string, val uuid.UUID) error {
	err := c.rediscache.Set(ctx, key, val)
	if err != nil {
		c.logger.Error("failed to add last seen to cache", "err", err, "key", key)
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
			ID:        uuid.MustParse(msgIDStr),
			MsgInfo:   *payload,
			CreatedAt: payload.CreatedAt, 
		})
	}
	return &messageList, nil
}  

//right now it is duplicating 

func (c *chatCache) getMessagesWithTime(ctx context.Context, key string,since time.Time) (*[]chatmodel.MessageMetaDataRes, error) {
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
		if payload.CreatedAt.After(since){
			messageList = append(messageList, chatmodel.MessageMetaDataRes{
				ID:        uuid.MustParse(msgIDStr),
				MsgInfo:   *payload,
				CreatedAt: payload.CreatedAt, 
			})
		}

	}
	return &messageList, nil
}  




