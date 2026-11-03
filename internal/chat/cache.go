package chat

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// streamCap is how many recent messages each chat keeps in redis.
	streamCap = 200
	pageSize  = 50
	chatTTL   = 7 * 24 * time.Hour
)

// Redis keys owned by the chat package:
//
//	chat:dm:{a_b}:msgs / chat:group:{id}:msgs     stream  recent messages (JSON)
//	chat:dm:{a_b}:loaded / chat:group:{id}:loaded string  marker: stream seeded from postgres
//	chat:seen:{chatID}:{userID}                   string  last seen message id
type chatKey string

func privateKey(a, b uuid.UUID) chatKey { return chatKey("chat:dm:" + PrivateChatID(a, b)) }
func groupKey(id uuid.UUID) chatKey     { return chatKey("chat:group:" + id.String()) }
func (k chatKey) msgs() string          { return string(k) + ":msgs" }
func (k chatKey) loaded() string        { return string(k) + ":loaded" }
func seenKey(chatID string, user uuid.UUID) string {
	return "chat:seen:" + chatID + ":" + user.String()
}

type Cache interface {
	Loaded(ctx context.Context, k chatKey) (bool, error)
	// Seed replaces the stream with msgs (chronological) and marks it loaded.
	Seed(ctx context.Context, k chatKey, msgs []MessageMetaDataRes) error
	Append(ctx context.Context, k chatKey, m MessageMetaDataRes) error
	// Recent returns up to n newest messages in chronological order.
	Recent(ctx context.Context, k chatKey, n int) ([]MessageMetaDataRes, error)
	SetSeen(ctx context.Context, chatID string, user, msgID uuid.UUID) error
	// Invalidate drops the stream so the next read reseeds it from postgres.
	Invalidate(ctx context.Context, k chatKey) error
}

type redisCache struct {
	rdb redis.UniversalClient
}

func NewCache(rdb redis.UniversalClient) Cache { return &redisCache{rdb: rdb} }

func xadd(ctx context.Context, p redis.Cmdable, stream string, m MessageMetaDataRes) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return p.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		MaxLen: streamCap,
		Approx: true,
		Values: map[string]any{"id": m.ID.String(), "m": raw},
	}).Err()
}

func (c *redisCache) Loaded(ctx context.Context, k chatKey) (bool, error) {
	n, err := c.rdb.Exists(ctx, k.loaded()).Result()
	return n == 1, err
}

func (c *redisCache) Seed(ctx context.Context, k chatKey, msgs []MessageMetaDataRes) error {
	_, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, k.msgs())
		for _, m := range msgs {
			if err := xadd(ctx, p, k.msgs(), m); err != nil {
				return err
			}
		}
		p.Set(ctx, k.loaded(), "1", chatTTL)
		p.Expire(ctx, k.msgs(), chatTTL)
		return nil
	})
	return err
}

func (c *redisCache) Append(ctx context.Context, k chatKey, m MessageMetaDataRes) error {
	_, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		if err := xadd(ctx, p, k.msgs(), m); err != nil {
			return err
		}
		p.Expire(ctx, k.msgs(), chatTTL)
		p.Expire(ctx, k.loaded(), chatTTL)
		return nil
	})
	return err
}

func (c *redisCache) Recent(ctx context.Context, k chatKey, n int) ([]MessageMetaDataRes, error) {
	entries, err := c.rdb.XRevRangeN(ctx, k.msgs(), "+", "-", int64(n)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]MessageMetaDataRes, 0, len(entries))
	for _, e := range entries {
		raw, ok := e.Values["m"].(string)
		if !ok {
			continue
		}
		var m MessageMetaDataRes
		if json.Unmarshal([]byte(raw), &m) == nil {
			out = append(out, m)
		}
	}
	slices.Reverse(out)
	return out, nil
}

func (c *redisCache) SetSeen(ctx context.Context, chatID string, user, msgID uuid.UUID) error {
	return c.rdb.Set(ctx, seenKey(chatID, user), msgID.String(), chatTTL).Err()
}

func (c *redisCache) Invalidate(ctx context.Context, k chatKey) error {
	return c.rdb.Del(ctx, k.loaded(), k.msgs()).Err()
}
