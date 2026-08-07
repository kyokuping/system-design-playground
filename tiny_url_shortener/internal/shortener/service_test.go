package shortener

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/handler"
)

const (
	testUserID   = "user-123"
	testShortKey = "Ab12Cd3"
)

type futureURLServiceContract interface {
	handler.URLService
	GetCustomShortURL(userID string, longURL *url.URL, customKey string) (shortKey string, created bool, err error)
	UpdateLongURL(userID, shortKey string, longURL *url.URL) error
	DeleteURL(userID, shortKey string) error
}

func newShortener() handler.URLService {
	repository := NewMemoryRepository()
	return New(repository, NewRandomKeyGenerator())
}

func newFutureShortener() futureURLServiceContract {
	repository := NewMemoryRepository()
	return New(repository, NewRandomKeyGenerator())
}

func skipExpectedFailure(t *testing.T) {
	t.Helper()
}

func parseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q) returned an unexpected error: %v", rawURL, err)
	}
	return parsedURL
}

func shorten(t *testing.T, service handler.URLService, userID, rawURL string) (string, bool) {
	t.Helper()

	shortKey, created, err := service.GetShortURL(userID, parseURL(t, rawURL))
	if err != nil {
		t.Fatalf("GetShortURL() returned an unexpected error: %v", err)
	}
	return shortKey, created
}

// 아래 테스트는 README에 정의한 이 프로젝트의 설계를 검증한다.

func TestGetShortURL_NewURLReturnsShortKey(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	shortKey, created := shorten(t, service, testUserID, "https://example.com/alpha")

	if shortKey == "" {
		t.Fatal("GetShortURL() returned an empty short key")
	}
	if !created {
		t.Fatal("GetShortURL() created = false, want true")
	}
}

func TestGetShortURL_DistinctURLsReturnDistinctKeys(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	first, _ := shorten(t, service, testUserID, "https://example.com/alpha")
	second, _ := shorten(t, service, testUserID, "https://example.com/beta")

	if first == second {
		t.Fatalf("distinct URLs received the same key %q", first)
	}
}

func TestGetShortURL_DuplicateReturnsExistingShortKey(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	first, firstCreated := shorten(t, service, testUserID, "https://example.com/alpha")
	second, secondCreated := shorten(t, service, testUserID, "https://example.com/alpha")

	if first != second {
		t.Fatalf("duplicate URL received keys %q and %q", first, second)
	}
	if !firstCreated || secondCreated {
		t.Fatalf("created values = (%t, %t), want (true, false)", firstCreated, secondCreated)
	}
}

func TestGetLongURL_ReturnsOriginalURL(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	shortKey, _ := shorten(t, service, testUserID, "https://example.com/alpha")
	got, err := service.GetLongURL(shortKey)
	if err != nil {
		t.Fatalf("GetLongURL() returned an unexpected error: %v", err)
	}

	want := parseURL(t, "https://example.com/alpha")
	if got.String() != want.String() {
		t.Fatalf("GetLongURL() = %q, want %q", got, want)
	}
}

func TestGetShortURL_DifferentUsersShareKeyForSameURL(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	first, firstCreated := shorten(t, service, "user-1", "https://example.com/alpha")
	second, secondCreated := shorten(t, service, "user-2", "https://example.com/alpha")

	if first != second {
		t.Fatalf("different users received keys %q and %q for the same URL", first, second)
	}
	if !firstCreated || secondCreated {
		t.Fatalf("created values = (%t, %t), want (true, false)", firstCreated, secondCreated)
	}
}

func TestGetShortURL_DifferentUsersOwnSharedKey(t *testing.T) {
	skipExpectedFailure(t)

	repository := newOwnershipRepository()
	generator := &sequenceKeyGenerator{keys: []string{testShortKey}}
	service := newShortenerWithDependencies(repository, generator)

	shortKey, _, err := service.GetShortURL("user-1", parseURL(t, "https://example.com/alpha"))
	if err != nil {
		t.Fatalf("first GetShortURL() returned an unexpected error: %v", err)
	}
	sharedKey, _, err := service.GetShortURL("user-2", parseURL(t, "https://example.com/alpha"))
	if err != nil {
		t.Fatalf("second GetShortURL() returned an unexpected error: %v", err)
	}

	if shortKey != sharedKey {
		t.Fatalf("different users received keys %q and %q", shortKey, sharedKey)
	}
	for _, userID := range []string{"user-1", "user-2"} {
		if !repository.hasOwner(userID, shortKey) {
			t.Fatalf("user %q is not recorded as an owner of %q", userID, shortKey)
		}
	}
}

func TestGetShortURL_NormalizesEquivalentURLs(t *testing.T) {
	skipExpectedFailure(t)

	testCases := []struct {
		name   string
		first  string
		second string
	}{
		{name: "scheme and host case", first: "HTTPS://EXAMPLE.COM/path", second: "https://example.com/path"},
		{name: "default HTTPS port", first: "https://example.com:443/path", second: "https://example.com/path"},
		{name: "default HTTP port", first: "http://example.com:80/path", second: "http://example.com/path"},
		{name: "empty root path", first: "https://example.com", second: "https://example.com/"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := newShortener()
			first, _ := shorten(t, service, "user-1", testCase.first)
			second, created := shorten(t, service, "user-2", testCase.second)

			if first != second {
				t.Fatalf("equivalent URLs received keys %q and %q", first, second)
			}
			if created {
				t.Fatal("normalized duplicate created a new mapping")
			}
		})
	}
}

func TestGetShortURL_PreservesPathAndQuery(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	pathA, _ := shorten(t, service, testUserID, "https://example.com/a?order=first")
	pathB, _ := shorten(t, service, testUserID, "https://example.com/b?order=first")
	queryB, _ := shorten(t, service, testUserID, "https://example.com/a?order=second")

	if pathA == pathB || pathA == queryB {
		t.Fatal("different paths or query parameters must not share a short key")
	}
}

func TestGetShortURL_RejectsInvalidUserID(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	for _, userID := range []string{"", "   "} {
		_, _, err := service.GetShortURL(userID, parseURL(t, "https://example.com"))
		if !errors.Is(err, ErrInvalidUserID) {
			t.Fatalf("GetShortURL(%q) error = %v, want ErrInvalidUserID", userID, err)
		}
	}
}

func TestGetShortURL_RejectsInvalidURL(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	testCases := []*url.URL{
		nil,
		{},
		{Scheme: "htt", Host: "example.com"},
		{Scheme: "https"},
	}

	for _, invalidURL := range testCases {
		_, _, err := service.GetShortURL(testUserID, invalidURL)
		if !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("GetShortURL(%v) error = %v, want ErrInvalidURL", invalidURL, err)
		}
	}
}

func TestGetLongURL_UnknownKeyReturnsDomainError(t *testing.T) {
	skipExpectedFailure(t)

	service := newShortener()
	_, err := service.GetLongURL("Unknown")
	if !errors.Is(err, ErrURLMappingNotFound) {
		t.Fatalf("GetLongURL() error = %v, want ErrURLMappingNotFound", err)
	}
}

func TestGetCustomShortURL_AvailableKeyUsesRequestedKey(t *testing.T) {
	skipExpectedFailure(t)

	service := newFutureShortener()
	shortKey, created, err := service.GetCustomShortURL(
		testUserID,
		parseURL(t, "https://example.com/alpha"),
		"Custom1",
	)
	if err != nil {
		t.Fatalf("GetCustomShortURL() returned an unexpected error: %v", err)
	}
	if shortKey != "Custom1" {
		t.Fatalf("GetCustomShortURL() = %q, want %q", shortKey, "Custom1")
	}
	if !created {
		t.Fatal("GetCustomShortURL() created = false, want true")
	}
}

func TestUpdateLongURL_CreatorCanUpdateMapping(t *testing.T) {
	skipExpectedFailure(t)

	service := newFutureShortener()
	shortKey, _, err := service.GetShortURL(
		testUserID,
		parseURL(t, "https://example.com/before"),
	)
	if err != nil {
		t.Fatalf("GetShortURL() returned an unexpected error: %v", err)
	}

	want := parseURL(t, "https://example.com/after")
	if err := service.UpdateLongURL(testUserID, shortKey, want); err != nil {
		t.Fatalf("UpdateLongURL() returned an unexpected error: %v", err)
	}
	got, err := service.GetLongURL(shortKey)
	if err != nil {
		t.Fatalf("GetLongURL() returned an unexpected error: %v", err)
	}
	if got.String() != want.String() {
		t.Fatalf("GetLongURL() = %q, want %q", got, want)
	}
}

func TestDeleteURL_CreatorCanDeleteMapping(t *testing.T) {
	skipExpectedFailure(t)

	service := newFutureShortener()
	shortKey, _, err := service.GetShortURL(
		testUserID,
		parseURL(t, "https://example.com/alpha"),
	)
	if err != nil {
		t.Fatalf("GetShortURL() returned an unexpected error: %v", err)
	}

	if err := service.DeleteURL(testUserID, shortKey); err != nil {
		t.Fatalf("DeleteURL() returned an unexpected error: %v", err)
	}
	_, err = service.GetLongURL(shortKey)
	if !errors.Is(err, ErrURLMappingNotFound) {
		t.Fatalf("GetLongURL() error = %v, want ErrURLMappingNotFound", err)
	}
}

type ownershipRepository struct {
	byShortKey map[string]URLMapping
	byLongURL  map[string]URLMapping
	owners     map[string]map[string]bool
}

func newOwnershipRepository() *ownershipRepository {
	return &ownershipRepository{
		byShortKey: make(map[string]URLMapping),
		byLongURL:  make(map[string]URLMapping),
		owners:     make(map[string]map[string]bool),
	}
}

func (r *ownershipRepository) Save(_ context.Context, mapping URLMapping) error {
	r.byShortKey[mapping.ShortKey] = mapping
	r.byLongURL[mapping.LongURL.String()] = mapping
	return nil
}

func (r *ownershipRepository) AddOwner(_ context.Context, ownership URLOwnership) error {
	if r.owners[ownership.ShortKey] == nil {
		r.owners[ownership.ShortKey] = make(map[string]bool)
	}
	r.owners[ownership.ShortKey][ownership.UserID] = true
	return nil
}

func (r *ownershipRepository) FindByShortKey(
	_ context.Context,
	shortKey string,
) (URLMapping, error) {
	mapping, ok := r.byShortKey[shortKey]
	if !ok {
		return URLMapping{}, ErrURLMappingNotFound
	}
	return mapping, nil
}

func (r *ownershipRepository) FindByLongURL(
	_ context.Context,
	longURL *url.URL,
) (URLMapping, error) {
	mapping, ok := r.byLongURL[longURL.String()]
	if !ok {
		return URLMapping{}, ErrURLMappingNotFound
	}
	return mapping, nil
}

func (r *ownershipRepository) hasOwner(userID, shortKey string) bool {
	return r.owners[shortKey][userID]
}
