package users

import (
	"context"
	"errors"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/redisx"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	nameTTL   = 24 * time.Hour
	configTTL = 7 * 24 * time.Hour
)

// Redis keys owned by the users package:
//
//	user:{id}:name    string  display name
//	user:{id}:config  string  JSON []ElementCustom
func nameKey(id uuid.UUID) string   { return "user:" + id.String() + ":name" }
func configKey(id uuid.UUID) string { return "user:" + id.String() + ":config" }

// Store caches user data in Redis.
type Store interface {
	Name(ctx context.Context, id uuid.UUID) (string, bool, error)
	SetNames(ctx context.Context, users ...PublicUser) error
	Config(ctx context.Context, id uuid.UUID) ([]ElementCustom, bool, error)
	SetConfig(ctx context.Context, id uuid.UUID, list []ElementCustom) error
	Forget(ctx context.Context, id uuid.UUID) error
}

type redisStore struct {
	rdb redis.UniversalClient
}

func NewStore(rdb redis.UniversalClient) Store {
	return &redisStore{rdb: rdb}
}

func (s *redisStore) Name(ctx context.Context, id uuid.UUID) (string, bool, error) {
	name, err := s.rdb.Get(ctx, nameKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

func (s *redisStore) SetNames(ctx context.Context, users ...PublicUser) error {
	if len(users) == 0 {
		return nil
	}
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, u := range users {
			p.Set(ctx, nameKey(u.ID), u.Name, nameTTL)
		}
		return nil
	})
	return err
}

func (s *redisStore) Config(ctx context.Context, id uuid.UUID) ([]ElementCustom, bool, error) {
	return redisx.GetJSON[[]ElementCustom](ctx, s.rdb, configKey(id))
}

func (s *redisStore) SetConfig(ctx context.Context, id uuid.UUID, list []ElementCustom) error {
	return redisx.SetJSON(ctx, s.rdb, configKey(id), list, configTTL)
}

// Forget drops everything cached about a user (after account deletion).
func (s *redisStore) Forget(ctx context.Context, id uuid.UUID) error {
	return s.rdb.Del(ctx, nameKey(id), configKey(id)).Err()
}
