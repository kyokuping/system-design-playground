package shortener

import (
	"errors"
	"testing"
	"time"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/handler"
)

func newExpiringShortener(lastAccessedAt, now time.Time) handler.URLService {
	panic("TODO: wire the Shortener with an injectable clock")
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
