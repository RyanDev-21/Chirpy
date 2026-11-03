package friends

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const socialTTL = 7 * 24 * time.Hour

// Redis keys owned by the friends package (per user):
//
//	user:{id}:social:ver  counter  bumped on every write to this user's relationships
//	user:{id}:social      string   JSON Snapshot cached from postgres
//
// Postgres is the source of truth. A write commits there first and then
// calls Invalidate; a reader only caches what it loaded if the version did
// not move while it was reading, so a slow read cannot put stale data back.
func socialKey(id uuid.UUID) string { return "user:" + id.String() + ":social" }
func versionKey(id uuid.UUID) string {
	return "user:" + id.String() + ":social:ver"
}

type Store interface {
	// Get returns the cached snapshot; found is false on a miss.
	Get(ctx context.Context, userID uuid.UUID) (snap Snapshot, found bool, err error)
	Version(ctx context.Context, userID uuid.UUID) (int64, error)
	// Put caches snap unless the version changed since `version` was read.
	Put(ctx context.Context, userID uuid.UUID, version int64, snap Snapshot) error
	Invalidate(ctx context.Context, userIDs ...uuid.UUID) error
}

type redisStore struct {
	rdb redis.UniversalClient
}

func NewStore(rdb redis.UniversalClient) Store { return &redisStore{rdb: rdb} }

func (s *redisStore) Get(ctx context.Context, userID uuid.UUID) (Snapshot, bool, error) {
	raw, err := s.rdb.Get(ctx, socialKey(userID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return Snapshot{}, false, err
	}
	return snap, true, nil
}

func (s *redisStore) Version(ctx context.Context, userID uuid.UUID) (int64, error) {
	v, err := s.rdb.Get(ctx, versionKey(userID)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

func (s *redisStore) Put(ctx context.Context, userID uuid.UUID, version int64, snap Snapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	vk := versionKey(userID)
	err = s.rdb.Watch(ctx, func(tx *redis.Tx) error {
		cur, err := tx.Get(ctx, vk).Int64()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if cur != version {
			return nil // a write happened while we were reading: do not cache
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, socialKey(userID), raw, socialTTL)
			return nil
		})
		return err
	}, vk)
	if errors.Is(err, redis.TxFailedErr) {
		return nil
	}
	return err
}

func (s *redisStore) Invalidate(ctx context.Context, userIDs ...uuid.UUID) error {
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, id := range userIDs {
			p.Incr(ctx, versionKey(id))
			p.Expire(ctx, versionKey(id), socialTTL)
			p.Del(ctx, socialKey(id))
		}
		return nil
	})
	return err
}
