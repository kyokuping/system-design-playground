package shortener

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestCachedRepository_CacheHitSkipsSource(t *testing.T) {
	cache := &stubURLCache{getURL: "https://example.com/hit"}
	source := &countingRepository{}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)

	mapping, err := repository.FindByShortKey(context.Background(), "Ab12Cd3")
	if err != nil {
		t.Fatalf("FindByShortKey() error = %v", err)
	}
	if got := mapping.LongURL.String(); got != cache.getURL {
		t.Fatalf("URL = %q, want %q", got, cache.getURL)
	}
	if source.findCalls != 0 {
		t.Fatalf("source calls = %d, want 0", source.findCalls)
	}
}

func TestCachedRepository_CacheMissFillsFromSource(t *testing.T) {
	source := &countingRepository{mapping: URLMapping{ShortKey: "Ab12Cd3", LongURL: mustCacheURL(t, "https://example.com/source")}}
	cache := &stubURLCache{getErr: ErrCacheMiss}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)

	mapping, err := repository.FindByShortKey(context.Background(), "Ab12Cd3")
	if err != nil {
		t.Fatalf("FindByShortKey() error = %v", err)
	}
	if mapping.LongURL.String() != source.mapping.LongURL.String() {
		t.Fatalf("unexpected mapping: %+v", mapping)
	}
	if cache.setURL != source.mapping.LongURL.String() {
		t.Fatalf("cached URL = %q", cache.setURL)
	}
}

func TestCachedRepository_CacheFailureFallsBackToSource(t *testing.T) {
	source := &countingRepository{mapping: URLMapping{ShortKey: "Ab12Cd3", LongURL: mustCacheURL(t, "https://example.com/source")}}
	cache := &stubURLCache{getErr: errors.New("redis unavailable"), setErr: errors.New("redis unavailable")}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)

	if _, err := repository.FindByShortKey(context.Background(), "Ab12Cd3"); err != nil {
		t.Fatalf("FindByShortKey() error = %v, want cache failure ignored", err)
	}
}

func TestCachedRepository_NegativeCacheSkipsSource(t *testing.T) {
	cache := &stubURLCache{negative: true}
	source := &countingRepository{}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)

	_, err := repository.FindByShortKey(context.Background(), "Unknown")
	if !errors.Is(err, ErrURLMappingNotFound) {
		t.Fatalf("error = %v, want ErrURLMappingNotFound", err)
	}
	if source.findCalls != 0 {
		t.Fatalf("source calls = %d, want 0", source.findCalls)
	}
}

func TestCachedRepository_SourceNotFoundSetsNegativeCache(t *testing.T) {
	cache := &stubURLCache{getErr: ErrCacheMiss}
	source := &countingRepository{findErr: ErrURLMappingNotFound}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)

	_, err := repository.FindByShortKey(context.Background(), "Unknown")
	if !errors.Is(err, ErrURLMappingNotFound) {
		t.Fatalf("error = %v, want ErrURLMappingNotFound", err)
	}
	if !cache.negativeSet {
		t.Fatal("negative cache was not set")
	}
}

type stubURLCache struct {
	getURL      string
	getErr      error
	negative    bool
	setURL      string
	setErr      error
	negativeSet bool
}

func (c *stubURLCache) Get(context.Context, string) (string, bool, error) {
	return c.getURL, c.negative, c.getErr
}
func (c *stubURLCache) Set(_ context.Context, _ string, value string, _ time.Duration) error {
	c.setURL = value
	return c.setErr
}
func (c *stubURLCache) SetNegative(context.Context, string, time.Duration) error {
	c.negativeSet = true
	return c.setErr
}
func (c *stubURLCache) Delete(context.Context, string) error { return c.setErr }

type countingRepository struct {
	mapping   URLMapping
	findErr   error
	findCalls int
}

func (*countingRepository) Save(context.Context, URLMapping) error       { return nil }
func (*countingRepository) AddOwner(context.Context, URLOwnership) error { return nil }
func (r *countingRepository) FindByShortKey(context.Context, string) (URLMapping, error) {
	r.findCalls++
	if r.findErr != nil {
		return URLMapping{}, r.findErr
	}
	return r.mapping, nil
}
func (*countingRepository) FindByLongURL(context.Context, *url.URL) (URLMapping, error) {
	return URLMapping{}, ErrURLMappingNotFound
}

func mustCacheURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
