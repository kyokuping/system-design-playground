package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/domain"
)

const shortURLsPath = "/api/v1/short-urls"

type URLAPIService interface {
	GetShortURL(userID string, longURL *url.URL) (shortKey string, created bool, err error)
	GetURLMapping(shortKey string) (*url.URL, error)
}

type URLRedirectService interface {
	GetLongURL(shortKey string) (*url.URL, error)
}

type URLService interface {
	URLAPIService
	URLRedirectService
}

type URLHandler struct {
	api          URLAPIService
	redirects    URLRedirectService
	shortURLBase string
}

func NewURLHandler(service URLService, shortURLBase string) http.Handler {
	return &URLHandler{api: service, redirects: service, shortURLBase: strings.TrimRight(shortURLBase, "/")}
}

func NewCommandHandler(service URLAPIService, shortURLBase string) http.Handler {
	return &URLHandler{api: service, shortURLBase: strings.TrimRight(shortURLBase, "/")}
}

func NewRedirectHandler(service URLRedirectService) http.Handler {
	return &URLHandler{redirects: service}
}

func (h *URLHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch {
	case h.api != nil && request.Method == http.MethodPost && request.URL.Path == shortURLsPath:
		h.shorten(response, request)
	case h.api != nil && request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, shortURLsPath+"/"):
		h.getMapping(response, request)
	case h.redirects != nil && request.Method == http.MethodGet:
		h.resolve(response, request)
	default:
		http.NotFound(response, request)
	}
}

func (h *URLHandler) shorten(response http.ResponseWriter, request *http.Request) {
	var body struct {
		UserID string `json:"user_id"`
		URL    string `json:"url"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	// Decode the first JSON value into the body struct.
	if err := decoder.Decode(&body); err != nil {
		http.Error(response, "invalid JSON request", http.StatusBadRequest)
		return
	}
	// Verify there is no trailing data. Decoding into an empty struct, should reach the end of the stream.
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(response, "invalid JSON request", http.StatusBadRequest)
		return
	}

	trimmedURL := strings.TrimSpace(body.URL)
	parsed, err := url.Parse(trimmedURL)
	if err != nil {
		http.Error(response, "invalid URL", http.StatusBadRequest)
		return
	}
	shortKey, created, err := h.api.GetShortURL(body.UserID, parsed)
	if err != nil {
		writeDomainError(response, err)
		return
	}
	longURL, err := h.api.GetURLMapping(shortKey)
	if err != nil {
		writeDomainError(response, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	response.Header().Set("Content-Type", "application/json")
	if created {
		response.Header().Set("Location", shortURLsPath+"/"+shortKey)
	}
	response.WriteHeader(status)
	h.writeMapping(response, shortKey, longURL)
}

func (h *URLHandler) resolve(response http.ResponseWriter, request *http.Request) {
	shortKey, ok := pathKey(request.URL.Path, "/")
	if !ok {
		http.NotFound(response, request)
		return
	}
	longURL, err := h.redirects.GetLongURL(shortKey)
	if err != nil {
		writeDomainError(response, err)
		return
	}
	http.Redirect(response, request, longURL.String(), http.StatusTemporaryRedirect)
}

func (h *URLHandler) getMapping(response http.ResponseWriter, request *http.Request) {
	shortKey, ok := pathKey(request.URL.Path, shortURLsPath+"/")
	if !ok {
		http.NotFound(response, request)
		return
	}
	longURL, err := h.api.GetURLMapping(shortKey)
	if err != nil {
		writeDomainError(response, err)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	h.writeMapping(response, shortKey, longURL)
}

func (h *URLHandler) writeMapping(response http.ResponseWriter, shortKey string, longURL *url.URL) {
	_ = json.NewEncoder(response).Encode(struct {
		ShortKey string `json:"short_key"`
		ShortURL string `json:"short_url"`
		LongURL  string `json:"long_url"`
	}{
		ShortKey: shortKey,
		ShortURL: h.shortURLBase + "/" + shortKey,
		LongURL:  longURL.String(),
	})
}

func pathKey(path, prefix string) (string, bool) {
	shortKey := strings.TrimPrefix(path, prefix)
	return shortKey, shortKey != "" && !strings.Contains(shortKey, "/")
}

func writeDomainError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidUserID), errors.Is(err, domain.ErrInvalidURL), errors.Is(err, domain.ErrInvalidShortKey):
		http.Error(response, "invalid request", http.StatusBadRequest)
	case errors.Is(err, domain.ErrURLMappingNotFound):
		http.Error(response, "URL mapping not found", http.StatusNotFound)
	case errors.Is(err, domain.ErrURLMappingExpired):
		http.Error(response, "URL mapping expired", http.StatusGone)
	case errors.Is(err, domain.ErrShortURLConflict):
		http.Error(response, "short URL already exists", http.StatusConflict)
	case errors.Is(err, domain.ErrForbidden):
		http.Error(response, "forbidden", http.StatusForbidden)
	default:
		http.Error(response, "internal server error", http.StatusInternalServerError)
	}
}
