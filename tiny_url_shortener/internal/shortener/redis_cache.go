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

// One minute exceeds the server's longest 10-second bounded operation.
const revisionTTLGuard = time.Minute

const (
	cacheStatePositive = "positive"
	cacheStateNegative = "negative"
)

type redisCacheEntry struct {
	State          string    `json:"state"`
	LongURL        string    `json:"long_url,omitempty"`
	LastAccessedAt time.Time `json:"last_accessed_at,omitempty"`
	Revision       int64     `json:"revision,omitempty"`
}

var setPositiveIfCurrent = redis.NewScript(`
local current = redis.call('GET', KEYS[2]) or '0'
local incoming = ARGV[1]
if #current > #incoming or (#current == #incoming and current > incoming) then
    return 0
end
redis.call('SET', KEYS[2], ARGV[1], 'PX', ARGV[4])
if tonumber(ARGV[3]) > 0 then
    redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
else
    redis.call('SET', KEYS[1], ARGV[2])
end
return 1
`)

var invalidateIfCurrent = redis.NewScript(`
local current = redis.call('GET', KEYS[2]) or '0'
local incoming = ARGV[1]
if #current > #incoming or (#current == #incoming and current > incoming) then
    return 0
end
redis.call('SET', KEYS[2], ARGV[1], 'PX', ARGV[2])
redis.call('DEL', KEYS[1])
return 1
`)

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
		return CachedURL{LongURL: entry.LongURL, LastAccessedAt: entry.LastAccessedAt, Revision: entry.Revision}, nil
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
		Revision:       mapping.Revision,
	}
	value, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	entryTTL := jitter(ttl)
	_, err = setPositiveIfCurrent.Run(ctx, c.client,
		[]string{redisURLKey(mapping.ShortKey), redisURLRevisionKey(mapping.ShortKey)},
		mapping.Revision, value, entryTTL.Milliseconds(), revisionTTL(ttl).Milliseconds(),
	).Result()
	return err
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
func (c *RedisCache) Invalidate(ctx context.Context, shortKey string, revision int64, positiveTTL time.Duration) error {
	_, err := invalidateIfCurrent.Run(ctx, c.client,
		[]string{redisURLKey(shortKey), redisURLRevisionKey(shortKey)}, revision, revisionTTL(positiveTTL).Milliseconds(),
	).Result()
	return err
}
func revisionTTL(positiveTTL time.Duration) time.Duration {
	return max(positiveTTL, 0) + revisionTTLGuard
}
func redisURLKey(shortKey string) string {
	return redisURLKeyPrefix + "{" + shortKey + "}:entry"
}
func redisURLRevisionKey(shortKey string) string {
	return redisURLKeyPrefix + "{" + shortKey + "}:revision"
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
