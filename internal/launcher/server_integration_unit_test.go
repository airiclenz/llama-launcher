package launcher

// Untagged helpers behind the real-llama-server API-key probe in
// server_integration_test.go (build tag integration). The skip and
// build-detection decisions live here, outside the tag, so the plain
// `go test ./...` unit pass covers them without a llama-server on PATH; the
// tagged test calls these same helpers (untagged package symbols are visible
// to a //go:build integration file).

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	// llamaServerBinary is the executable the API-key probe needs on PATH.
	llamaServerBinary = "llama-server"
	// llamaServerModelEnv names the variable holding the absolute .gguf path
	// the probe loads.
	llamaServerModelEnv = "INTEGRATION_MODEL_LLAMACPP"
)

// llamaServerBuildPattern matches the build line llama-server prints for
// --version, e.g. "version: 0.4.0-dev (build 10851, commit 67672dc5b)".
var llamaServerBuildPattern = regexp.MustCompile(`build (\d+), commit ([0-9a-f]+)`)

// llamaServerSkipReason returns why the real-llama-server probe cannot run
// on this host, or "" when it can. lookPath and getenv are injected so the
// decision is testable without touching PATH or the environment.
func llamaServerSkipReason(lookPath func(string) (string, error), getenv func(string) string) string {
	if _, err := lookPath(llamaServerBinary); err != nil {
		return "binary " + llamaServerBinary + " not found in PATH: " + err.Error()
	}
	if getenv(llamaServerModelEnv) == "" {
		return llamaServerModelEnv + " not set (absolute path to a .gguf model)"
	}
	return ""
}

// requireLlamaServer skips t unless llama-server is on PATH and a model is
// configured, returning the model path otherwise.
func requireLlamaServer(t *testing.T) string {
	t.Helper()
	if reason := llamaServerSkipReason(exec.LookPath, os.Getenv); reason != "" {
		t.Skip(reason)
	}
	return os.Getenv(llamaServerModelEnv)
}

// parseLlamaServerBuild extracts the build identifier from llama-server
// --version output as "b<number> (commit <hash>)", or "" when the output
// carries no recognisable build line.
func parseLlamaServerBuild(versionOutput string) string {
	m := llamaServerBuildPattern.FindStringSubmatch(versionOutput)
	if m == nil {
		return ""
	}
	return "b" + m[1] + " (commit " + m[2] + ")"
}

// detectLlamaServerBuild runs `llama-server --version` and returns the build
// it reports, falling back to the trimmed raw output when no build line is
// recognised so the tested build is always recorded.
func detectLlamaServerBuild(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(llamaServerBinary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v\n%s", llamaServerBinary, err, out)
	}
	if build := parseLlamaServerBuild(string(out)); build != "" {
		return build
	}
	return strings.TrimSpace(string(out))
}

func TestLlamaServerSkipReason(t *testing.T) {
	t.Parallel()

	found := func(string) (string, error) { return "/usr/local/bin/llama-server", nil }
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	withModel := func(name string) string {
		if name == llamaServerModelEnv {
			return "/models/tiny.gguf"
		}
		return ""
	}
	noModel := func(string) string { return "" }

	tests := []struct {
		name     string
		lookPath func(string) (string, error)
		getenv   func(string) string
		wantSkip string // substring of the reason; "" means the probe runs
	}{
		{"binary and model present", found, withModel, ""},
		{"binary missing", missing, withModel, "not found in PATH"},
		{"model unset", found, noModel, llamaServerModelEnv + " not set"},
		{"both missing reports the binary first", missing, noModel, "not found in PATH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := llamaServerSkipReason(tt.lookPath, tt.getenv)
			if tt.wantSkip == "" {
				if got != "" {
					t.Errorf("skip reason = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, tt.wantSkip) {
				t.Errorf("skip reason = %q, want it to contain %q", got, tt.wantSkip)
			}
		})
	}
}

func TestParseLlamaServerBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "homebrew version output",
			output: "version: 0.4.0-dev (build 10851, commit 67672dc5b)\nbuilt with AppleClang 21.0.0.21000333 for Darwin arm64\n",
			want:   "b10851 (commit 67672dc5b)",
		},
		{
			name:   "build line after load noise",
			output: "ggml_metal_init: found device\nversion: 10068 (build 10068, commit abc1234)\n",
			want:   "b10068 (commit abc1234)",
		},
		{name: "no build line", output: "llama-server: unknown option\n", want: ""},
		{name: "empty output", output: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseLlamaServerBuild(tt.output); got != tt.want {
				t.Errorf("parseLlamaServerBuild() = %q, want %q", got, tt.want)
			}
		})
	}
}
