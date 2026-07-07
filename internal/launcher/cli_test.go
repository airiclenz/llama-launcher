package launcher

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusEntry mirrors the JSON schema cmdStatusJSON emits (TDD §3.2).
type statusEntry struct {
	Backend       string `json:"backend"`
	Running       bool   `json:"running"`
	Address       string `json:"address"`
	ActiveProfile string `json:"active_profile"`
	ActiveModel   string `json:"active_model"`
	PID           int    `json:"pid"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything fn wrote plus its return value. Tests using it must not call
// t.Parallel(), because os.Stdout is process-global.
func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	code := fn()

	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	return string(out), code
}

// newFakeLlamaCppServer starts an httptest server that satisfies the llamacpp
// health, model-list and props probes, reporting modelID as the loaded model.
func newFakeLlamaCppServer(t *testing.T, modelID string) (string, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{"id": modelID}},
			})
		case "/props":
			w.Write([]byte(`{"default_generation_settings":{"n_ctx":4096}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return hostPort(t, srv.URL)
}

func TestCmdStatusJSON_TwoInstancesSameBackend(t *testing.T) {
	// Two fake llamacpp servers on different ports: one at the backend's
	// configured (defaults) address, one reached via a profile port override
	// — the multi-instance layout auto_stop_server: false permits.
	hostA, portA := newFakeLlamaCppServer(t, "/models/a.gguf")
	_, portB := newFakeLlamaCppServer(t, "/models/b.gguf")
	modelsDir := t.TempDir()
	if err := writeEmpty(filepath.Join(modelsDir, "b.gguf")); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Servers:   map[string]ServerConfig{"llamacpp": {Enabled: true}},
		ModelsDir: modelsDir,
		LogDir:    t.TempDir(),
		Profiles: map[string]Profile{
			"second": {Model: "b.gguf", ProfileParams: ProfileParams{Port: &portB}},
		},
	}
	cfg.Defaults = ProfileParams{Server: strPtrLocal("llamacpp"), Host: &hostA, Port: &portA}

	out, code := captureStdout(t, func() int { return cmdStatusJSON(cfg) })

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	var entries []statusEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	for i, e := range entries {
		if e.Backend != "llamacpp" || !e.Running {
			t.Errorf("entries[%d] = %+v, want backend llamacpp and running true", i, e)
		}
	}
	if entries[0].Address == entries[1].Address {
		t.Errorf("both entries report address %q, want distinct addresses", entries[0].Address)
	}
}

// starColumn returns the visible column (escape-aware, rune-counted) at which
// the ★ marker sits in line, or -1 when the line carries no marker.
func starColumn(line string) int {
	if i := strings.IndexRune(line, '★'); i >= 0 {
		return visibleWidth(line[:i])
	}
	return -1
}

func TestCmdList_StarColumnAlignmentMultibyte(t *testing.T) {
	// Two favourite profiles whose descriptions share the same *visible* width
	// but differ in byte length (the em dash is a 3-byte rune). Byte-len
	// measurement drifts the ★ column between the rows; visibleWidth keeps the
	// marker aligned, matching how the menu builders measure the same input.
	host := "127.0.0.1"
	port := 8080
	cfg := &Config{
		Servers: map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:  t.TempDir(),
		Profiles: map[string]Profile{
			"alpha": {Description: "wide — dash", IsFavourite: true},
			"bravo": {Description: "plain-ascii", IsFavourite: true},
		},
	}
	cfg.Defaults = ProfileParams{Server: strPtrLocal("llamacpp"), Host: &host, Port: &port}

	out, code := captureStdout(t, func() int { return cmdList(cfg, nil) })
	if code != 0 {
		t.Fatalf("cmdList exit = %d, want 0", code)
	}

	var cols []int
	for _, line := range strings.Split(out, "\n") {
		if c := starColumn(line); c >= 0 {
			cols = append(cols, c)
		}
	}
	if len(cols) != 2 {
		t.Fatalf("expected 2 ★ rows, got %d in:\n%s", len(cols), out)
	}
	if cols[0] != cols[1] {
		t.Errorf("★ columns misaligned: %d vs %d\n%s", cols[0], cols[1], out)
	}
}

func TestCmdStatusJSON_NothingRunning(t *testing.T) {
	host := "127.0.0.1"
	port := 1 // a port nothing listens on
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true},
			"ollama":   {Enabled: true},
			"lmstudio": {Enabled: false},
		},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	cfg.Defaults = ProfileParams{Server: strPtrLocal("llamacpp"), Host: &host, Port: &port}

	out, code := captureStdout(t, func() int { return cmdStatusJSON(cfg) })

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	var entries []statusEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (one per enabled backend): %+v", len(entries), entries)
	}
	wantBackends := []string{"llamacpp", "ollama"} // stopped entries are sorted by backend name
	for i, e := range entries {
		if e.Backend != wantBackends[i] {
			t.Errorf("entries[%d].Backend = %q, want %q", i, e.Backend, wantBackends[i])
		}
		if e.Running || e.Address != "" {
			t.Errorf("entries[%d] = %+v, want running=false with empty address", i, e)
		}
	}
}

// writeRunConfig writes a minimal two-backend config to a temp file and returns
// its path. Both backends resolve to 127.0.0.1:1 — a port nothing listens on —
// so DiscoverRunningInstances finds nothing, which the adversarial exit-code
// cases below all assume. The single profile names its server explicitly so the
// load is free of the defaults.server deprecation warning.
func writeRunConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "servers:\n" +
		"  llamacpp: true\n" +
		"  ollama: true\n" +
		"log_dir: " + dir + "\n" +
		"defaults:\n" +
		"  server: llamacpp\n" +
		"  host: 127.0.0.1\n" +
		"  port: 1\n" +
		"profiles:\n" +
		"  chat:\n" +
		"    server: ollama\n" +
		"    model: llama3\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

// runCLI drives Run with an explicit --config, capturing stdout for assertions
// and discarding stderr so a command's diagnostics do not clutter the test log.
// Callers must not use t.Parallel(): the standard streams are process-global.
func runCLI(t *testing.T, cfgPath string, args ...string) (string, int) {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stdout): %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe (stderr): %v", err)
	}
	os.Stdout, os.Stderr = outW, errW

	// Drain stderr concurrently so a chatty command cannot fill the pipe
	// buffer and block Run.
	drained := make(chan struct{})
	go func() {
		io.Copy(io.Discard, errR)
		close(drained)
	}()

	code := Run(append([]string{"--config", cfgPath}, args...))

	os.Stdout, os.Stderr = origOut, origErr
	if err := outW.Close(); err != nil {
		t.Fatalf("closing stdout writer: %v", err)
	}
	if err := errW.Close(); err != nil {
		t.Fatalf("closing stderr writer: %v", err)
	}
	<-drained
	out, err := io.ReadAll(outR)
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	return string(out), code
}

// TestRun_StatusJSONNothingRunning pins the exit-code + payload contract the MCP
// adapter's result mapping depends on: with nothing running, `status --json`
// exits 1 while still printing a valid JSON array (one running=false entry per
// enabled backend, per item 9).
func TestRun_StatusJSONNothingRunning(t *testing.T) {
	cfgPath := writeRunConfig(t)

	out, code := runCLI(t, cfgPath, "status", "--json")

	if code != 1 {
		t.Errorf("exit code = %d, want 1 (nothing running)", code)
	}
	var entries []statusEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\noutput: %s", err, out)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (one per enabled backend): %+v", len(entries), entries)
	}
	wantBackends := []string{"llamacpp", "ollama"}
	for i, e := range entries {
		if e.Backend != wantBackends[i] {
			t.Errorf("entries[%d].Backend = %q, want %q", i, e.Backend, wantBackends[i])
		}
		if e.Running {
			t.Errorf("entries[%d] = %+v, want running=false", i, e)
		}
	}
}

// TestRun_ExitCodes exercises the documented 0/1/2/3 exit-code contract (TDD
// §3.3) through the real Run dispatcher: usage errors are 2 and a
// nothing-running stop/unload is 1.
func TestRun_ExitCodes(t *testing.T) {
	cfgPath := writeRunConfig(t)

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"load without a profile is a usage error", []string{"load"}, 2},
		{"an unknown command is a usage error", []string{"frobnicate"}, 2},
		{"an unknown flag on load is a usage error", []string{"load", "--no-such-flag"}, 2},
		{"stop with nothing running exits not-running", []string{"stop"}, 1},
		{"unload with nothing running exits not-running", []string{"unload"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, code := runCLI(t, cfgPath, c.args...)
			if code != c.want {
				t.Errorf("Run(%v) exit = %d, want %d", c.args, code, c.want)
			}
		})
	}
}
