package main

import (
	"log"
	"net/http"
	"os"
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
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/healthz" {
			http.NotFound(response, request)
			return
		}

		response.WriteHeader(http.StatusNoContent)
	})
}
