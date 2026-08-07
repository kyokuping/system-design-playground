package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/shortener"
)

const testShortURLBase = "https://tiny.url"

func wireTestHandler(service URLService, baseURL string) http.Handler {
	return NewURLHandler(service, baseURL)
}

func skipExpectedFailure(t *testing.T) {
	t.Helper()
}

func TestPostShorten_NewMappingReturnsCreatedAndShortURL(t *testing.T) {
	skipExpectedFailure(t)

	service := &stubURLService{shortKey: "Ab12Cd3", created: true}
	response := performShortenRequest(t, service, `{
		"user_id": "user-123",
		"url": "https://example.com/very/long/path"
	}`)

	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/short-urls status = %d, want %d", response.Code, http.StatusCreated)
	}
	if got, want := response.Header().Get("Location"), "/api/v1/short-urls/Ab12Cd3"; got != want {
		t.Fatalf("Location header = %q, want %q", got, want)
	}
	assertMappingResponse(t, response, "Ab12Cd3", "https://tiny.url/Ab12Cd3", "https://example.com/very/long/path")
}

func TestPostShorten_ExistingMappingReturnsOKAndShortURL(t *testing.T) {
	skipExpectedFailure(t)

	service := &stubURLService{shortKey: "Ab12Cd3", created: false}
	response := performShortenRequest(t, service, `{
		"user_id": "user-456",
		"url": "https://example.com/very/long/path"
	}`)

	if response.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/short-urls status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Location"); got != "" {
		t.Fatalf("Location header = %q, want empty for reused mapping", got)
	}
	assertMappingResponse(t, response, "Ab12Cd3", "https://tiny.url/Ab12Cd3", "https://example.com/very/long/path")
}

func TestPostShorten_InvalidInputReturnsBadRequest(t *testing.T) {
	skipExpectedFailure(t)

	for _, domainError := range []error{shortener.ErrInvalidUserID, shortener.ErrInvalidURL} {
		service := &stubURLService{shortenErr: domainError}
		response := performShortenRequest(t, service, `{
			"user_id": "user-123",
			"url": "https://example.com"
		}`)

		if response.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v1/short-urls error %v status = %d, want %d", domainError, response.Code, http.StatusBadRequest)
		}
	}
}

func TestPostShorten_InvalidJSONReturnsBadRequest(t *testing.T) {
	skipExpectedFailure(t)

	response := performShortenRequest(t, &stubURLService{}, `{invalid`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/v1/short-urls status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestPostShorten_TrimsURLWhitespaceBeforeCallingService(t *testing.T) {
	skipExpectedFailure(t)

	service := &stubURLService{shortKey: "Ab12Cd3", created: true}
	response := performShortenRequest(t, service, `{
		"user_id": "user-123",
		"url": "  https://example.com/path  "
	}`)

	if response.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/short-urls status = %d, want %d", response.Code, http.StatusCreated)
	}
	if service.receivedLongURL == nil {
		t.Fatal("GetShortURL() received a nil URL")
	}
	if got, want := service.receivedLongURL.String(), "https://example.com/path"; got != want {
		t.Fatalf("GetShortURL() URL = %q, want %q", got, want)
	}
}

func TestPostShorten_KeyGenerationFailureReturnsInternalServerError(t *testing.T) {
	skipExpectedFailure(t)

	service := &stubURLService{shortenErr: shortener.ErrKeyGenerationFailed}
	response := performShortenRequest(t, service, `{
		"user_id": "user-123",
		"url": "https://example.com"
	}`)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("POST /api/v1/short-urls status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}

func TestGetShortURL_ExistingKeyReturnsTemporaryRedirect(t *testing.T) {
	skipExpectedFailure(t)

	originalURL := mustParseURL(t, "https://example.com/very/long/path")
	service := &stubURLService{longURL: originalURL}
	response := performResolveRequest(t, service, "Ab12Cd3")

	if response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GET /{shortKey} status = %d, want %d", response.Code, http.StatusTemporaryRedirect)
	}
	if got := response.Header().Get("Location"); got != originalURL.String() {
		t.Fatalf("Location header = %q, want %q", got, originalURL)
	}
}

func TestGetShortURL_UnknownKeyReturnsNotFound(t *testing.T) {
	skipExpectedFailure(t)

	service := &stubURLService{resolveErr: shortener.ErrURLMappingNotFound}
	response := performResolveRequest(t, service, "Unknown")

	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /{shortKey} status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestGetShortURL_ExpiredKeyReturnsGone(t *testing.T) {
	skipExpectedFailure(t)

	service := &stubURLService{resolveErr: shortener.ErrURLMappingExpired}
	response := performResolveRequest(t, service, "Expired")

	if response.Code != http.StatusGone {
		t.Fatalf("GET /{shortKey} status = %d, want %d", response.Code, http.StatusGone)
	}
}

func TestGetURLMapping_ReturnsJSONWithoutRedirect(t *testing.T) {
	skipExpectedFailure(t)

	longURL := mustParseURL(t, "https://example.com/very/long/path")
	service := &stubURLService{mappingURL: longURL}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/short-urls/Ab12Cd3", nil)
	response := httptest.NewRecorder()

	wireTestHandler(service, testShortURLBase).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/short-urls/{shortKey} status = %d, want %d", response.Code, http.StatusOK)
	}
	assertMappingResponse(t, response, "Ab12Cd3", "https://tiny.url/Ab12Cd3", longURL.String())
}

func TestGetURLMapping_UnknownKeyReturnsNotFound(t *testing.T) {
	skipExpectedFailure(t)

	service := &stubURLService{mappingErr: shortener.ErrURLMappingNotFound}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/short-urls/Unknown", nil)
	response := httptest.NewRecorder()

	wireTestHandler(service, testShortURLBase).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v1/short-urls/{shortKey} status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func performShortenRequest(
	t *testing.T,
	service URLService,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/short-urls", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	wireTestHandler(service, testShortURLBase).ServeHTTP(response, request)
	return response
}

func performResolveRequest(
	t *testing.T,
	service URLService,
	shortKey string,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/"+shortKey, nil)
	response := httptest.NewRecorder()
	wireTestHandler(service, testShortURLBase).ServeHTTP(response, request)
	return response
}

func assertMappingResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantKey string,
	wantShortURL string,
	wantLongURL string,
) {
	t.Helper()

	var body struct {
		ShortKey string `json:"short_key"`
		ShortURL string `json:"short_url"`
		LongURL  string `json:"long_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body.ShortKey != wantKey || body.ShortURL != wantShortURL || body.LongURL != wantLongURL {
		t.Fatalf("mapping response = %+v, want key=%q short_url=%q long_url=%q", body, wantKey, wantShortURL, wantLongURL)
	}
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", rawURL, err)
	}
	return parsedURL
}

type stubURLService struct {
	shortKey        string
	created         bool
	shortenErr      error
	longURL         *url.URL
	resolveErr      error
	mappingURL      *url.URL
	mappingErr      error
	receivedUserID  string
	receivedLongURL *url.URL
}

func (s *stubURLService) GetShortURL(
	userID string,
	longURL *url.URL,
) (string, bool, error) {
	s.receivedUserID = userID
	s.receivedLongURL = longURL
	return s.shortKey, s.created, s.shortenErr
}

func (s *stubURLService) GetLongURL(_ string) (*url.URL, error) {
	return s.longURL, s.resolveErr
}

func (s *stubURLService) GetURLMapping(_ string) (*url.URL, error) {
	if s.mappingURL == nil && s.mappingErr == nil {
		return s.receivedLongURL, nil
	}
	return s.mappingURL, s.mappingErr
}

var _ URLService = (*stubURLService)(nil)
