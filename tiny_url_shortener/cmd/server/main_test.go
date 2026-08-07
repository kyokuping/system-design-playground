package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestHealthzReturnsNoContent(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	newHandler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("GET /healthz status = %d, want %d", response.Code, http.StatusNoContent)
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
