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
		Revision:       42,
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got redisCacheEntry
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got.State != want.State || got.LongURL != want.LongURL || !got.LastAccessedAt.Equal(want.LastAccessedAt) || got.Revision != want.Revision {
		t.Fatalf("entry = %+v, want %+v", got, want)
	}
}

func TestRedisURLKey_KeepsFirstCacheKeyVersion(t *testing.T) {
	// The keys carry a hash tag and a purpose suffix, so they never collide
	// with the plain cache:url:v1:{shortKey} strings of the original format.
	// An incompatible value format needs no key version bump.
	if got, want := redisURLKey("abc123"), "cache:url:v1:{abc123}:entry"; got != want {
		t.Fatalf("redisURLKey() = %q, want %q", got, want)
	}
	if got, want := redisURLRevisionKey("abc123"), "cache:url:v1:{abc123}:revision"; got != want {
		t.Fatalf("redisURLRevisionKey() = %q, want %q", got, want)
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

func TestRevisionTTL_OutlivesPositiveEntryAndInFlightRead(t *testing.T) {
	positiveTTL := time.Hour
	if got := revisionTTL(positiveTTL); got <= positiveTTL+10*time.Second {
		t.Fatalf("revisionTTL(%v) = %v, want more than entry TTL plus longest read", positiveTTL, got)
	}
}
