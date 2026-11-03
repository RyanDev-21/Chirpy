package groups

import (
	"context"
	"strconv"
	"strings"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/redisx"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const groupTTL = 7 * 24 * time.Hour

// Redis keys owned by the groups package:
//
//	group:{id}:loaded   string  marker: group was loaded from postgres
//	group:{id}:meta     hash    name, max, leader
//	group:{id}:members  set     member ids
//	group:name:{name}   string  group id holding this (lower-cased) name
type gkeys struct{ loaded, meta, members string }

func groupKeys(id uuid.UUID) gkeys {
	p := "group:" + id.String()
	return gkeys{p + ":loaded", p + ":meta", p + ":members"}
}

func nameKey(name string) string { return "group:name:" + strings.ToLower(name) }

// KEYS: meta members  ARGV: userID
var joinScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return redis.error_reply('NOGROUP') end
if redis.call('SISMEMBER', KEYS[2], ARGV[1]) == 1 then return redis.error_reply('MEMBER') end
local max = tonumber(redis.call('HGET', KEYS[1], 'max'))
if redis.call('SCARD', KEYS[2]) >= max then return redis.error_reply('FULL') end
redis.call('SADD', KEYS[2], ARGV[1])
return 1
`)

// KEYS: meta members  ARGV: userID
var leaveScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return redis.error_reply('NOGROUP') end
if redis.call('HGET', KEYS[1], 'leader') == ARGV[1] then return redis.error_reply('LEADER') end
if redis.call('SREM', KEYS[2], ARGV[1]) == 0 then return redis.error_reply('NOTMEMBER') end
return 1
`)

// Store keeps group metadata and membership in Redis.
type Store interface {
	Loaded(ctx context.Context, groupID uuid.UUID) (bool, error)
	Load(ctx context.Context, groupID uuid.UUID, snap GroupSnapshot) error
	// ReserveName claims name for groupID; false if another group has it.
	ReserveName(ctx context.Context, name string, groupID uuid.UUID) (bool, error)
	ReleaseName(ctx context.Context, name string) error
	Join(ctx context.Context, groupID, userID uuid.UUID) error
	Leave(ctx context.Context, groupID, userID uuid.UUID) error
	IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error)
	Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error)
}

type redisStore struct {
	rdb redis.UniversalClient
}

func NewStore(rdb redis.UniversalClient) Store { return &redisStore{rdb: rdb} }

func (s *redisStore) Loaded(ctx context.Context, groupID uuid.UUID) (bool, error) {
	n, err := s.rdb.Exists(ctx, groupKeys(groupID).loaded).Result()
	return n == 1, err
}

func (s *redisStore) Load(ctx context.Context, groupID uuid.UUID, snap GroupSnapshot) error {
	k := groupKeys(groupID)
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, k.meta, k.members)
		p.HSet(ctx, k.meta, "name", snap.Name, "max", strconv.Itoa(int(snap.MaxMem)), "leader", snap.LeaderID.String())
		if len(snap.Members) > 0 {
			ids := make([]any, len(snap.Members))
			for i, m := range snap.Members {
				ids[i] = m.String()
			}
			p.SAdd(ctx, k.members, ids...)
		}
		p.Set(ctx, k.loaded, "1", groupTTL)
		p.Expire(ctx, k.meta, groupTTL)
		p.Expire(ctx, k.members, groupTTL)
		return nil
	})
	return err
}

func (s *redisStore) ReserveName(ctx context.Context, name string, groupID uuid.UUID) (bool, error) {
	return s.rdb.SetNX(ctx, nameKey(name), groupID.String(), groupTTL).Result()
}

func (s *redisStore) ReleaseName(ctx context.Context, name string) error {
	return s.rdb.Del(ctx, nameKey(name)).Err()
}

func (s *redisStore) Join(ctx context.Context, groupID, userID uuid.UUID) error {
	k := groupKeys(groupID)
	return mapErr(joinScript.Run(ctx, s.rdb, []string{k.meta, k.members}, userID.String()).Err())
}

func (s *redisStore) Leave(ctx context.Context, groupID, userID uuid.UUID) error {
	k := groupKeys(groupID)
	return mapErr(leaveScript.Run(ctx, s.rdb, []string{k.meta, k.members}, userID.String()).Err())
}

func (s *redisStore) IsMember(ctx context.Context, groupID, userID uuid.UUID) (bool, error) {
	return s.rdb.SIsMember(ctx, groupKeys(groupID).members, userID.String()).Result()
}

func (s *redisStore) Members(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	raw, err := s.rdb.SMembers(ctx, groupKeys(groupID).members).Result()
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(raw))
	for _, v := range raw {
		if id, err := uuid.Parse(v); err == nil {
			out = append(out, id)
		}
	}
	return out, nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	switch redisx.ScriptErrCode(err) {
	case "NOGROUP":
		return ErrNotFoundGroup
	case "MEMBER":
		return ErrAlreadyMember
	case "FULL":
		return ErrGroupFull
	case "LEADER":
		return ErrLeaderCannotLeave
	case "NOTMEMBER":
		return ErrNotMember
	}
	return err
}
