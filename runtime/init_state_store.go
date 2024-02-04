package runtime

import (
	redisStateStore "github.com/s8sg/goflow/core/redis-statestore"
	"github.com/s8sg/goflow/core/sdk"
	"github.com/s8sg/goflow/types"
)

func initStateStore(cfg *types.RedisConfig) (stateStore sdk.StateStore, err error) {
	stateStore, err = redisStateStore.GetRedisStateStore(cfg)
	return stateStore, err
}
