package groups

import (
	"context"
	"errors"
	"log"
	"slices"
	"sync"
	"time"

	"RyanDev-21.com/Chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type CacheGroupInfo struct {
	Name     string
	TotalMem int16
	MaxMem   int16
}

// Cache is an in-process cache of group metadata and memberships.
// All access goes through methods so the locks are always held.
type Cache struct {
	mu          sync.RWMutex
	GroupCache  map[uuid.UUID]*CacheGroupInfo
	MemberCache map[uuid.UUID][]uuid.UUID
	groupRepo   GroupRepo
}

func FormatDbModel(dbModel *database.ChatGroup) *CacheGroupInfo {
	return &CacheGroupInfo{
		TotalMem: dbModel.CurrentMember,
		Name:     dbModel.Name,
		MaxMem:   dbModel.MaxMember,
	}
}

func NewGroupCache(groupRepo GroupRepo) *Cache {
	return &Cache{
		GroupCache:  make(map[uuid.UUID]*CacheGroupInfo, 1000),
		MemberCache: make(map[uuid.UUID][]uuid.UUID, 1000),
		groupRepo:   groupRepo,
	}
}

// CheckNameFromGroup reports whether memID is a member of groupID.
func (cache *Cache) CheckNameFromGroup(groupID uuid.UUID, memID uuid.UUID) bool {
	members, err := cache.GetFromMember(groupID)
	if err != nil {
		return false
	}
	return slices.Contains(members, memID)
}

func (cache *Cache) Load() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	groupInfo, err := cache.groupRepo.getAllGroupInfo(ctx)
	if err != nil {
		return err
	}
	for _, v := range *groupInfo {
		groupMems, err := cache.groupRepo.getMemsByID(ctx, v.ID)
		if err != nil {
			log.Printf("failed to get the group member ids: %s", err)
			continue
		}
		cache.mu.Lock()
		cache.GroupCache[v.ID] = &CacheGroupInfo{Name: v.Name, TotalMem: v.CurrentMember, MaxMem: v.MaxMember}
		cache.MemberCache[v.ID] = *groupMems
		cache.mu.Unlock()
	}
	return nil
}

// GetFromGroup returns a copy of the group metadata, loading it from the db on a miss.
func (cache *Cache) GetFromGroup(groupID uuid.UUID) (*CacheGroupInfo, error) {
	cache.mu.RLock()
	v, ok := cache.GroupCache[groupID]
	cache.mu.RUnlock()
	if ok {
		cp := *v
		return &cp, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	groupInfo, err := cache.groupRepo.getGroupInfoByID(ctx, groupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFoundGroup
		}
		return nil, err
	}
	info := FormatDbModel(groupInfo)
	cache.mu.Lock()
	cache.GroupCache[groupID] = info
	cache.mu.Unlock()
	cp := *info
	return &cp, nil
}

// GetFromMember returns a copy of the member list, loading it from the db on a miss.
func (cache *Cache) GetFromMember(groupID uuid.UUID) ([]uuid.UUID, error) {
	cache.mu.RLock()
	v, ok := cache.MemberCache[groupID]
	cache.mu.RUnlock()
	if ok {
		return slices.Clone(v), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	groupMems, err := cache.groupRepo.getMemsByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	cache.mu.Lock()
	cache.MemberCache[groupID] = *groupMems
	cache.mu.Unlock()
	return slices.Clone(*groupMems), nil
}

// AddGroup stores a freshly created group.
func (cache *Cache) AddGroup(groupID uuid.UUID, info CacheGroupInfo, members []uuid.UUID) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.GroupCache[groupID] = &info
	cache.MemberCache[groupID] = slices.Clone(members)
}

// AddMember adds userID to the group, enforcing capacity and uniqueness atomically.
func (cache *Cache) AddMember(groupID, userID uuid.UUID) error {
	if _, err := cache.GetFromGroup(groupID); err != nil {
		return err
	}
	if _, err := cache.GetFromMember(groupID); err != nil {
		return err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	info := cache.GroupCache[groupID]
	members := cache.MemberCache[groupID]
	if slices.Contains(members, userID) {
		return ErrAlreadyMember
	}
	if info.TotalMem >= info.MaxMem {
		return ErrGroupFull
	}
	cache.MemberCache[groupID] = append(members, userID)
	info.TotalMem++
	return nil
}

// RemoveMember removes userID from the group.
func (cache *Cache) RemoveMember(groupID, userID uuid.UUID) error {
	if _, err := cache.GetFromGroup(groupID); err != nil {
		return err
	}
	if _, err := cache.GetFromMember(groupID); err != nil {
		return err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	members := cache.MemberCache[groupID]
	idx := slices.Index(members, userID)
	if idx == -1 {
		return ErrNotMember
	}
	cache.MemberCache[groupID] = slices.Delete(members, idx, idx+1)
	if info := cache.GroupCache[groupID]; info.TotalMem > 0 {
		info.TotalMem--
	}
	return nil
}

// CheckGroupNameFromCache reports whether a group with this name already exists.
func (cache *Cache) CheckGroupNameFromCache(name string) (bool, error) {
	cache.mu.RLock()
	for _, v := range cache.GroupCache {
		if v.Name == name {
			cache.mu.RUnlock()
			return true, nil
		}
	}
	cache.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := cache.groupRepo.getGroupInfoByName(ctx, name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}
