package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrCacheMiss = errors.New("cache: miss")

type Cache interface {
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// SetJSON serializes v and stores it in cache with ttl.
func SetJSON[T any](c Cache, ctx context.Context, key string, v T, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Set(ctx, key, b, ttl)
}

// GetJSON gets a key and deserializes JSON into out. Returns ErrCacheMiss if not found.
func GetJSON[T any](c Cache, ctx context.Context, key string) (T, error) {
	var zero T
	b, err := c.Get(ctx, key)
	if err != nil {
		return zero, err
	}
	if b == nil {
		return zero, ErrCacheMiss
	}
	if err := json.Unmarshal(b, &zero); err != nil {
		return zero, err
	}
	return zero, nil
}
