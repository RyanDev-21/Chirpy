// Package redisx creates the shared Redis client and small helpers used by
// the per-domain Redis stores.
package redisx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Options struct {
	URL      string // redis:// or rediss://; overrides Addr/Username/Password/DB
	Addr     string
	Username string
	Password string
	DB       int
	PoolSize int // 0: go-redis default (10 per CPU)
}

// NewClient connects and pings Redis.
func NewClient(ctx context.Context, o Options) (*redis.Client, error) {
	opts := &redis.Options{Addr: o.Addr, Username: o.Username, Password: o.Password, DB: o.DB}
	if o.URL != "" {
		parsed, err := redis.ParseURL(o.URL)
		if err != nil {
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
		opts = parsed
	}
	opts.PoolSize = o.PoolSize
	rdb := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping %s: %w", opts.Addr, err)
	}
	return rdb, nil
}

// GetJSON loads a JSON value. found is false on a cache miss.
func GetJSON[T any](ctx context.Context, rdb redis.Cmdable, key string) (v T, found bool, err error) {
	raw, err := rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, false, err
	}
	return v, true, nil
}

// SetJSON stores v as JSON with a TTL.
func SetJSON(ctx context.Context, rdb redis.Cmdable, key string, v any, ttl time.Duration) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, key, raw, ttl).Err()
}

// ScriptErrCode returns the code from a Lua redis.error_reply('CODE'),
// which Redis delivers as "ERR CODE". Other errors return "".
func ScriptErrCode(err error) string {
	var rerr redis.Error
	if err == nil || !errors.As(err, &rerr) {
		return ""
	}
	msg := rerr.Error()
	if len(msg) > 4 && msg[:4] == "ERR " {
		return msg[4:]
	}
	return msg
}
