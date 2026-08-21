package shortener

import (
	"errors"
	"testing"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/handler"
)

type analyticsServiceContract interface {
	handler.URLService
	Statistics(shortKey string) (URLStatistics, error)
}

func newAnalyticsShortener() analyticsServiceContract {
	return New(NewMemoryRepository(), NewRandomKeyGenerator())
}

func TestGetLongURL_IncrementsVisitCount(t *testing.T) {
	skipExpectedFailure(t)

	service := newAnalyticsShortener()
	shortKey, _ := shorten(t, service, testUserID, "https://example.com/alpha")
	for range 3 {
		if _, err := service.GetLongURL(shortKey); err != nil {
			t.Fatalf("GetLongURL() returned an unexpected error: %v", err)
		}
	}

	statistics, err := service.Statistics(shortKey)
	if err != nil {
		t.Fatalf("Statistics() returned an unexpected error: %v", err)
	}
	if statistics.Visits != 3 {
		t.Fatalf("Statistics().Visits = %d, want 3", statistics.Visits)
	}
}

func TestGetShortURL_DoesNotIncrementVisitCount(t *testing.T) {
	skipExpectedFailure(t)

	service := newAnalyticsShortener()
	shortKey, _ := shorten(t, service, testUserID, "https://example.com/alpha")
	shorten(t, service, testUserID, "https://example.com/alpha")

	statistics, err := service.Statistics(shortKey)
	if err != nil {
		t.Fatalf("Statistics() returned an unexpected error: %v", err)
	}
	if statistics.Visits != 0 {
		t.Fatalf("Statistics().Visits = %d, want 0", statistics.Visits)
	}
}

func TestStatistics_ReturnsURLMappingAndVisitCount(t *testing.T) {
	skipExpectedFailure(t)

	service := newAnalyticsShortener()
	shortKey, _ := shorten(t, service, testUserID, "https://example.com/alpha")
	for range 2 {
		if _, err := service.GetLongURL(shortKey); err != nil {
			t.Fatalf("GetLongURL() returned an unexpected error: %v", err)
		}
	}

	got, err := service.Statistics(shortKey)
	if err != nil {
		t.Fatalf("Statistics() returned an unexpected error: %v", err)
	}
	want := URLStatistics{
		ShortKey: shortKey,
		LongURL:  "https://example.com/alpha",
		Visits:   2,
	}
	if got != want {
		t.Fatalf("Statistics() = %+v, want %+v", got, want)
	}
}

func TestStatistics_UnknownKeyReturnsDomainError(t *testing.T) {
	skipExpectedFailure(t)

	service := newAnalyticsShortener()
	_, err := service.Statistics("Unknown")
	if !errors.Is(err, ErrURLMappingNotFound) {
		t.Fatalf("Statistics() error = %v, want ErrURLMappingNotFound", err)
	}
}
