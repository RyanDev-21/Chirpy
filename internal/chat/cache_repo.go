package chat

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/redisx"
	"github.com/google/uuid"
)

type chatCache struct {
	rediscache redisx.RedisCacheImpl
	logger     *slog.Logger
}

type ChatRepoCache interface {
	addMessage(ctx context.Context, key string, payload *MessageMetaData) error
	generateRedisKey(chatID string, chatType string) string
	generateRedisKeyForSeen(chatID string, userID uuid.UUID) string
	// getMessages returns the newest cached messages in chronological order.
	// When since is non-zero only messages created after it are returned.
	getMessages(ctx context.Context, key string, since time.Time) ([]MessageMetaDataRes, error)
	UpdateLastSeen(ctx context.Context, key string, val uuid.UUID) error
}

func NewChatCache(rediscache redisx.RedisCacheImpl, logger *slog.Logger) ChatRepoCache {
	return &chatCache{
		rediscache: rediscache,
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

func (c *chatCache) addMessage(ctx context.Context, key string, payload *MessageMetaData) error {
	payloadBytes, err := marshallBinary(payload.MsgInfo)
	if err != nil {
		c.logger.Error("failed to marshal message into binary", "err", err)
		return err
	}
	if err := c.rediscache.XAdd(ctx, key, payload.ID.String(), payloadBytes); err != nil {
		c.logger.Error("failed to add message to cache", "err", err, "key", key)
		return err
	}
	return nil
}

func (c *chatCache) UpdateLastSeen(ctx context.Context, key string, val uuid.UUID) error {
	if err := c.rediscache.Set(ctx, key, val.String()); err != nil {
		c.logger.Error("failed to add last seen to cache", "err", err, "key", key)
		return err
	}
	return nil
}

func (c *chatCache) getMessages(ctx context.Context, key string, since time.Time) ([]MessageMetaDataRes, error) {
	list, err := c.rediscache.XRevRangeN(ctx, key)
	if err != nil {
		return nil, err
	}
	messageList := make([]MessageMetaDataRes, 0, len(list))
	for _, v := range list {
		msgIDStr, ok := v.Values["message_id"].(string)
		if !ok {
			continue
		}
		payloadStr, ok := v.Values["payload"].(string)
		if !ok {
			continue
		}
		msgID, err := uuid.Parse(msgIDStr)
		if err != nil {
			continue
		}
		payload, err := unmarshalBinary([]byte(payloadStr))
		if err != nil {
			return nil, err
		}
		if !since.IsZero() && !payload.CreatedAt.After(since) {
			continue
		}
		messageList = append(messageList, MessageMetaDataRes{
			ID:        msgID,
			MsgInfo:   *payload,
			CreatedAt: payload.CreatedAt,
		})
	}
	// XREVRANGE returns newest first; clients expect chronological order.
	slices.Reverse(messageList)
	return messageList, nil
}
