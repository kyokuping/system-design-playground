package main

import "testing"

func TestHealthcheckURL_DerivesPortFromListenAddr(t *testing.T) {
	testCases := []struct {
		addr string
		want string
	}{
		{addr: ":8080", want: "http://127.0.0.1:8080/healthz"},
		{addr: ":9090", want: "http://127.0.0.1:9090/healthz"},
		{addr: "0.0.0.0:9090", want: "http://127.0.0.1:9090/healthz"},
		{addr: "127.0.0.1:9090", want: "http://127.0.0.1:9090/healthz"},
		{addr: "[::]:9090", want: "http://127.0.0.1:9090/healthz"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.addr, func(t *testing.T) {
			got, err := healthcheckURL(testCase.addr)
			if err != nil {
				t.Fatalf("healthcheckURL(%q) error = %v", testCase.addr, err)
			}
			if got != testCase.want {
				t.Fatalf("healthcheckURL(%q) = %q, want %q", testCase.addr, got, testCase.want)
			}
		})
	}
}

// A probe that cannot name the port must fail loudly instead of guessing one and
// reporting a healthy container that nobody can reach.
func TestHealthcheckURL_RejectsUnusableListenAddr(t *testing.T) {
	for _, addr := range []string{"", "not-an-address"} {
		t.Run(addr, func(t *testing.T) {
			if _, err := healthcheckURL(addr); err == nil {
				t.Fatalf("healthcheckURL(%q) error = nil, want an error", addr)
			}
		})
	}
}
