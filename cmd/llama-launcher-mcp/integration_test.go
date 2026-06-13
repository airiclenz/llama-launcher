package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// echoArgsCLI writes a fake llama-launcher that echoes its arguments, so a tool
// call's full command line can be asserted end-to-end through the MCP layer.
func echoArgsCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-llama-launcher")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// startAdapter wires the real MCP server + allowlist behind an httptest server
// and returns a connected client session.
func startAdapter(t *testing.T, cfg *config, allow []allowEntry) *mcp.ClientSession {
	t.Helper()
	server := newServer(cfg)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(allowlistMiddleware(allow, handler))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func loopbackAllow(t *testing.T) []allowEntry {
	t.Helper()
	allow, err := resolveAllowlist(nil, nil) // loopback only
	if err != nil {
		t.Fatal(err)
	}
	return allow
}

func callText(t *testing.T, s *mcp.ClientSession, name string, args any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool(%s): no content", name)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s): content %T", name, res.Content[0])
	}
	return tc.Text
}

func TestEndToEndToolsForwardCommands(t *testing.T) {
	cfg := &config{llamaLauncherBin: echoArgsCLI(t)}
	s := startAdapter(t, cfg, loopbackAllow(t))

	cases := []struct {
		tool string
		args any
		want string
	}{
		{"server_status", map[string]any{}, "status --json"},
		{"list_profiles", map[string]any{}, "list --json"},
		{"load_profile", map[string]any{"name": "qwen", "restart": true}, "load qwen --restart"},
		{"load_profile", map[string]any{"name": "qwen"}, "load qwen"},
		{"unload_model", map[string]any{}, "unload"},
		{"start_server", map[string]any{"profile": "qwen"}, "start --profile qwen"},
		{"stop_server", map[string]any{"target": "llamacpp"}, "stop llamacpp"},
		{"tail_log", map[string]any{}, "logs"},
	}
	for _, c := range cases {
		if got := callText(t, s, c.tool, c.args); got != c.want {
			t.Errorf("%s -> %q, want %q", c.tool, got, c.want)
		}
	}
}

// In --read-only mode the mutating tools must not be registered at all.
func TestEndToEndReadOnlyHidesMutatingTools(t *testing.T) {
	cfg := &config{llamaLauncherBin: echoArgsCLI(t), readOnly: true}
	s := startAdapter(t, cfg, loopbackAllow(t))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	names := map[string]bool{}
	for tool, err := range s.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("list tools: %v", err)
		}
		names[tool.Name] = true
	}
	for _, read := range []string{"list_profiles", "server_status", "tail_log"} {
		if !names[read] {
			t.Errorf("read tool %q should be present in read-only mode", read)
		}
	}
	for _, mut := range []string{"load_profile", "unload_model", "start_server", "stop_server"} {
		if names[mut] {
			t.Errorf("mutating tool %q should be hidden in read-only mode", mut)
		}
	}
}

// A client whose source IP is not allowed cannot establish a session at all.
func TestEndToEndAllowlistBlocksConnect(t *testing.T) {
	cfg := &config{llamaLauncherBin: echoArgsCLI(t)}
	denyAll, err := parseAllow([]string{"203.0.113.7"}) // loopback excluded
	if err != nil {
		t.Fatal(err)
	}
	server := newServer(cfg)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	ts := httptest.NewServer(allowlistMiddleware(denyAll, handler))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	if _, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL, MaxRetries: -1}, nil); err == nil {
		t.Fatal("expected connect to fail for non-allowlisted client")
	}
}
