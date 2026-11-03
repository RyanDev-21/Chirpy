package friends

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/redisx"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const socialTTL = 7 * 24 * time.Hour

// Redis keys owned by the friends package (per user):
//
//	user:{id}:social    string  marker: relationships were loaded from postgres
//	user:{id}:friends   hash    friendID -> name
//	user:{id}:fr:in     hash    reqID -> JSON FriendMetaData (sender)
//	user:{id}:fr:out    hash    reqID -> JSON FriendMetaData (receiver)
//	user:{id}:fr:peer   hash    otherUserID -> reqID (pending either way)
type keys struct{ marker, friends, in, out, peer string }

func userKeys(id uuid.UUID) keys {
	p := "user:" + id.String()
	return keys{p + ":social", p + ":friends", p + ":fr:in", p + ":fr:out", p + ":fr:peer"}
}

var (
	errScriptExists  = "EXISTS"
	errScriptFriends = "FRIENDS"
	errScriptMissing = "MISSING"
)

// addRequest atomically records a pending request unless one already exists
// between the two users or they are already friends.
// KEYS: from.friends from.out from.peer to.in to.peer
// ARGV: reqID fromID toID fromJSON toJSON
var addRequestScript = redis.NewScript(`
if redis.call('HEXISTS', KEYS[1], ARGV[3]) == 1 then return redis.error_reply('FRIENDS') end
if redis.call('HEXISTS', KEYS[3], ARGV[3]) == 1 then return redis.error_reply('EXISTS') end
redis.call('HSET', KEYS[2], ARGV[1], ARGV[5])
redis.call('HSET', KEYS[3], ARGV[3], ARGV[1])
redis.call('HSET', KEYS[4], ARGV[1], ARGV[4])
redis.call('HSET', KEYS[5], ARGV[2], ARGV[1])
return 1
`)

// resolveRequest removes a pending request and, when accept is "1", makes
// the two users friends.
// KEYS: from.out from.peer from.friends to.in to.peer to.friends
// ARGV: reqID fromID toID fromName toName accept
var resolveRequestScript = redis.NewScript(`
if redis.call('HEXISTS', KEYS[4], ARGV[1]) == 0 then return redis.error_reply('MISSING') end
redis.call('HDEL', KEYS[1], ARGV[1])
redis.call('HDEL', KEYS[2], ARGV[3])
redis.call('HDEL', KEYS[4], ARGV[1])
redis.call('HDEL', KEYS[5], ARGV[2])
if ARGV[6] == '1' then
  redis.call('HSET', KEYS[3], ARGV[3], ARGV[5])
  redis.call('HSET', KEYS[6], ARGV[2], ARGV[4])
end
return 1
`)

// Store keeps relationship state in Redis.
type Store interface {
	Loaded(ctx context.Context, userID uuid.UUID) (bool, error)
	Load(ctx context.Context, userID uuid.UUID, snap Snapshot) error
	Get(ctx context.Context, userID uuid.UUID) (Snapshot, error)
	IsFriend(ctx context.Context, userID, otherID uuid.UUID) (bool, error)
	// Request looks up one pending request of userID; incoming selects
	// requests received rather than sent.
	Request(ctx context.Context, userID, reqID uuid.UUID, incoming bool) (FriendMetaData, bool, error)
	AddRequest(ctx context.Context, reqID uuid.UUID, from, to FriendMetaData) error
	// ResolveRequest removes a pending request from sender `from` to
	// receiver `to`; with accept it also records the friendship.
	ResolveRequest(ctx context.Context, reqID uuid.UUID, from, to FriendMetaData, accept bool) error
}

type redisStore struct {
	rdb redis.UniversalClient
}

func NewStore(rdb redis.UniversalClient) Store { return &redisStore{rdb: rdb} }

func (s *redisStore) Loaded(ctx context.Context, userID uuid.UUID) (bool, error) {
	n, err := s.rdb.Exists(ctx, userKeys(userID).marker).Result()
	return n == 1, err
}

func (s *redisStore) Load(ctx context.Context, userID uuid.UUID, snap Snapshot) error {
	k := userKeys(userID)
	_, err := s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, k.friends, k.in, k.out, k.peer)
		for _, f := range snap.Friends {
			p.HSet(ctx, k.friends, f.UserID.String(), f.Name)
		}
		for reqID, f := range snap.Incoming {
			p.HSet(ctx, k.in, reqID.String(), mustJSON(f))
			p.HSet(ctx, k.peer, f.UserID.String(), reqID.String())
		}
		for reqID, f := range snap.Outgoing {
			p.HSet(ctx, k.out, reqID.String(), mustJSON(f))
			p.HSet(ctx, k.peer, f.UserID.String(), reqID.String())
		}
		p.Set(ctx, k.marker, "1", socialTTL)
		for _, key := range []string{k.friends, k.in, k.out, k.peer} {
			p.Expire(ctx, key, socialTTL)
		}
		return nil
	})
	return err
}

func (s *redisStore) Get(ctx context.Context, userID uuid.UUID) (Snapshot, error) {
	k := userKeys(userID)
	snap := Snapshot{Friends: []FriendMetaData{}, Incoming: map[uuid.UUID]FriendMetaData{}, Outgoing: map[uuid.UUID]FriendMetaData{}}
	var friends, in, out *redis.MapStringStringCmd
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		friends = p.HGetAll(ctx, k.friends)
		in = p.HGetAll(ctx, k.in)
		out = p.HGetAll(ctx, k.out)
		return nil
	})
	if err != nil {
		return snap, err
	}
	for id, name := range friends.Val() {
		if uid, err := uuid.Parse(id); err == nil {
			snap.Friends = append(snap.Friends, FriendMetaData{UserID: uid, Name: name})
		}
	}
	decodeRequests(in.Val(), snap.Incoming)
	decodeRequests(out.Val(), snap.Outgoing)
	return snap, nil
}

func (s *redisStore) IsFriend(ctx context.Context, userID, otherID uuid.UUID) (bool, error) {
	return s.rdb.HExists(ctx, userKeys(userID).friends, otherID.String()).Result()
}

func (s *redisStore) Request(ctx context.Context, userID, reqID uuid.UUID, incoming bool) (FriendMetaData, bool, error) {
	k := userKeys(userID)
	key := k.out
	if incoming {
		key = k.in
	}
	raw, err := s.rdb.HGet(ctx, key, reqID.String()).Result()
	if errors.Is(err, redis.Nil) {
		return FriendMetaData{}, false, nil
	}
	if err != nil {
		return FriendMetaData{}, false, err
	}
	var f FriendMetaData
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return FriendMetaData{}, false, err
	}
	return f, true, nil
}

func (s *redisStore) AddRequest(ctx context.Context, reqID uuid.UUID, from, to FriendMetaData) error {
	fk, tk := userKeys(from.UserID), userKeys(to.UserID)
	err := addRequestScript.Run(ctx, s.rdb,
		[]string{fk.friends, fk.out, fk.peer, tk.in, tk.peer},
		reqID.String(), from.UserID.String(), to.UserID.String(), mustJSON(from), mustJSON(to),
	).Err()
	return mapScriptErr(err)
}

func (s *redisStore) ResolveRequest(ctx context.Context, reqID uuid.UUID, from, to FriendMetaData, accept bool) error {
	fk, tk := userKeys(from.UserID), userKeys(to.UserID)
	flag := "0"
	if accept {
		flag = "1"
	}
	err := resolveRequestScript.Run(ctx, s.rdb,
		[]string{fk.out, fk.peer, fk.friends, tk.in, tk.peer, tk.friends},
		reqID.String(), from.UserID.String(), to.UserID.String(), from.Name, to.Name, flag,
	).Err()
	return mapScriptErr(err)
}

func mapScriptErr(err error) error {
	if err == nil {
		return nil
	}
	switch redisx.ScriptErrCode(err) {
	case errScriptExists:
		return ErrRequestExists
	case errScriptFriends:
		return ErrAlreadyFriends
	case errScriptMissing:
		return ErrNotFound
	}
	return err
}

func decodeRequests(raw map[string]string, dst map[uuid.UUID]FriendMetaData) {
	for id, v := range raw {
		reqID, err := uuid.Parse(id)
		if err != nil {
			continue
		}
		var f FriendMetaData
		if json.Unmarshal([]byte(v), &f) == nil {
			dst[reqID] = f
		}
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(errors.Join(errors.New("friends: marshal"), err))
	}
	return string(b)
}
