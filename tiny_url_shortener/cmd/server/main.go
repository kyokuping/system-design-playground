package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/handler"
	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/shortener"
)

func main() {
	address := os.Getenv("HTTP_ADDR")
	if address == "" {
		address = ":8080"
	}

	log.Printf("tiny URL shortener listening on %s", address)
	if err := http.ListenAndServe(address, newHandler()); err != nil {
		log.Fatal(err)
	}
}

func newHandler() http.Handler {
	repository := shortener.NewMemoryRepository()
	allocator := shortener.NewMemoryRangeAllocator(nil)
	ids := shortener.NewDistributedIDGenerator(allocator, 1_000)
	service := shortener.New(repository, shortener.NewIDKeyGenerator(ids))
	baseURL := strings.TrimSpace(os.Getenv("SHORT_URL_BASE"))
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	urlHandler := handler.NewURLHandler(service, baseURL)

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/healthz" {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		urlHandler.ServeHTTP(response, request)
	})
}
