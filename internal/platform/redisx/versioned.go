package redisx

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Versioned caches one JSON value per key next to a version counter.
//
// Postgres stays the source of truth. Writers commit there and then call
// Invalidate, which bumps the version and drops the value. Readers that
// miss load from postgres and call Put with the version they saw before
// loading; Put is skipped if a write happened in between, so a slow read
// can never put stale data back.
//
// Keys: {prefix}{id} holds the JSON value, {prefix}{id}:ver the counter.
type Versioned[T any] struct {
	rdb    redis.UniversalClient
	prefix string
	ttl    time.Duration
}

func NewVersioned[T any](rdb redis.UniversalClient, prefix string, ttl time.Duration) *Versioned[T] {
	return &Versioned[T]{rdb: rdb, prefix: prefix, ttl: ttl}
}

func (v *Versioned[T]) valueKey(id string) string { return v.prefix + id }
func (v *Versioned[T]) verKey(id string) string   { return v.prefix + id + ":ver" }

// Get returns the cached value; found is false on a miss.
func (v *Versioned[T]) Get(ctx context.Context, id string) (val T, found bool, err error) {
	return GetJSON[T](ctx, v.rdb, v.valueKey(id))
}

// Version returns the current version (0 if never written).
func (v *Versioned[T]) Version(ctx context.Context, id string) (int64, error) {
	n, err := v.rdb.Get(ctx, v.verKey(id)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}

// Put caches val unless the version moved since `version` was read.
func (v *Versioned[T]) Put(ctx context.Context, id string, version int64, val T) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	vk := v.verKey(id)
	err = v.rdb.Watch(ctx, func(tx *redis.Tx) error {
		cur, err := tx.Get(ctx, vk).Int64()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if cur != version {
			return nil // written meanwhile: skip
		}
		_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			p.Set(ctx, v.valueKey(id), raw, v.ttl)
			return nil
		})
		return err
	}, vk)
	if errors.Is(err, redis.TxFailedErr) {
		return nil
	}
	return err
}

// Invalidate bumps the version and drops the cached value for each id.
func (v *Versioned[T]) Invalidate(ctx context.Context, ids ...string) error {
	_, err := v.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		for _, id := range ids {
			p.Incr(ctx, v.verKey(id))
			p.Expire(ctx, v.verKey(id), v.ttl)
			p.Del(ctx, v.valueKey(id))
		}
		return nil
	})
	return err
}

// Load is the read path: return the cached value, or call load, cache the
// result if nothing changed meanwhile, and return it. Cache errors are
// reported through onCacheErr and never fail the read.
func (v *Versioned[T]) Load(ctx context.Context, id string, load func(context.Context) (T, error), onCacheErr func(error)) (T, error) {
	if val, ok, err := v.Get(ctx, id); err == nil && ok {
		return val, nil
	} else if err != nil {
		onCacheErr(err)
	}
	version, verErr := v.Version(ctx, id)
	val, err := load(ctx)
	if err != nil {
		return val, err
	}
	if verErr != nil {
		onCacheErr(verErr)
		return val, nil
	}
	if err := v.Put(ctx, id, version, val); err != nil {
		onCacheErr(err)
	}
	return val, nil
}
