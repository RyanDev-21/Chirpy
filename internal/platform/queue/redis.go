package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis layout:
//
//	jobs:{topic}   stream  one entry per job: payload, attempt
//	jobs:delayed   zset    retries waiting for their backoff; score = due unix ms
//	jobs:dead      stream  jobs that failed permanently or ran out of retries
const (
	streamPrefix = "jobs:"
	delayedKey   = "jobs:delayed"
	deadKey      = "jobs:dead"
)

type RedisOptions struct {
	Group        string        // consumer group name, shared by all instances
	MaxRetries   int           // attempts before a job goes to jobs:dead
	StreamMaxLen int64         // approximate cap per topic stream
	JobTimeout   time.Duration // per-attempt handler deadline
	ClaimIdle    time.Duration // pending entries idle this long are reclaimed
	BlockFor     time.Duration // XREADGROUP block time
}

func (o *RedisOptions) defaults() {
	if o.Group == "" {
		o.Group = "chirpy"
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = 8
	}
	if o.StreamMaxLen <= 0 {
		o.StreamMaxLen = 100_000
	}
	if o.JobTimeout <= 0 {
		o.JobTimeout = 30 * time.Second
	}
	if o.ClaimIdle <= 0 {
		o.ClaimIdle = time.Minute
	}
	if o.BlockFor <= 0 {
		o.BlockFor = 2 * time.Second
	}
}

// Redis is a durable job queue on Redis Streams with consumer groups.
// Jobs survive restarts, are shared across instances, are retried with
// backoff, and end up in jobs:dead when they cannot succeed.
type Redis struct {
	rdb      redis.UniversalClient
	opts     RedisOptions
	logger   *slog.Logger
	consumer string
	wg       sync.WaitGroup
	moverOn  sync.Once
}

func NewRedis(rdb redis.UniversalClient, opts RedisOptions, logger *slog.Logger) *Redis {
	opts.defaults()
	host, _ := os.Hostname()
	return &Redis{
		rdb:      rdb,
		opts:     opts,
		logger:   logger,
		consumer: fmt.Sprintf("%s-%d", host, os.Getpid()),
	}
}

// envelope is a delayed retry. Payload stays an opaque string so Lua's
// cjson never re-encodes it (it would turn [] into {}).
type envelope struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
	Attempt int    `json:"attempt"`
}

func (q *Redis) Publish(ctx context.Context, topic string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return q.add(ctx, q.rdb, topic, raw, 0)
}

func (q *Redis) add(ctx context.Context, c redis.Cmdable, topic string, payload []byte, attempt int) error {
	return c.XAdd(ctx, &redis.XAddArgs{
		Stream: streamPrefix + topic,
		MaxLen: q.opts.StreamMaxLen,
		Approx: true,
		Values: map[string]any{"payload": payload, "attempt": attempt},
	}).Err()
}

// Subscribe starts one reader for topic that hands jobs to `workers`
// goroutines. Only the reader holds a blocking Redis connection, so the
// pool is never exhausted by idle consumers. They stop when ctx ends.
func (q *Redis) Subscribe(ctx context.Context, topic string, workers int, h Handler) error {
	stream := streamPrefix + topic
	err := q.rdb.XGroupCreateMkStream(ctx, stream, q.opts.Group, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create consumer group for %s: %w", topic, err)
	}
	q.moverOn.Do(func() { q.goLoop(ctx, q.moveDue, 250*time.Millisecond) })

	jobs := make(chan redis.XMessage)
	for range max(workers, 1) {
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			for msg := range jobs {
				q.process(ctx, topic, stream, msg, h)
			}
		}()
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		defer close(jobs)
		q.consume(ctx, topic, stream, max(workers, 1), jobs)
	}()
	return nil
}

// Wait blocks until every consumer has stopped.
func (q *Redis) Wait() { q.wg.Wait() }

func (q *Redis) goLoop(ctx context.Context, fn func(context.Context), every time.Duration) {
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fn(ctx)
			}
		}
	}()
}

// consume reads new entries and, every ClaimIdle/2, entries abandoned by
// crashed consumers, and feeds both to the workers. Sending on the
// unbuffered channel blocks until a worker is free, which is the
// backpressure: at most `batch` entries are read ahead.
func (q *Redis) consume(ctx context.Context, topic, stream string, batch int, jobs chan<- redis.XMessage) {
	consumer := q.consumer + "-" + topic
	nextClaim := time.Now().Add(q.opts.ClaimIdle / 2)
	send := func(msgs []redis.XMessage) bool {
		for _, m := range msgs {
			select {
			case jobs <- m:
			case <-ctx.Done():
				return false // unprocessed entries stay pending and are reclaimed later
			}
		}
		return true
	}
	for ctx.Err() == nil {
		if time.Now().After(nextClaim) {
			nextClaim = time.Now().Add(q.opts.ClaimIdle / 2)
			msgs, _, err := q.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: stream, Group: q.opts.Group, Consumer: consumer,
				MinIdle: q.opts.ClaimIdle, Start: "0", Count: int64(batch),
			}).Result()
			if err != nil && ctx.Err() == nil {
				q.logger.Error("queue reclaim failed", "topic", topic, "error", err)
			}
			if !send(msgs) {
				return
			}
		}
		res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    q.opts.Group,
			Consumer: consumer,
			Streams:  []string{stream, ">"},
			Count:    int64(batch),
			Block:    q.opts.BlockFor,
		}).Result()
		if errors.Is(err, redis.Nil) || ctx.Err() != nil {
			continue
		}
		if err != nil {
			q.logger.Error("queue read failed", "topic", topic, "error", err)
			sleep(ctx, time.Second)
			continue
		}
		for _, st := range res {
			if !send(st.Messages) {
				return
			}
		}
	}
}

func (q *Redis) process(ctx context.Context, topic, stream string, msg redis.XMessage, h Handler) {
	payload, _ := msg.Values["payload"].(string)
	attempt, _ := strconv.Atoi(fmt.Sprint(msg.Values["attempt"]))

	jobCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), q.opts.JobTimeout)
	err := h(jobCtx, json.RawMessage(payload))
	cancel()

	// ack and reschedule/dead-letter in one transaction so a job is never
	// both acknowledged and lost; finish it even while shutting down
	ctx = context.WithoutCancel(ctx)
	_, txErr := q.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		switch {
		case err == nil:
		case IsPermanent(err) || attempt+1 >= q.opts.MaxRetries:
			q.logger.Error("job failed, moved to dead letter", "topic", topic, "attempt", attempt+1, "error", err)
			p.XAdd(ctx, &redis.XAddArgs{
				Stream: deadKey,
				MaxLen: q.opts.StreamMaxLen,
				Approx: true,
				Values: map[string]any{"topic": topic, "payload": payload, "attempt": attempt + 1, "error": err.Error()},
			})
		default:
			q.logger.Warn("job failed, retrying", "topic", topic, "attempt", attempt+1, "error", err)
			env, _ := json.Marshal(envelope{Topic: topic, Payload: payload, Attempt: attempt + 1})
			due := time.Now().Add(Backoff(attempt + 1)).UnixMilli()
			p.ZAdd(ctx, delayedKey, redis.Z{Score: float64(due), Member: env})
		}
		p.XAck(ctx, stream, q.opts.Group, msg.ID)
		p.XDel(ctx, stream, msg.ID)
		return nil
	})
	if txErr != nil {
		// left pending; the reclaimer will retry it
		q.logger.Error("queue ack failed", "topic", topic, "error", txErr)
	}
}

// moveDueScript pops due retries and re-adds them to their topic stream
// atomically, so concurrent instances never move the same job twice.
// KEYS: delayed  ARGV: now, limit, maxlen, prefix
var moveDueScript = redis.NewScript(`
local due = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, ARGV[2])
for _, raw in ipairs(due) do
  redis.call('ZREM', KEYS[1], raw)
  local env = cjson.decode(raw)
  redis.call('XADD', ARGV[4] .. env.topic, 'MAXLEN', '~', ARGV[3], '*',
    'payload', env.payload, 'attempt', env.attempt)
end
return #due
`)

func (q *Redis) moveDue(ctx context.Context) {
	err := moveDueScript.Run(ctx, q.rdb, []string{delayedKey},
		time.Now().UnixMilli(), 100, q.opts.StreamMaxLen, streamPrefix).Err()
	if err != nil && ctx.Err() == nil {
		q.logger.Error("queue delayed move failed", "error", err)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
