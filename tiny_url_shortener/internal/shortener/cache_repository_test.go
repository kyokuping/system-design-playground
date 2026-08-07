package shortener

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestCachedRepository_CacheHitSkipsSource(t *testing.T) {
	lastAccessedAt := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	cache := &stubURLCache{cached: CachedURL{LongURL: "https://example.com/hit", LastAccessedAt: lastAccessedAt}}
	source := &countingRepository{}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)

	mapping, err := repository.FindByShortKey(context.Background(), "Ab12Cd3")
	if err != nil {
		t.Fatalf("FindByShortKey() error = %v", err)
	}
	if got := mapping.LongURL.String(); got != cache.cached.LongURL {
		t.Fatalf("URL = %q, want %q", got, cache.cached.LongURL)
	}
	if !mapping.LastAccessedAt.Equal(lastAccessedAt) {
		t.Fatalf("LastAccessedAt = %v, want %v", mapping.LastAccessedAt, lastAccessedAt)
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
	if cache.positive.LongURL.String() != source.mapping.LongURL.String() {
		t.Fatalf("cached mapping = %+v", cache.positive)
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
	cache := &stubURLCache{cached: CachedURL{Negative: true}}
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

func TestCachedRepository_SaveWritesPositiveCache(t *testing.T) {
	mapping := URLMapping{
		ShortKey:       "Ab12Cd3",
		LongURL:        mustCacheURL(t, "https://example.com/created"),
		LastAccessedAt: time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC),
	}
	cache := &stubURLCache{}
	repository := NewCachedRepository(&countingRepository{}, cache, time.Hour, 30*time.Second)

	if err := repository.Save(context.Background(), mapping); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if cache.positive.ShortKey != mapping.ShortKey || cache.positive.LongURL.String() != mapping.LongURL.String() {
		t.Fatalf("positive cache = %+v, want %+v", cache.positive, mapping)
	}
	if cache.deleteCalls != 0 {
		t.Fatalf("cache deletes = %d, want 0", cache.deleteCalls)
	}
}

func TestCachedRepository_LateNegativeCannotOverwriteCreatedMapping(t *testing.T) {
	findStarted := make(chan struct{})
	continueFind := make(chan struct{})
	source := &countingRepository{
		findErr:      ErrURLMappingNotFound,
		findStarted:  findStarted,
		continueFind: continueFind,
	}
	cache := &orderedURLCache{}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)
	mapping := URLMapping{
		ShortKey:       "Ab12Cd3",
		LongURL:        mustCacheURL(t, "https://example.com/created"),
		LastAccessedAt: time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC),
	}

	result := make(chan URLMapping, 1)
	errors := make(chan error, 1)
	go func() {
		got, err := repository.FindByShortKey(context.Background(), mapping.ShortKey)
		result <- got
		errors <- err
	}()

	<-findStarted
	if err := repository.Save(context.Background(), mapping); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	close(continueFind)

	if err := <-errors; err != nil {
		t.Fatalf("FindByShortKey() error = %v", err)
	}
	got := <-result
	if got.LongURL.String() != mapping.LongURL.String() {
		t.Fatalf("URL = %q, want %q", got.LongURL, mapping.LongURL)
	}
	if cached, err := cache.Get(context.Background(), mapping.ShortKey); err != nil || cached.Negative {
		t.Fatalf("cache = %+v, error = %v; want positive", cached, err)
	}
}

func TestCachedRepository_CacheHitPreservesExpiration(t *testing.T) {
	lastAccessedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := lastAccessedAt.AddDate(0, 6, 0)
	source := &countingRepository{mapping: URLMapping{
		ShortKey:       "Ab12Cd3",
		LongURL:        mustCacheURL(t, "https://example.com/expired"),
		LastAccessedAt: lastAccessedAt,
	}}
	cache := &orderedURLCache{}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)
	service := NewWithClock(repository, NewRandomKeyGenerator(), func() time.Time { return now })

	for attempt := 1; attempt <= 2; attempt++ {
		_, err := service.GetLongURL("Ab12Cd3")
		if !errors.Is(err, ErrURLMappingExpired) {
			t.Fatalf("attempt %d: error = %v, want ErrURLMappingExpired", attempt, err)
		}
	}
	if source.findCalls != 1 {
		t.Fatalf("source calls = %d, want 1", source.findCalls)
	}
}

func TestCachedRepository_RecordAccessRefreshesCachedExpiration(t *testing.T) {
	lastAccessedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := lastAccessedAt.AddDate(0, 6, 0).Add(-time.Minute)
	mapping := URLMapping{
		ShortKey:       "Ab12Cd3",
		LongURL:        mustCacheURL(t, "https://example.com/active"),
		LastAccessedAt: lastAccessedAt,
	}
	source := NewMemoryRepository()
	if err := source.Save(context.Background(), mapping); err != nil {
		t.Fatalf("source.Save() error = %v", err)
	}
	cache := &orderedURLCache{}
	repository := NewCachedRepository(source, cache, time.Hour, 30*time.Second)
	service := NewWithClock(repository, NewRandomKeyGenerator(), func() time.Time { return now })

	if _, err := service.GetLongURL(mapping.ShortKey); err != nil {
		t.Fatalf("first GetLongURL() error = %v", err)
	}
	now = lastAccessedAt.AddDate(0, 6, 0).Add(time.Minute)
	if _, err := service.GetLongURL(mapping.ShortKey); err != nil {
		t.Fatalf("second GetLongURL() error = %v; cached access time was not refreshed", err)
	}
}

type stubURLCache struct {
	cached      CachedURL
	getErr      error
	positive    URLMapping
	setErr      error
	negativeSet bool
	deleteCalls int
}

func (c *stubURLCache) Get(context.Context, string) (CachedURL, error) {
	return c.cached, c.getErr
}
func (c *stubURLCache) SetPositive(_ context.Context, mapping URLMapping, _ time.Duration) error {
	c.positive = mapping
	return c.setErr
}
func (c *stubURLCache) SetNegativeIfAbsent(context.Context, string, time.Duration) (bool, error) {
	c.negativeSet = true
	return true, c.setErr
}
func (c *stubURLCache) Delete(context.Context, string) error {
	c.deleteCalls++
	return c.setErr
}

type countingRepository struct {
	mapping      URLMapping
	findErr      error
	findCalls    int
	findStarted  chan struct{}
	continueFind chan struct{}
}

func (*countingRepository) Save(context.Context, URLMapping) error       { return nil }
func (*countingRepository) AddOwner(context.Context, URLOwnership) error { return nil }
func (r *countingRepository) FindByShortKey(context.Context, string) (URLMapping, error) {
	r.findCalls++
	if r.findStarted != nil {
		close(r.findStarted)
		<-r.continueFind
	}
	if r.findErr != nil {
		return URLMapping{}, r.findErr
	}
	return r.mapping, nil
}

type orderedURLCache struct {
	mu    sync.Mutex
	entry CachedURL
	set   bool
}

func (c *orderedURLCache) Get(context.Context, string) (CachedURL, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set {
		return CachedURL{}, ErrCacheMiss
	}
	return c.entry, nil
}

func (c *orderedURLCache) SetPositive(_ context.Context, mapping URLMapping, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entry = CachedURL{LongURL: mapping.LongURL.String(), LastAccessedAt: mapping.LastAccessedAt}
	c.set = true
	return nil
}

func (c *orderedURLCache) SetNegativeIfAbsent(context.Context, string, time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set {
		return false, nil
	}
	c.entry = CachedURL{Negative: true}
	c.set = true
	return true, nil
}

func (c *orderedURLCache) Delete(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entry = CachedURL{}
	c.set = false
	return nil
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
