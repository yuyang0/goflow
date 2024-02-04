package runtime

import (
	redisStateStore "github.com/yuyang0/goflow/core/redis-statestore"
	"github.com/yuyang0/goflow/core/sdk"
	"github.com/yuyang0/goflow/types"
)

func initStateStore(cfg *types.RedisConfig) (stateStore sdk.StateStore, err error) {
	stateStore, err = redisStateStore.GetRedisStateStore(cfg)
	return stateStore, err
}
