package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robert-crandall/go-home-server/apiclient"
	foundationmcp "github.com/robert-crandall/go-home-server/mcp"
)

func TestCommandDispatch(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		needsClient   bool
		shouldConnect bool
	}{
		{name: "no arguments", needsClient: true},
		{name: "serve", args: []string{"serve"}, needsClient: true},
		{name: "list", args: []string{"list"}, needsClient: true, shouldConnect: true},
		{name: "list JSON", args: []string{"list", "--json"}, needsClient: true, shouldConnect: true},
		{name: "call", args: []string{"call", "tool"}, needsClient: true, shouldConnect: true},
		{name: "call with input", args: []string{"call", "tool", "--input", `{}`}, needsClient: true, shouldConnect: true},
		{name: "call missing tool", args: []string{"call"}},
		{name: "call flag as tool", args: []string{"call", "--input", `{}`}},
		{name: "call missing input", args: []string{"call", "tool", "--input"}},
		{name: "call invalid input", args: []string{"call", "tool", "--input", `{`}},
		{name: "call non-object input", args: []string{"call", "tool", "--input", `[]`}},
		{name: "call unknown flag", args: []string{"call", "tool", "--inpt", `{}`}},
		{name: "call extra argument", args: []string{"call", "tool", "extra"}},
		{name: "call duplicate input", args: []string{"call", "tool", "--input", `{}`, "--input", `{}`}},
		{name: "help", args: []string{"help"}},
		{name: "short help", args: []string{"-h"}},
		{name: "long help", args: []string{"--help"}},
		{name: "unknown", args: []string{"bogus"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsClient(tc.args); got != tc.needsClient {
				t.Errorf("needsClient(%q) = %v, want %v", tc.args, got, tc.needsClient)
			}
			if got := shouldPreflight(tc.args); got != tc.shouldConnect {
				t.Errorf("shouldPreflight(%q) = %v, want %v", tc.args, got, tc.shouldConnect)
			}
		})
	}
}

func TestMalformedCallDoesNotNeedConfig(t *testing.T) {
	for _, args := range [][]string{
		{"call"},
		{"call", "--input", `{}`},
		{"call", "tool", "--input"},
		{"call", "tool", "--input", `{`},
		{"call", "tool", "--input", `[]`},
		{"call", "tool", "--inpt", `{}`},
		{"call", "tool", "extra"},
		{"call", "tool", "--input", `{}`, "--input", `{}`},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, configPath := isolateConfigSources(t)

			err := run(context.Background(), args)
			if err == nil {
				t.Fatalf("%v succeeded", args)
			}
			if strings.Contains(err.Error(), configPath) {
				t.Errorf("local syntax error was hidden by config error %q", err)
			}
		})
	}
}

func TestOperationalCommandsWithoutTokenSurfaceConfigHelp(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "list JSON", args: []string{"list", "--json"}},
		{name: "serve", args: []string{"serve"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, configPath := isolateConfigSources(t)

			err := run(context.Background(), tc.args)
			if err == nil {
				t.Fatalf("%v succeeded without an API token", tc.args)
			}
			if !strings.Contains(err.Error(), configPath) {
				t.Errorf("error %q does not name config path %q", err, configPath)
			}
			if !strings.Contains(err.Error(), "POST /api/tokens") {
				t.Errorf("error %q does not explain how to mint a token", err)
			}
		})
	}
}

func TestHelpDoesNotNeedConfig(t *testing.T) {
	isolateConfigSources(t)

	if err := run(context.Background(), []string{"help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
}

func TestConnectToAppAuthenticatesCurrentUser(t *testing.T) {
	name, _ := isolateConfigSources(t)
	const token = "pat_7_configured-token"

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != currentUserPath {
			t.Errorf("path = %s, want %s", r.URL.Path, currentUserPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q, want configured bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"email":"user@example.com"}`))
	}))
	defer ts.Close()

	writeAppConfig(t, name, apiclient.FileConfig{AppURL: ts.URL, Token: token})

	client, err := apiclient.FromConfig(name)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	if client == nil {
		t.Fatal("FromConfig returned a nil client")
	}
	if err := preflight(context.Background(), client); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want exactly one preflight", requests)
	}
}

func TestConnectToAppRejectsUnauthorizedWithoutLeakingToken(t *testing.T) {
	name, _ := isolateConfigSources(t)
	const token = "pat_9_supersecret"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"rejected ` + r.Header.Get("Authorization") + `"}`))
	}))
	defer ts.Close()

	writeAppConfig(t, name, apiclient.FileConfig{AppURL: ts.URL, Token: token})

	client, err := apiclient.FromConfig(name)
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	err = preflight(context.Background(), client)
	if err == nil {
		t.Fatal("preflight succeeded with a refused token")
	}
	got := err.Error()
	if !strings.Contains(got, currentUserPath) || !strings.Contains(got, "401") {
		t.Errorf("error %q should name the endpoint and status", got)
	}
	if strings.Contains(got, token) {
		t.Fatalf("error leaked the API token: %q", got)
	}
}

func isolateConfigSources(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("MCP_APP_URL", "")
	t.Setenv("MCP_APP_TOKEN", "")
	t.Chdir(t.TempDir())

	name := foundationmcp.AppName()
	return name, filepath.Join(configDir, name+".json")
}

func writeAppConfig(t *testing.T, name string, cfg apiclient.FileConfig) {
	t.Helper()

	dir := os.Getenv("XDG_CONFIG_HOME")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
