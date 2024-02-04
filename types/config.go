package types

import (
	"github.com/redis/go-redis/v9"
)

type RedisConfig struct {
	Addr          string   `json:"addr"`
	SentinelAddrs []string `json:"sentinel_addrs"`
	MasterName    string   `json:"master_name"`
	Username      string   `json:"username"`
	Password      string   `json:"password"`
	DB            int      `json:"db"`
	Expire        uint     `json:"expire"`
}

func (cfg *RedisConfig) NewRedisClient() (cli *redis.Client) {
	if len(cfg.SentinelAddrs) > 0 {
		cli = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    cfg.MasterName,
			SentinelAddrs: cfg.SentinelAddrs,
			DB:            cfg.DB,
			Username:      cfg.Username,
			Password:      cfg.Password,
		})
	} else {
		cli = redis.NewClient(&redis.Options{
			Addr:     cfg.Addr,
			DB:       cfg.DB,
			Username: cfg.Username,
			Password: cfg.Password,
		})
	}
	return
}
