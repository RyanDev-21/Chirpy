package users

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
)

type configCache struct {
	configCache  map[uuid.UUID]*ConfigList
	configMuLock sync.RWMutex
	userRepo     UserRepo
}

type ConfigCache interface {
	Load() error
	UpdateConfig(userID uuid.UUID, confgiList *ConfigList) error
	GetConfig(userID uuid.UUID) *ConfigList
}

func NewConfigCache(userRepo UserRepo) ConfigCache {
	return &configCache{
		configCache:  make(map[uuid.UUID]*ConfigList),
		configMuLock: sync.RWMutex{},
		userRepo:     userRepo,
	}
}

func (c *configCache) Load() error {
	context, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	configBytes, err := c.userRepo.GetAllUserConfigs(context)
	if err != nil {
		return err
	}
	c.configMuLock.Lock()
	defer c.configMuLock.Unlock()
	for _, v := range *configBytes {
		var pay []ElementCustom
		err := json.Unmarshal(v.Pref, &pay)
		if err != nil {
			return err
		}
		c.configCache[v.UserID] = &ConfigList{
			List: pay,
		}
	}
	return nil
}

// right now we just update the whole config
// maybe there might be another way to handler the error
func (c *configCache) UpdateConfig(userID uuid.UUID, configList *ConfigList) error {
	c.configMuLock.Lock()
	defer c.configMuLock.Unlock()
	if _, ok := c.configCache[userID]; !ok {
		c.configCache[userID] = configList
	}
	c.configCache[userID] = configList
	return nil
}

func (c *configCache) GetConfig(userID uuid.UUID) *ConfigList {
	c.configMuLock.Lock()
	defer c.configMuLock.Unlock()
	if v, ok := c.configCache[userID]; ok {
		return v
	}
	return nil
}
