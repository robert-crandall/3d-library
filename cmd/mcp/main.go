package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/robert-crandall/go-home-server/apiclient"
	foundationmcp "github.com/robert-crandall/go-home-server/mcp"

	"github.com/robert-crandall/3d-library/internal/app"
)

const currentUserPath = "/api/auth/me"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	name := foundationmcp.AppName()
	srv := foundationmcp.New(name+"-mcp", app.Version)

	if !needsClient(args) {
		return srv.Run(ctx, args)
	}

	client, err := apiclient.FromConfig(name)
	if err != nil {
		return err
	}
	// Missing config is static, so every operational mode loads it now. App
	// availability and token validity are transient: shell commands check them
	// because list is otherwise in-memory, but stdio waits for each tool's own
	// API call so a desktop client that starts early does not die permanently.
	if shouldPreflight(args) {
		if err := preflight(ctx, client); err != nil {
			return err
		}
	}
	registerTools(srv, client)

	return srv.Run(ctx, args)
}

func needsClient(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "serve", "list":
		return true
	case "call":
		return callIsWellFormed(args[1:])
	default:
		return false
	}
}

func shouldPreflight(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "list":
		return true
	case "call":
		return callIsWellFormed(args[1:])
	default:
		return false
	}
}

// callIsWellFormed mirrors the pinned harness's public call grammar only to
// decide whether auth may run. Server.Run remains the parser and error source.
func callIsWellFormed(args []string) bool {
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return false
	}
	if len(args) == 1 {
		return true
	}
	if len(args) != 3 || args[1] != "--input" || args[2] == "" {
		return false
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal([]byte(args[2]), &obj) == nil && obj != nil
}

func preflight(ctx context.Context, client *apiclient.Client) error {
	if err := client.Do(ctx, http.MethodGet, currentUserPath, nil, nil); err != nil {
		return fmt.Errorf("mcp: connect to app: %w", err)
	}
	return nil
}

func registerTools(_ *foundationmcp.Server, _ *apiclient.Client) {
	// Add thin, API-backed tools here. README.md has a complete example.
}
