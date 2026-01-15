package main

import (
	"context"
	"fmt"
//	"time"

	"github.com/redis/go-redis/v9"
)

func ConnectionToRedis() {
	ctx := context.Background()

	rdb := redis.NewClient(&redis.Options{
		Addr:     "redis-10979.c292.ap-southeast-1-1.ec2.cloud.redislabs.com:10979",
		Username: "default",
		Password: "dKtoeF3RH6WhCU34GmXj6whNDGXlrWEC",
		DB:       0,
	})

	rdb.Del(ctx,"foo","veryGood")
	result, err := rdb.Get(ctx, "foo").Result()

	if err != nil {
		panic(err)
	}

	fmt.Println(result) // >>> bar

}

func main(){
	ConnectionToRedis()
}

