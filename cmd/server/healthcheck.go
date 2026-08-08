package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// The container's HEALTHCHECK. distroless has no shell and no curl, so the
// binary probes itself: `/app healthcheck` exits 0 when /healthz answers 200.
//
// Two things make it correct, and both are easy to get wrong:
//
//   - main() dispatches on os.Args[1] BEFORE config.Load or db.Migrate. A probe
//     that loaded config would need DATABASE_URL, and one that reached Migrate
//     would run goose Up against production every 30 seconds.
//   - it reads ADDR with os.Getenv rather than through config, for the same
//     reason.

// probeURL turns a listen address into the URL to probe.
//
// Only the port carries over. ADDR is a *listen* address - ":8080",
// "0.0.0.0:8080" and "[::]:8080" are all wildcards, and none of them is dialable
// as written - so the host is always 127.0.0.1, which is where the server is
// from inside its own container.
//
// Anything unparseable falls back to port 8080, matching the default
// server.Run uses for an empty addr. There's no error return because there's no
// better answer: if ADDR is junk the server never bound anything either, and
// "probe 8080 and fail" is the outcome either way.
func probeURL(addr string) string {
	port := "8080"
	if _, p, err := net.SplitHostPort(addr); err == nil && p != "" {
		port = p
	}
	return "http://127.0.0.1:" + port + "/healthz"
}

// runHealthCheck returns the process exit code: 0 only for a 200.
//
// /healthz already returns 503 when the database ping fails (main wires
// server.Options.HealthCheck to pool.Ping), so a container that can still serve
// but has lost Postgres reports unhealthy without any extra logic here.
func runHealthCheck(url string) int {
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: %s returned %s\n", url, resp.Status)
		return 1
	}
	return 0
}
