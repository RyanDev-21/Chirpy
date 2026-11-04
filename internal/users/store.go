package users

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const nameTTL = 24 * time.Hour

// Redis keys owned by the users package:
//
//	user:{id}:handle  string  username
//
// ":handle" (was ":name") so names cached before usernames were
// normalised (migration 038) are never read again
func nameKey(id uuid.UUID) string { return "user:" + id.String() + ":handle" }

// Store caches user data in Redis.
type Store interface {
	Name(ctx context.Context, id uuid.UUID) (string, bool, error)
	SetNames(ctx context.Context, users ...PublicUser) error
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

// Forget drops everything cached about a user (after account deletion).
func (s *redisStore) Forget(ctx context.Context, id uuid.UUID) error {
	return s.rdb.Del(ctx, nameKey(id)).Err()
}
