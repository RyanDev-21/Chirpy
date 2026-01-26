package cache

import (
	"context"
	"sync"
	"time"
)

type memoryItem struct {
	value  []byte
	expiry time.Time
}

type MemoryCache struct {
	mu    sync.RWMutex
	items map[string]memoryItem
}

func NewMemoryCache() *MemoryCache {
	return &MemoryCache{items: make(map[string]memoryItem)}
}

func (m *MemoryCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	it := memoryItem{value: value}
	if ttl > 0 {
		it.expiry = time.Now().Add(ttl)
	}
	m.items[key] = it
	return nil
}

func (m *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	it, ok := m.items[key]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrCacheMiss
	}
	if !it.expiry.IsZero() && time.Now().After(it.expiry) {
		// expired: remove and return miss
		m.mu.Lock()
		delete(m.items, key)
		m.mu.Unlock()
		return nil, ErrCacheMiss
	}
	return it.value, nil
}

func (m *MemoryCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.items, key)
	m.mu.Unlock()
	return nil
}
