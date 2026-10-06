package redis

import (
	"fmt"
	"log"

	"github.com/lnb/HRPAuth-Yggdrasil-API/config"
	"github.com/redis/go-redis/v9"
)

var Client *redis.Client

func Init() {
	cfg := config.AppConfig.Redis

	Client = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	log.Println("Redis client initialized")
}
