package shortener

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRedisCacheEntryRoundTripsExpirationMetadata(t *testing.T) {
	want := redisCacheEntry{
		State:          cacheStatePositive,
		LongURL:        "https://example.com/created",
		LastAccessedAt: time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC),
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got redisCacheEntry
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.State != want.State || got.LongURL != want.LongURL || !got.LastAccessedAt.Equal(want.LastAccessedAt) {
		t.Fatalf("entry = %+v, want %+v", got, want)
	}
}

func TestRedisURLKey_KeepsFirstCacheKeyVersion(t *testing.T) {
	// The entry key carries a hash tag and a purpose suffix, so it never
	// collides with the plain cache:url:v1:{shortKey} strings of the original
	// format. An incompatible value format needs no key version bump.
	if got, want := redisURLKey("abc123"), "cache:url:v1:{abc123}:entry"; got != want {
		t.Fatalf("redisURLKey() = %q, want %q", got, want)
	}
}

func TestCacheTTLJitterStaysWithinTenPercent(t *testing.T) {
	ttl := time.Hour
	var varied bool
	for range 1_000 {
		got := jitter(ttl)
		if got < 54*time.Minute || got > 66*time.Minute {
			t.Fatalf("jitter(%v) = %v, outside ±10%%", ttl, got)
		}
		if got != ttl {
			varied = true
		}
	}
	if !varied {
		t.Fatalf("jitter(%v) never varied", ttl)
	}
}

func TestCacheTTLJitterPreservesNonPositiveTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		if got := jitter(ttl); got != ttl {
			t.Fatalf("jitter(%v) = %v", ttl, got)
		}
	}
}
