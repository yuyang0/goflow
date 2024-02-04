package runtime

import (
	redisDataStore "github.com/yuyang0/goflow/core/redis-datastore"
	"github.com/yuyang0/goflow/core/sdk"
	"github.com/yuyang0/goflow/types"
)

func initDataStore(cfg *types.RedisConfig) (dataStore sdk.DataStore, err error) {
	dataStore, err = redisDataStore.GetRedisDataStore(cfg)
	return dataStore, err
}
