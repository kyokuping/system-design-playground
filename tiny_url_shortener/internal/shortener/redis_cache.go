package shortener

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisURLKeyPrefix = "cache:url:v1:"
const negativeCacheValue = "\x00"

type RedisCache struct{ client *redis.Client }

func OpenRedis(ctx context.Context, address string) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{Addr: address})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &RedisCache{client: client}, nil
}
func (c *RedisCache) Close() error { return c.client.Close() }
func (c *RedisCache) Get(ctx context.Context, shortKey string) (string, bool, error) {
	value, err := c.client.Get(ctx, redisURLKeyPrefix+shortKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, ErrCacheMiss
	}
	if err != nil {
		return "", false, err
	}
	return value, value == negativeCacheValue, nil
}
func (c *RedisCache) Set(ctx context.Context, shortKey, longURL string, ttl time.Duration) error {
	return c.client.Set(ctx, redisURLKeyPrefix+shortKey, longURL, jitter(ttl)).Err()
}
func (c *RedisCache) SetNegative(ctx context.Context, shortKey string, ttl time.Duration) error {
	return c.client.Set(ctx, redisURLKeyPrefix+shortKey, negativeCacheValue, jitter(ttl)).Err()
}
func (c *RedisCache) Delete(ctx context.Context, shortKey string) error {
	return c.client.Del(ctx, redisURLKeyPrefix+shortKey).Err()
}
func jitter(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return ttl
	}
	spread := ttl / 10
	if spread == 0 {
		return ttl
	}
	return ttl - spread + time.Duration(rand.Int64N(int64(spread)*2+1))
}
