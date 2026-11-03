package groups

import (
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/redisx"
	"github.com/redis/go-redis/v9"
)

// Store caches one GroupSnapshot per group:
//
//	group:{id}      JSON GroupSnapshot
//	group:{id}:ver  version counter (see redisx.Versioned)
type Store = *redisx.Versioned[GroupSnapshot]

const groupTTL = 7 * 24 * time.Hour

func NewStore(rdb redis.UniversalClient) Store {
	return redisx.NewVersioned[GroupSnapshot](rdb, "group:", groupTTL)
}
