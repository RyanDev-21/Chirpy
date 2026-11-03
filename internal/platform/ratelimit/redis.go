package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// slidingWindowScript approximates a sliding window from the current and
// previous fixed-window counters.
// KEYS: current, previous  ARGV: limit, window_ms, elapsed_ms
// returns {allowed (0/1), remaining}
var slidingWindowScript = redis.NewScript(`
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local elapsed = tonumber(ARGV[3])
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local prev = tonumber(redis.call('GET', KEYS[2]) or '0')
local count = prev * ((window - elapsed) / window) + cur
if count >= limit then
  return {0, 0}
end
redis.call('INCR', KEYS[1])
redis.call('PEXPIRE', KEYS[1], window * 2)
return {1, math.floor(limit - count - 1)}
`)

// Redis is a sliding-window limiter shared by every server instance.
// Keys: rl:{name}:{caller}:{window index}.
type Redis struct {
	rdb    redis.UniversalClient
	limit  int
	window time.Duration
	now    func() time.Time
}

func NewRedis(rdb redis.UniversalClient, limit int, window time.Duration) *Redis {
	return &Redis{rdb: rdb, limit: limit, window: window, now: time.Now}
}

func (l *Redis) Allow(ctx context.Context, key string) (Result, error) {
	nowMs := l.now().UnixMilli()
	winMs := l.window.Milliseconds()
	idx := nowMs / winMs
	res, err := slidingWindowScript.Run(ctx, l.rdb,
		[]string{fmt.Sprintf("rl:%s:%d", key, idx), fmt.Sprintf("rl:%s:%d", key, idx-1)},
		l.limit, winMs, nowMs%winMs,
	).Int64Slice()
	if err != nil {
		return Result{}, err
	}
	return Result{Allowed: res[0] == 1, Remaining: int(res[1]), Limit: l.limit}, nil
}
