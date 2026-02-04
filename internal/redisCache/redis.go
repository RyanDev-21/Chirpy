package rediscache

import (
	"context"
	"encoding/json"
	"time"

	//	"time"

	redisotel "github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
)

//need to implement get set and hget and hset and stuff
//for only chat messages
type RedisCache struct{
	redis redis.UniversalClient
}

type RedisCacheImpl interface{
	Get(ctx context.Context,key string,dst interface{})(bool,error)
	Set(ctx context.Context,key string,val interface{})error
	Delete(ctx context.Context,key string)error
	HGet(ctx context.Context,key,field string,dst interface{})(bool,error)
	HMGet(ctx context.Context, key string, fields []string) ([]interface{}, error)
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HSet(ctx context.Context, key string, values ...interface{}) error
	HDel(ctx context.Context, key, field string) error
	RPush(ctx context.Context, key string, val interface{}) error
	LRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	XAdd(ctx context.Context,key string,values ...interface{})error	
}

func NewRedisClient()(redis.UniversalClient,error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     "redis-10979.c292.ap-southeast-1-1.ec2.cloud.redislabs.com:10979",
		Username: "default",
		Password: "dKtoeF3RH6WhCU34GmXj6whNDGXlrWEC",
		DB:       0,
	})
	
	context := context.Background()
	_,err:=rdb.Ping(context).Result()	
	if err == redis.Nil || err !=nil{
		return nil,err
	}
	if err = redisotel.InstrumentTracing(rdb); err!=nil{
		return nil,err
	}
	return rdb,nil
}

func NewRedisCacheImpl(rdb redis.UniversalClient)RedisCacheImpl{
	return &RedisCache{
		redis: rdb,
	}
}

func (rc *RedisCache)XAdd(ctx context.Context,key string,values ...interface{})error{
	return rc.redis.HSet(ctx, key, values).Err()
}

func (rc *RedisCache)Get(ctx context.Context,key string,dst interface{})(bool,error){
	val, err:= rc.redis.Get(ctx,key).Result()
	if err !=nil{
		if err == redis.Nil{
			return false,nil
		}
		return false,err
	}
	if err = json.Unmarshal([]byte(val),dst);err!=nil{
		return false,err
	}
	return true,nil
}




func (rc *RedisCache) Set(ctx context.Context, key string, val interface{}) error {
	if err := rc.redis.Set(ctx, key, val,10*time.Hour).Err(); err != nil {
		return err
	}
	return nil
}

// Delete deletes a key
func (rc *RedisCache) Delete(ctx context.Context, key string) error {
	if err := rc.redis.Del(ctx, key).Err(); err != nil {
		return err
	}
	return nil
}

func (rc *RedisCache) HGet(ctx context.Context, key, field string, dst interface{}) (bool, error) {
	val, err := rc.redis.HGet(ctx, key, field).Result()
	if err == redis.Nil {
		return false, nil
	} else if err != nil {
		return false, err
	} else {
		if err = json.Unmarshal([]byte(val), dst); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (rc *RedisCache) HMGet(ctx context.Context, key string, fields []string) ([]interface{}, error) {
	return rc.redis.HMGet(ctx, key, fields...).Result()
}

func (rc *RedisCache) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return rc.redis.HGetAll(ctx, key).Result()
}

func (rc *RedisCache) HSet(ctx context.Context, key string, values ...interface{}) error {
	return rc.redis.HSet(ctx, key, values).Err()
}

func (rc *RedisCache) HDel(ctx context.Context, key, field string) error {
	return rc.redis.HDel(ctx, key, field).Err()
}

func (rc *RedisCache) RPush(ctx context.Context, key string, val interface{}) error {
	return rc.redis.RPush(ctx, key, val).Err()
}

func (rc *RedisCache) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return rc.redis.LRange(ctx, key, start, stop).Result()
}
