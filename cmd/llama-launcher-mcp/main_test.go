package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// markerCLI writes a fake llama-launcher that records each invocation by
// creating a marker file, so a test can assert the CLI was never run.
func markerCLI(t *testing.T, marker string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI script is POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-llama-launcher")
	script := "#!/bin/sh\ntouch " + shellQuote(marker) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	return path
}

// assertCLINotInvoked fails when the marker file exists, i.e. the fake CLI ran.
func assertCLINotInvoked(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("CLI was invoked (marker %q exists, stat err %v)", marker, err)
	}
}

// Hostile positionals — flags and subcommand keywords — must be rejected as an
// MCP tool error before any CLI invocation: "clean" would turn the read-only
// tail_log into the destructive `logs clean`, and "-f" would make `logs`
// follow forever, blocking the adapter.
func TestRunWithPositionalRejectsUnsafeValues(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value string
	}{
		{"short flag", "target", "-f"},
		{"long flag", "target", "--follow"},
		{"bare dash", "target", "-"},
		{"keyword load", "target", "load"},
		{"keyword unload", "profile", "unload"},
		{"keyword start", "target", "start"},
		{"keyword stop", "target", "stop"},
		{"keyword status", "target", "status"},
		{"keyword list", "target", "list"},
		{"keyword logs", "target", "logs"},
		{"keyword clean", "target", "clean"},
		{"keyword validate", "target", "validate"},
		{"keyword init", "profile", "init"},
		{"keyword reset", "profile", "reset"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "invoked")
			cfg := &config{llamaLauncherBin: markerCLI(t, marker)}
			res := cfg.runWithPositional(context.Background(), "logs", c.field, c.value)
			if !res.IsError {
				t.Errorf("%s %q should be rejected as a tool error", c.field, c.value)
			}
			if got := resultText(t, res); !strings.Contains(got, c.value) || !strings.Contains(got, c.field) {
				t.Errorf("error text %q should name the %s and its value %q", got, c.field, c.value)
			}
			assertCLINotInvoked(t, marker)
		})
	}
}

// Legitimate targets — backend names, host:port, or the empty optional — are
// still forwarded to the CLI as positionals.
func TestRunWithPositionalForwardsSafeValues(t *testing.T) {
	cfg := &config{llamaLauncherBin: echoArgsCLI(t)}
	cases := []struct {
		value string
		want  string
	}{
		{"127.0.0.1:8080", "logs 127.0.0.1:8080"},
		{"llamacpp", "logs llamacpp"},
		{"", "logs"},
	}
	for _, c := range cases {
		res := cfg.runWithPositional(context.Background(), "logs", "target", c.value)
		if res.IsError {
			t.Errorf("target %q should be forwarded, got tool error %q", c.value, resultText(t, res))
			continue
		}
		if got := resultText(t, res); got != c.want {
			t.Errorf("target %q -> %q, want %q", c.value, got, c.want)
		}
	}
}
