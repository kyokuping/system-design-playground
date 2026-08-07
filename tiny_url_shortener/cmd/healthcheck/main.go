package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

const defaultHealthcheckURL = "http://127.0.0.1:8080/healthz"

func main() {
	endpoint := os.Getenv("HEALTHCHECK_URL")
	if endpoint == "" {
		endpoint = defaultHealthcheckURL
	}

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck request failed: %v\n", err)
		os.Exit(1)
	}
	if err := response.Body.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close healthcheck response: %v\n", err)
		os.Exit(1)
	}

	if response.StatusCode != http.StatusNoContent {
		fmt.Fprintf(os.Stderr, "healthcheck status = %d, want %d\n", response.StatusCode, http.StatusNoContent)
		os.Exit(1)
	}
}
