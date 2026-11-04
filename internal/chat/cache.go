package chat

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	chatTTL = 7 * 24 * time.Hour
)

func seenKey(chatID string, user uuid.UUID) string {
	return "chat:seen:" + chatID + ":" + user.String()
}

type Cache interface {
	SetSeen(ctx context.Context, chatID string, user, msgID uuid.UUID) error
}

type redisCache struct {
	rdb redis.UniversalClient
}

func NewCache(rdb redis.UniversalClient) Cache { return &redisCache{rdb: rdb} }

func (c *redisCache) SetSeen(ctx context.Context, chatID string, user, msgID uuid.UUID) error {
	return c.rdb.Set(ctx, seenKey(chatID, user), msgID.String(), chatTTL).Err()
}
