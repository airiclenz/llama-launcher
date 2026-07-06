package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeCLI writes a small shell script that emits the given stdout/stderr and
// exits with the given code, then returns its path for use as llamaLauncherBin.
func fakeCLI(t *testing.T, stdout, stderr string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI script is POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-llama-launcher")
	script := "#!/bin/sh\n"
	if stdout != "" {
		script += "printf '%s' " + shellQuote(stdout) + "\n"
	}
	if stderr != "" {
		script += "printf '%s' " + shellQuote(stderr) + " 1>&2\n"
	}
	script += "exit " + itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake CLI: %v", err)
	}
	return path
}

func shellQuote(s string) string { return "'" + s + "'" }
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *mcp.TextContent", r.Content[0])
	}
	return tc.Text
}

func TestRunSuccessReturnsStdout(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, `[{"name":"qwen"}]`, "", 0)}
	res := cfg.run(context.Background(), "list", "--json")
	if res.IsError {
		t.Fatal("success should not be flagged as error")
	}
	if got := resultText(t, res); got != `[{"name":"qwen"}]` {
		t.Errorf("text = %q", got)
	}
}

// A non-zero exit that still prints JSON (e.g. `status` when nothing is
// running) must be returned as normal content, not an error.
func TestRunNonZeroWithStdoutIsNotError(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, `[]`, "", 1)}
	res := cfg.run(context.Background(), "status", "--json")
	if res.IsError {
		t.Error("non-zero exit with stdout should not be an error")
	}
	if got := resultText(t, res); got != `[]` {
		t.Errorf("text = %q", got)
	}
}

// A non-zero exit with no stdout is a real failure: flag it and surface stderr.
func TestRunFailureSurfacesStderr(t *testing.T) {
	cfg := &config{llamaLauncherBin: fakeCLI(t, "", "Error: no such profile", 2)}
	res := cfg.run(context.Background(), "load", "ghost")
	if !res.IsError {
		t.Fatal("failure with no stdout should be flagged as error")
	}
	if got := resultText(t, res); got != "Error: no such profile" {
		t.Errorf("text = %q", got)
	}
}

func TestRunForwardsConfigPath(t *testing.T) {
	// The fake CLI echoes its own args so we can assert --config is forwarded.
	dir := t.TempDir()
	path := filepath.Join(dir, "echo-args")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config{llamaLauncherBin: path, configPath: "/tmp/cfg.yaml"}
	got := resultText(t, cfg.run(context.Background(), "status", "--json"))
	want := "--config /tmp/cfg.yaml status --json"
	if got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
}

// A fake CLI that floods stdout with more than the cap must come back as the
// capped prefix plus a truncation notice, not the full unbounded output.
func TestRunCapsOversizedOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flood")
	script := "#!/bin/sh\nyes flood | head -c " + itoa(2*maxCapturedOutput) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config{llamaLauncherBin: path}
	res := cfg.run(context.Background(), "logs")
	if res.IsError {
		t.Fatal("truncated output should still be a normal result, not an error")
	}
	got := resultText(t, res)
	if maxLen := maxCapturedOutput + len(truncationNotice) + 1; len(got) > maxLen {
		t.Errorf("output length = %d, want <= %d", len(got), maxLen)
	}
	if !strings.HasSuffix(got, truncationNotice) {
		t.Errorf("output does not end with truncation notice %q; tail = %q", truncationNotice, got[len(got)-80:])
	}
}

func TestLimitedWriter(t *testing.T) {
	tests := []struct {
		name      string
		limit     int
		writes    []string
		want      string
		truncated bool
	}{
		{"under cap", 10, []string{"ab", "cd"}, "abcd", false},
		{"exactly cap is not truncation", 5, []string{"abcde"}, "abcde", false},
		{"over cap in one write", 5, []string{"abcdefgh"}, "abcde", true},
		{"over cap across writes", 6, []string{"abcd", "efgh"}, "abcdef", true},
		{"writes after cap are discarded", 5, []string{"abcde", "fgh"}, "abcde", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &limitedWriter{limit: tt.limit}
			for _, s := range tt.writes {
				n, err := w.Write([]byte(s))
				if err != nil {
					t.Fatalf("Write(%q) error: %v", s, err)
				}
				if n != len(s) {
					t.Errorf("Write(%q) = %d, want %d (must report full consumption)", s, n, len(s))
				}
			}
			if got := w.b.String(); got != tt.want {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
			if w.truncated != tt.truncated {
				t.Errorf("truncated = %v, want %v", w.truncated, tt.truncated)
			}
		})
	}
}
