package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every case pins the whole URL rather than just the port. An implementation
// that returned the address unchanged, or that dialed the wildcard host it was
// given, produces a string that a port-only assertion would happily accept.
func TestProbeURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		addr string
		want string
	}{
		{"the default listen address", ":8080", "http://127.0.0.1:8080/healthz"},
		{"an explicit wildcard", "0.0.0.0:8080", "http://127.0.0.1:8080/healthz"},
		{"an IPv6 wildcard", "[::]:8080", "http://127.0.0.1:8080/healthz"},
		{"an already-loopback address", "127.0.0.1:9000", "http://127.0.0.1:9000/healthz"},
		{"a non-default port", ":3000", "http://127.0.0.1:3000/healthz"},
		{"ADDR unset", "", "http://127.0.0.1:8080/healthz"},
		{"junk", "not-an-address", "http://127.0.0.1:8080/healthz"},
		{"a host with no port", "localhost", "http://127.0.0.1:8080/healthz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeURL(tc.addr); got != tc.want {
				t.Errorf("probeURL(%q) = %q, want %q", tc.addr, got, tc.want)
			}
		})
	}
}

func TestRunHealthCheck(t *testing.T) {
	t.Run("a healthy server exits 0", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if got := runHealthCheck(srv.URL); got != 0 {
			t.Errorf("exit code = %d, want 0", got)
		}
	})

	// This is the degraded case: the server is up and answering, but its
	// database ping failed. Docker must see that as unhealthy.
	t.Run("a degraded server exits 1", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		if got := runHealthCheck(srv.URL); got != 1 {
			t.Errorf("exit code = %d, want 1", got)
		}
	})

	t.Run("nothing listening exits 1", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close() // frees the port, so the dial is refused rather than timing out

		if got := runHealthCheck(url); got != 1 {
			t.Errorf("exit code = %d, want 1", got)
		}
	})
}
