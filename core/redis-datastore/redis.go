package RedisDataStore

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/yuyang0/goflow/core/sdk"
	"github.com/yuyang0/goflow/types"
)

type RedisDataStore struct {
	bucketName  string
	redisClient redis.UniversalClient
}

func GetRedisDataStore(cfg *types.RedisConfig) (sdk.DataStore, error) {
	ds := &RedisDataStore{}
	client := cfg.NewRedisClient()
	err := client.Ping(context.TODO()).Err()
	if err != nil {
		return nil, err
	}

	ds.redisClient = client
	return ds, nil
}

func (this *RedisDataStore) Configure(flowName string, requestId string) {
	bucketName := fmt.Sprintf("core-%s-%s", flowName, requestId)

	this.bucketName = bucketName
}

func (this *RedisDataStore) Init() error {
	if this.redisClient == nil {
		return fmt.Errorf("redis client not initialized, use GetRedisDataStore()")
	}

	return nil
}

func (this *RedisDataStore) Set(key string, value []byte) error {
	if this.redisClient == nil {
		return fmt.Errorf("redis client not initialized, use GetRedisDataStore()")
	}

	fullPath := getPath(this.bucketName, key)
	_, err := this.redisClient.Set(context.TODO(), fullPath, string(value), 0).Result()
	if err != nil {
		return fmt.Errorf("error writing: %s, error: %s", fullPath, err.Error())
	}

	return nil
}

func (this *RedisDataStore) Get(key string) ([]byte, error) {
	if this.redisClient == nil {
		return nil, fmt.Errorf("redis client not initialized, use GetRedisDataStore()")
	}

	fullPath := getPath(this.bucketName, key)
	v := this.redisClient.Get(context.TODO(), fullPath)
	if v == nil {
		return nil, errors.New(fmt.Sprintf("error reading: %v, data is nil", fullPath))
	}
	value, err := v.Result()
	if err != nil {
		return nil, fmt.Errorf("error reading: %s, error: %s", fullPath, err.Error())
	}
	return []byte(value), nil
}

func (this *RedisDataStore) Del(key string) error {
	if this.redisClient == nil {
		return fmt.Errorf("redis client not initialized, use GetRedisDataStore()")
	}

	fullPath := getPath(this.bucketName, key)
	_, err := this.redisClient.Del(context.TODO(), fullPath).Result()
	if err != nil {
		return fmt.Errorf("error removing: %s, error: %s", fullPath, err.Error())
	}
	return nil
}

func (this *RedisDataStore) Cleanup() error {
	key := this.bucketName + ".*"
	client := this.redisClient
	var rerr error

	iter := client.Scan(context.TODO(), 0, key, 0).Iterator()
	for iter.Next(context.TODO()) {
		err := client.Del(context.TODO(), iter.Val()).Err()
		if err != nil {
			rerr = err
		}
	}

	if err := iter.Err(); err != nil {
		rerr = err
	}
	return rerr
}

// getPath produces a string as bucketname.value
func getPath(bucket, key string) string {
	fileName := fmt.Sprintf("%s.value", key)
	return fmt.Sprintf("%s.%s", bucket, fileName)
}

func (this *RedisDataStore) CopyStore() (sdk.DataStore, error) {
	return &RedisDataStore{bucketName: this.bucketName, redisClient: this.redisClient}, nil
}
