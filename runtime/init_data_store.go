package runtime

import (
	redisDataStore "github.com/s8sg/goflow/core/redis-datastore"
	"github.com/s8sg/goflow/core/sdk"
	"github.com/s8sg/goflow/types"
)

func initDataStore(cfg *types.RedisConfig) (dataStore sdk.DataStore, err error) {
	dataStore, err = redisDataStore.GetRedisDataStore(cfg)
	return dataStore, err
}
