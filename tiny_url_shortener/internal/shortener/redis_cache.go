package shortener

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisURLKeyPrefix = "cache:url:v1:"

const (
	cacheStatePositive = "positive"
	cacheStateNegative = "negative"
)

type redisCacheEntry struct {
	State          string    `json:"state"`
	LongURL        string    `json:"long_url,omitempty"`
	LastAccessedAt time.Time `json:"last_accessed_at,omitempty"`
}

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
func (c *RedisCache) Get(ctx context.Context, shortKey string) (CachedURL, error) {
	value, err := c.client.Get(ctx, redisURLKey(shortKey)).Result()
	if errors.Is(err, redis.Nil) {
		return CachedURL{}, ErrCacheMiss
	}
	if err != nil {
		return CachedURL{}, err
	}
	var entry redisCacheEntry
	if err := json.Unmarshal([]byte(value), &entry); err != nil {
		return CachedURL{}, ErrCacheMiss
	}
	switch entry.State {
	case cacheStatePositive:
		if entry.LongURL == "" {
			return CachedURL{}, ErrCacheMiss
		}
		return CachedURL{LongURL: entry.LongURL, LastAccessedAt: entry.LastAccessedAt}, nil
	case cacheStateNegative:
		return CachedURL{Negative: true}, nil
	default:
		return CachedURL{}, ErrCacheMiss
	}
}
func (c *RedisCache) SetPositive(ctx context.Context, mapping URLMapping, ttl time.Duration) error {
	entry := redisCacheEntry{
		State:          cacheStatePositive,
		LongURL:        mapping.LongURL.String(),
		LastAccessedAt: mapping.LastAccessedAt,
	}
	value, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, redisURLKey(mapping.ShortKey), value, jitter(ttl)).Err()
}
func (c *RedisCache) SetNegativeIfAbsent(ctx context.Context, shortKey string, ttl time.Duration) (bool, error) {
	value, err := json.Marshal(redisCacheEntry{State: cacheStateNegative})
	if err != nil {
		return false, err
	}
	return c.client.SetNX(ctx, redisURLKey(shortKey), value, jitter(ttl)).Result()
}
func (c *RedisCache) Delete(ctx context.Context, shortKey string) error {
	return c.client.Del(ctx, redisURLKey(shortKey)).Err()
}
func redisURLKey(shortKey string) string {
	return redisURLKeyPrefix + "{" + shortKey + "}:entry"
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
