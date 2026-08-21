package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	endpoint, err := healthcheckURL(os.Getenv("HTTP_ADDR"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		os.Exit(1)
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

// healthcheckURL builds the container-local probe URL from the address the server
// binds to. The probe always targets loopback, so only the port carries over:
// taking it from HTTP_ADDR keeps the probe from outliving a port change.
func healthcheckURL(listenAddr string) (string, error) {
	_, port, err := net.SplitHostPort(strings.TrimSpace(listenAddr))
	if err != nil {
		return "", fmt.Errorf("HTTP_ADDR %q is not a listen address: %w", listenAddr, err)
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/-/healthz", nil
}
