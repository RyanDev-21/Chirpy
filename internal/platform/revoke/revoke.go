// Package revoke ends access tokens early. Access tokens are stateless JWTs
// that live for 15 minutes; when a user's sessions must end (password
// change or reset, account deletion), the time is recorded here and tokens
// issued before it are refused.
package revoke

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// localTTL is how long a server trusts its last answer for a user, so a
// busy user costs at most one Redis read per localTTL. Another server's
// revocation can therefore take up to localTTL to apply here.
const localTTL = 5 * time.Second

type entry struct {
	revokedAt time.Time // zero: not revoked
	checked   time.Time
}

// Store keeps revocations in Redis (auth:revoked:{user} = unix milliseconds,
// expiring with the access-token lifetime) with a short local cache.
type Store struct {
	rdb    redis.UniversalClient
	keep   time.Duration
	mu     sync.Mutex
	recent map[uuid.UUID]entry
}

// New keeps revocations for keep, which must be at least the access-token
// lifetime.
func New(rdb redis.UniversalClient, keep time.Duration) *Store {
	return &Store{rdb: rdb, keep: keep, recent: map[uuid.UUID]entry{}}
}

func key(id uuid.UUID) string { return "auth:revoked:" + id.String() }

// RevokeAll refuses every access token of userID issued before now.
func (s *Store) RevokeAll(ctx context.Context, userID uuid.UUID) error {
	now := time.Now().Truncate(time.Millisecond)
	if err := s.rdb.Set(ctx, key(userID), now.UnixMilli(), s.keep).Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.recent[userID] = entry{revokedAt: now, checked: time.Now()}
	s.mu.Unlock()
	return nil
}

// Revoked reports whether a token of userID issued at issuedAt was revoked.
func (s *Store) Revoked(ctx context.Context, userID uuid.UUID, issuedAt time.Time) (bool, error) {
	s.mu.Lock()
	e, ok := s.recent[userID]
	s.mu.Unlock()
	if !ok || time.Since(e.checked) > localTTL {
		v, err := s.rdb.Get(ctx, key(userID)).Result()
		switch {
		case err == redis.Nil:
			e = entry{checked: time.Now()}
		case err != nil:
			return false, err
		default:
			ms, _ := strconv.ParseInt(v, 10, 64)
			e = entry{revokedAt: time.UnixMilli(ms), checked: time.Now()}
		}
		s.mu.Lock()
		if len(s.recent) > 100_000 { // bound memory; entries are cheap to refetch
			clear(s.recent)
		}
		s.recent[userID] = e
		s.mu.Unlock()
	}
	return !e.revokedAt.IsZero() && !issuedAt.After(e.revokedAt), nil
}
