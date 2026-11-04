package groups

import (
	"time"

	"RyanDev-21.com/Chirpy/internal/platform/redisx"
	"github.com/redis/go-redis/v9"
)

// Store caches one GroupSnapshot per group:
//
//	group:v2:{id}      JSON GroupSnapshot (v2: roles and visibility)
//	group:v2:{id}:ver  version counter (see redisx.Versioned)
type Store = *redisx.Versioned[GroupSnapshot]

const groupTTL = 7 * 24 * time.Hour

func NewStore(rdb redis.UniversalClient) Store {
	return redisx.NewVersioned[GroupSnapshot](rdb, "group:v2:", groupTTL)
}
