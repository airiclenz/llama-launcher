package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestValidateTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		target string
		ok     bool
	}{
		{"empty auto-selects", "", true},
		{"backend llamacpp", "llamacpp", true},
		{"backend lmstudio", "lmstudio", true},
		{"backend ollama", "ollama", true},
		{"ipv4 host:port", "127.0.0.1:8080", true},
		{"hostname:port", "localhost:1234", true},
		{"dotted hostname:port", "my-host.local:11434", true},
		{"bracketed ipv6:port", "[::1]:8080", true},

		{"logs clean subcommand", "clean", false},
		{"follow short flag", "-f", false},
		{"follow long flag", "--follow", false},
		{"days flag", "--days", false},
		{"shell metacharacters", "; rm", false},
		{"space-separated injection", "clean --all", false},
		{"command substitution", "$(reboot)", false},
		{"unknown backend name", "vllm", false},
		{"flag-shaped host:port", "--config:80", false},
		{"empty host", ":8080", false},
		{"port zero", "localhost:0", false},
		{"port out of range", "localhost:65536", false},
		{"non-numeric port", "localhost:http", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateTarget(tc.target)
			if tc.ok && err != nil {
				t.Errorf("validateTarget(%q) = %v, want nil", tc.target, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("validateTarget(%q) = nil, want error", tc.target)
			}
		})
	}
}

// recordingCLI writes a fake llama-launcher that appends every invocation's
// argv to a record file (and echoes it), so a test can prove a rejected tool
// call never reached the CLI at all.
func recordingCLI(t *testing.T) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "argv.log")
	bin = filepath.Join(dir, "fake-llama-launcher")
	script := "#!/bin/sh\necho \"$@\" >> " + shellQuote(record) + "\necho \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func callTool(t *testing.T, s *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

// tail_log is a read tool and stays exposed under --read-only, so its target
// must never be able to reach a mutating (`logs clean`) or blocking
// (`logs -f`) CLI invocation. Adversarial targets are rejected as tool
// errors before the CLI is invoked; legitimate targets pass through.
func TestEndToEndReadOnlyTailLogRejectsInjection(t *testing.T) {
	bin, record := recordingCLI(t)
	cfg := &config{llamaLauncherBin: bin, readOnly: true}
	s := startAdapter(t, cfg, loopbackAllow(t))

	for _, target := range []string{"clean", "-f", "--follow", "--days", "; rm", "clean --all"} {
		res := callTool(t, s, "tail_log", map[string]any{"target": target})
		if !res.IsError {
			t.Errorf("tail_log target %q: want tool error, got success (%q)", target, resultText(t, res))
		}
	}

	// None of the rejected calls may have shelled out.
	if data, err := os.ReadFile(record); err == nil && len(data) > 0 {
		t.Errorf("rejected targets reached the CLI: %q", data)
	}

	for _, tc := range []struct{ target, want string }{
		{"ollama", "logs ollama"},
		{"127.0.0.1:8080", "logs 127.0.0.1:8080"},
	} {
		res := callTool(t, s, "tail_log", map[string]any{"target": tc.target})
		if res.IsError {
			t.Fatalf("tail_log target %q: unexpected tool error: %q", tc.target, resultText(t, res))
		}
		if got := resultText(t, res); got != tc.want {
			t.Errorf("tail_log target %q forwarded as %q, want %q", tc.target, got, tc.want)
		}
	}
}
