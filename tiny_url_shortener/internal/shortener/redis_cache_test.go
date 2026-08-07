package shortener

import (
	"testing"
	"time"
)

func TestCacheTTLJitterStaysWithinTenPercent(t *testing.T) {
	ttl := time.Hour
	for range 1_000 {
		got := jitter(ttl)
		if got < 54*time.Minute || got > 66*time.Minute {
			t.Fatalf("jitter(%v) = %v, outside ±10%%", ttl, got)
		}
	}
}

func TestCacheTTLJitterPreservesNonPositiveTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		if got := jitter(ttl); got != ttl {
			t.Fatalf("jitter(%v) = %v", ttl, got)
		}
	}
}
