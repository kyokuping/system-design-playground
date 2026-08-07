package shortener

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/handler"
)

func newExpiringShortener(lastAccessedAt, now time.Time) handler.URLService {
	repository := NewMemoryRepository()
	repository.byKey[testShortKey] = &memoryRecord{mapping: URLMapping{
		ShortKey:       testShortKey,
		LongURL:        parseURLForExpiration("https://example.com/alpha"),
		LastAccessedAt: lastAccessedAt,
	}}
	repository.byURL["https://example.com/alpha"] = testShortKey
	return NewWithClock(repository, NewRandomKeyGenerator(), func() time.Time { return now })
}

func parseURLForExpiration(rawURL string) *url.URL {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestGetLongURL_BeforeExpirationReturnsOriginalURL(t *testing.T) {
	skipExpectedFailure(t)

	lastAccessedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := lastAccessedAt.AddDate(0, 6, 0).Add(-time.Second)
	service := newExpiringShortener(lastAccessedAt, now)

	got, err := service.GetLongURL(testShortKey)
	if err != nil {
		t.Fatalf("GetLongURL() returned an unexpected error: %v", err)
	}
	want := "https://example.com/alpha"
	if got.String() != want {
		t.Fatalf("GetLongURL() = %q, want %q", got, want)
	}
}

func TestGetLongURL_AtExpirationReturnsDomainError(t *testing.T) {
	skipExpectedFailure(t)

	lastAccessedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := lastAccessedAt.AddDate(0, 6, 0)
	service := newExpiringShortener(lastAccessedAt, now)

	_, err := service.GetLongURL(testShortKey)
	if !errors.Is(err, ErrURLMappingExpired) {
		t.Fatalf("GetLongURL() error = %v, want ErrURLMappingExpired", err)
	}
}

func TestGetLongURL_AfterExpirationReturnsDomainError(t *testing.T) {
	skipExpectedFailure(t)

	lastAccessedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	now := lastAccessedAt.AddDate(0, 6, 0).Add(time.Second)
	service := newExpiringShortener(lastAccessedAt, now)

	_, err := service.GetLongURL(testShortKey)
	if !errors.Is(err, ErrURLMappingExpired) {
		t.Fatalf("GetLongURL() error = %v, want ErrURLMappingExpired", err)
	}
}
