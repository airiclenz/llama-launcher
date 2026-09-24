package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactLogText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		keys []string
		want string
	}{
		{
			name: "key in plain text",
			text: "auth failed for key sk-secret-1\n",
			keys: []string{"sk-secret-1"},
			want: "auth failed for key [redacted]\n",
		},
		{
			name: "key in an --api-key pair",
			text: "argv: llama-server --api-key sk-secret-1 --port 8080",
			keys: []string{"sk-secret-1"},
			want: "argv: llama-server --api-key [redacted] --port 8080",
		},
		{
			name: "multi-line with every occurrence masked",
			text: "line one sk-secret-1\nline two\nsk-secret-1 again\n",
			keys: []string{"sk-secret-1"},
			want: "line one [redacted]\nline two\n[redacted] again\n",
		},
		{
			name: "empty keys leave text without a flag pair unchanged",
			text: "main: server is listening on http://127.0.0.1:8080\n",
			keys: nil,
			want: "main: server is listening on http://127.0.0.1:8080\n",
		},
		{
			name: "blank keys are ignored rather than masking everything",
			text: "plain line\n",
			keys: []string{"", "   "},
			want: "plain line\n",
		},
		{
			name: "empty keys still mask the value after --api-key",
			text: "argv: llama-server --api-key extra-args-key --port 8080",
			keys: []string{},
			want: "argv: llama-server --api-key [redacted] --port 8080",
		},
		{
			name: "equals spelling of the flag",
			text: "argv: --api-key=extra-args-key -c 4096",
			keys: nil,
			want: "argv: --api-key=[redacted] -c 4096",
		},
		{
			name: "quoted flag pair keeps its closing quotes",
			text: `argv: ["--api-key", "extra-args-key"]`,
			keys: nil,
			want: `argv: ["--api-key", "[redacted]"]`,
		},
		{
			name: "api-key-file is not a key flag",
			text: "argv: --api-key-file /etc/keys.txt",
			keys: nil,
			want: "argv: --api-key-file /etc/keys.txt",
		},
		{
			name: "flag at end of line does not swallow the next line",
			text: "argv tail --api-key\nnext line\n",
			keys: nil,
			want: "argv tail --api-key\nnext line\n",
		},
		{
			name: "longer key containing a shorter one is masked whole",
			text: "keys sk-abc and sk-abcdef",
			keys: []string{"sk-abc", "sk-abcdef"},
			want: "keys [redacted] and [redacted]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := RedactLogText(tc.text, tc.keys); got != tc.want {
				t.Errorf("RedactLogText(%q, %q) = %q, want %q", tc.text, tc.keys, got, tc.want)
			}
		})
	}
}

// redactSentinelKey is the configured key the log-surface tests plant in a
// log; none of them may ever print it.
const redactSentinelKey = "sk-sentinel-log-redaction-7f3a"

// writeKeyEchoingLog writes a launcher-managed log for backend under logDir
// whose lines carry the configured key both bare and as an --api-key argv
// pair, plus an extra_args key the configuration does not know.
func writeKeyEchoingLog(t *testing.T, logDir, backend string) string {
	t.Helper()

	path := filepath.Join(logDir, backend+"-20260901-120000.log")
	content := "build: 10851\n" +
		"argv: llama-server --api-key " + redactSentinelKey + " --api-key extra-args-key\n" +
		"env LLAMA_API_KEY=" + redactSentinelKey + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing log: %v", err)
	}
	return path
}

// assertLogRedacted fails when out leaks either planted key or lost the
// log's non-secret content.
func assertLogRedacted(t *testing.T, out string) {
	t.Helper()

	if strings.Contains(out, redactSentinelKey) {
		t.Errorf("output leaks the configured key:\n%s", out)
	}
	if strings.Contains(out, "extra-args-key") {
		t.Errorf("output leaks the --api-key argv value:\n%s", out)
	}
	if !strings.Contains(out, "build: 10851") || !strings.Contains(out, redactionMark) {
		t.Errorf("output lost the log or the redaction marker:\n%s", out)
	}
}

// TestCmdLogs_RedactsConfiguredKey drives `llama-launcher logs` — the path the
// MCP tail_log tool shells to — against a running instance whose log echoes
// its key. Not parallel: captureStdout swaps os.Stdout.
func TestCmdLogs_RedactsConfiguredKey(t *testing.T) {
	srv := newFakeLlamaCppServer(t)
	host, port := hostPort(t, srv.URL)
	logDir := t.TempDir()
	writeKeyEchoingLog(t, logDir, "llamacpp")

	cfg := &Config{
		Servers: map[string]ServerConfig{"llamacpp": {Enabled: true, APIKey: redactSentinelKey}},
		LogDir:  logDir,
	}
	cfg.Defaults = ProfileParams{
		Server: strPtrLocal("llamacpp"),
		Host:   &host,
		Port:   &port,
	}

	var code int
	out := captureStdout(t, func() { code = cmdLogs(cfg, nil) })

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", code, out)
	}
	assertLogRedacted(t, out)
}

// TestShowInstanceLog_RedactsInstanceBackendKey exercises the menus' Show log
// seam for a backend other than llamacpp: the key must come from the
// instance's own backend. Not parallel: captureStdout swaps os.Stdout.
func TestShowInstanceLog_RedactsInstanceBackendKey(t *testing.T) {
	logDir := t.TempDir()
	path := writeKeyEchoingLog(t, logDir, "ollama")
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true, APIKey: "sk-some-other-backend"},
			"ollama":   {Enabled: true, APIKey: redactSentinelKey},
		},
		LogDir: logDir,
	}
	inst := &RunningInstance{Backend: "ollama", LogFile: path}

	var err error
	out := captureStdout(t, func() { err = showInstanceLog(cfg, inst, false) })

	if err != nil {
		t.Fatalf("showInstanceLog: %v", err)
	}
	assertLogRedacted(t, out)
}
