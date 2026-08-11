package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConfiguredRangeSizeUsesDefault(t *testing.T) {
	t.Setenv("ID_RANGE_SIZE", "")
	got, err := configuredRangeSize()
	if err != nil {
		t.Fatalf("configuredRangeSize() error = %v", err)
	}
	if got != 1_000 {
		t.Fatalf("configuredRangeSize() = %d, want 1000", got)
	}
}

func TestConfiguredRangeSizeReadsEnvironment(t *testing.T) {
	t.Setenv("ID_RANGE_SIZE", "250")
	got, err := configuredRangeSize()
	if err != nil {
		t.Fatalf("configuredRangeSize() error = %v", err)
	}
	if got != 250 {
		t.Fatalf("configuredRangeSize() = %d, want 250", got)
	}
}

func TestConfiguredRangeSizeRejectsInvalidValue(t *testing.T) {
	for _, value := range []string{"0", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ID_RANGE_SIZE", value)
			if _, err := configuredRangeSize(); err == nil {
				t.Fatal("configuredRangeSize() error = nil")
			}
		})
	}
}

func TestConfiguredServerRole(t *testing.T) {
	testCases := []struct {
		value string
		want  serverRole
	}{
		{value: "", want: roleAll},
		{value: "COMMAND", want: roleCommand},
		{value: " redirect ", want: roleRedirect},
	}
	for _, testCase := range testCases {
		t.Run(testCase.value, func(t *testing.T) {
			t.Setenv("SERVER_ROLE", testCase.value)
			got, err := configuredServerRole()
			if err != nil {
				t.Fatalf("configuredServerRole() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("configuredServerRole() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestConfiguredServerRoleRejectsUnknownRole(t *testing.T) {
	t.Setenv("SERVER_ROLE", "worker")
	if _, err := configuredServerRole(); err == nil {
		t.Fatal("configuredServerRole() error = nil")
	}
}

func TestConfiguredBaseURL_ReadsEnvironment(t *testing.T) {
	t.Setenv("SHORT_URL_BASE", " https://tiny.example ")
	got, err := configuredBaseURL(roleCommand)
	if err != nil {
		t.Fatalf("configuredBaseURL() error = %v", err)
	}
	if got != "https://tiny.example" {
		t.Fatalf("configuredBaseURL() = %q, want %q", got, "https://tiny.example")
	}
}

// An unset base URL used to fall back to localhost, which silently handed every
// caller an unreachable short URL instead of failing at startup.
func TestConfiguredBaseURL_RejectsMissingValueForURLMintingRoles(t *testing.T) {
	for _, role := range []serverRole{roleAll, roleCommand} {
		t.Run(string(role), func(t *testing.T) {
			t.Setenv("SHORT_URL_BASE", "")
			if _, err := configuredBaseURL(role); err == nil {
				t.Fatal("configuredBaseURL() error = nil, want an error")
			}
		})
	}
}

// The redirect role never mints short URLs, so it must start without the base URL.
func TestConfiguredBaseURL_AllowsMissingValueForRedirectRole(t *testing.T) {
	t.Setenv("SHORT_URL_BASE", "")
	got, err := configuredBaseURL(roleRedirect)
	if err != nil {
		t.Fatalf("configuredBaseURL() error = %v", err)
	}
	if got != "" {
		t.Fatalf("configuredBaseURL() = %q, want empty", got)
	}
}

// A failed bind used to be logged and then swallowed, so a server that never
// started still exited 0 and looked healthy to whatever supervised it.
func TestServe_ReturnsErrorWhenAddressIsUnavailable(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer func() { _ = occupied.Close() }()

	server := &http.Server{Addr: occupied.Addr().String(), ReadHeaderTimeout: time.Second}
	if err := serve(context.Background(), server, roleAll); err == nil {
		t.Fatal("serve() error = nil, want an error")
	}
}

func TestServe_ReturnsNilAfterShutdown(t *testing.T) {
	shutdown, stop := context.WithCancel(context.Background())
	server := &http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second}
	served := make(chan error, 1)
	go func() { served <- serve(shutdown, server, roleAll) }()

	stop()
	if err := <-served; err != nil {
		t.Fatalf("serve() error = %v, want nil", err)
	}
}

func TestRoleHandlersExposeOnlyTheirTrafficClass(t *testing.T) {
	probe := func(context.Context) error { return nil }

	commandResponse := httptest.NewRecorder()
	roleHandler(roleCommand, nil, "https://tiny.url", probe).ServeHTTP(
		commandResponse,
		httptest.NewRequest(http.MethodGet, "/Ab12Cd3", nil),
	)
	if commandResponse.Code != http.StatusNotFound {
		t.Fatalf("command GET redirect status = %d, want %d", commandResponse.Code, http.StatusNotFound)
	}

	redirectResponse := httptest.NewRecorder()
	roleHandler(roleRedirect, nil, "", probe).ServeHTTP(
		redirectResponse,
		httptest.NewRequest(http.MethodPost, "/api/v1/short-urls", nil),
	)
	if redirectResponse.Code != http.StatusNotFound {
		t.Fatalf("redirect POST shorten status = %d, want %d", redirectResponse.Code, http.StatusNotFound)
	}
}

func TestHealthzReturnsNoContent(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	probeCalled := false

	rootHandler(nil, "", func(context.Context) error {
		probeCalled = true
		return nil
	}).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("GET /healthz status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if !probeCalled {
		t.Fatal("GET /healthz did not call readiness probe")
	}
}

func TestHealthzReturnsServiceUnavailableWhenDependencyIsUnavailable(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	rootHandler(nil, "", func(context.Context) error {
		return errors.New("postgres unavailable")
	}).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /healthz status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestOtherMethodOrPathReturnsNotFound(t *testing.T) {
	testCases := []struct {
		name   string
		method string
		path   string
	}{
		{name: "other method", method: http.MethodPost, path: "/healthz"},
		{name: "other path", method: http.MethodGet, path: "/unknown"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, testCase.path, nil)
			response := httptest.NewRecorder()

			newHandler().ServeHTTP(response, request)

			if response.Code != http.StatusNotFound {
				t.Fatalf("%s %s status = %d, want %d", testCase.method, testCase.path, response.Code, http.StatusNotFound)
			}
		})
	}
}
