package friends

import (
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/redisx"
	"github.com/redis/go-redis/v9"
)

// Store caches one Snapshot per user:
//
//	user:social:{id}      JSON Snapshot
//	user:social:{id}:ver  version counter (see redisx.Versioned)
type Store = *redisx.Versioned[Snapshot]

const socialTTL = 7 * 24 * time.Hour

func NewStore(rdb redis.UniversalClient) Store {
	return redisx.NewVersioned[Snapshot](rdb, "user:social:v2:", socialTTL) // v2: display names (migration 038)
}
