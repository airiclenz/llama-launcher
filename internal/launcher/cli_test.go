package launcher

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusJSONEntry mirrors the fields of cmdStatusJSON's output that the
// tests assert on.
type statusJSONEntry struct {
	Backend string `json:"backend"`
	Running bool   `json:"running"`
	Address string `json:"address"`
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything fn wrote. Not safe for parallel tests — os.Stdout is process
// state — so callers must not call t.Parallel().
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = original }()

	fn()

	writer.Close()
	os.Stdout = original
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	return string(data)
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything fn wrote. Not safe for parallel tests — os.Stderr is process
// state — so callers must not call t.Parallel().
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stderr = writer
	defer func() { os.Stderr = original }()

	fn()

	writer.Close()
	os.Stderr = original
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	return string(data)
}

// newFakeLlamaCppServer returns an httptest server that passes the llamacpp
// backend's body-discriminating health check and 404s everything else.
func newFakeLlamaCppServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// decodeStatusJSON unmarshals cmdStatusJSON's stdout into entries.
func decodeStatusJSON(t *testing.T, out string) []statusJSONEntry {
	t.Helper()

	var entries []statusJSONEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("unmarshalling status JSON %q: %v", out, err)
	}
	return entries
}

// TestCmdStart_ManagedDefaultWithoutProfileFailsFast asserts that a bare
// `start` with a managed default backend (llamacpp) fails fast with the
// configuration-error exit code and an actionable message instead of
// forking a llama-server that would exit immediately for lack of a model
// (ADR-0003: the model is baked into the start arguments). PATH is blanked
// so a regression fails at the llama-server binary lookup (exit 3) rather
// than forking a real binary.
func TestCmdStart_ManagedDefaultWithoutProfileFailsFast(t *testing.T) {
	t.Setenv("PATH", "")

	host := "127.0.0.1"
	port := 1 // a port nothing listens on

	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	cfg.Defaults = ProfileParams{
		Server: strPtrLocal("llamacpp"),
		Host:   &host,
		Port:   &port,
	}

	var code int
	errOut := captureStderr(t, func() {
		_ = captureStdout(t, func() { code = cmdStart(cfg, nil) })
	})

	if code != 2 {
		t.Errorf("exit code = %d, want 2 (configuration error)", code)
	}
	if !strings.Contains(errOut, "requires a profile") {
		t.Errorf("stderr = %q, want it to say the backend requires a profile", errOut)
	}
	if !strings.Contains(errOut, "--profile") {
		t.Errorf("stderr = %q, want it to name the --profile flag", errOut)
	}
}

// TestCmdStatusJSON_ListsEveryInstanceOfABackend runs two fake llamacpp
// servers on distinct addresses (legal under auto_stop_server: false,
// ADR-0006) and asserts the JSON output enumerates both instead of
// keeping only the first discovered instance per backend.
func TestCmdStatusJSON_ListsEveryInstanceOfABackend(t *testing.T) {
	srvA := newFakeLlamaCppServer(t)
	srvB := newFakeLlamaCppServer(t)

	hostA, portA := hostPort(t, srvA.URL)
	_, portB := hostPort(t, srvB.URL)

	cfg := &Config{
		Servers: map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:  t.TempDir(),
		Profiles: map[string]Profile{
			// A second llamacpp instance address enters discovery via a
			// profile whose port overrides the default.
			"second": {ProfileParams: ProfileParams{Port: &portB}},
		},
	}
	cfg.Defaults = ProfileParams{
		Server: strPtrLocal("llamacpp"),
		Host:   &hostA,
		Port:   &portA,
	}

	var code int
	out := captureStdout(t, func() { code = cmdStatusJSON(cfg) })

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (instances running)", code)
	}
	entries := decodeStatusJSON(t, out)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Backend != "llamacpp" {
			t.Errorf("entry backend = %q, want llamacpp", e.Backend)
		}
		if !e.Running {
			t.Errorf("entry %q running = false, want true", e.Address)
		}
		seen[e.Address] = true
	}
	addrA := addrFromURL(t, srvA.URL)
	addrB := addrFromURL(t, srvB.URL)
	if !seen[addrA] || !seen[addrB] {
		t.Errorf("addresses %v, want both %q and %q", seen, addrA, addrB)
	}

	// Pin the full documented key set: the MCP server_status tool promises
	// these names to remote clients, so a silent rename must fail a test.
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("re-unmarshalling status JSON: %v", err)
	}
	for _, key := range []string{"backend", "running", "address", "active_profile", "active_model", "pid", "uptime_seconds"} {
		if _, ok := raw[0][key]; !ok {
			t.Errorf("running entry is missing documented key %q: %v", key, raw[0])
		}
	}
}

// TestCmdStatusJSON_AllStoppedEmitsIdleEntryPerBackend asserts that with
// nothing running the output still contains one running=false entry per
// enabled backend and the exit code is 1.
func TestCmdStatusJSON_AllStoppedEmitsIdleEntryPerBackend(t *testing.T) {
	host := "127.0.0.1"
	port := 1 // a port nothing listens on

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true},
			"ollama":   {Enabled: true},
		},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	cfg.Defaults = ProfileParams{
		Server: strPtrLocal("llamacpp"),
		Host:   &host,
		Port:   &port,
	}

	var code int
	out := captureStdout(t, func() { code = cmdStatusJSON(cfg) })

	if code != 1 {
		t.Errorf("exit code = %d, want 1 (all stopped)", code)
	}
	entries := decodeStatusJSON(t, out)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	wantBackends := []string{"llamacpp", "ollama"} // sorted backend order
	for i, e := range entries {
		if e.Backend != wantBackends[i] {
			t.Errorf("entry[%d].Backend = %q, want %q", i, e.Backend, wantBackends[i])
		}
		if e.Running {
			t.Errorf("entry[%d] (%s) running = true, want false", i, e.Backend)
		}
	}
}

// starColumn returns the visible-width column at which the ★ marker starts in
// line, or -1 when the line carries no marker.
func starColumn(line string) int {
	if i := strings.IndexRune(line, '★'); i >= 0 {
		return visibleWidth(line[:i])
	}
	return -1
}

// TestCmdList_StarColumnAlignmentMultibyte pins the display-width measurement
// in cmdList: two favourite profiles whose descriptions share the same
// *visible* width but differ in byte length (the em dash is a 3-byte rune).
// Byte-len measurement drifts the ★ column between the rows; visibleWidth
// keeps the marker aligned, matching how the menu builders measure the same
// input.
func TestCmdList_StarColumnAlignmentMultibyte(t *testing.T) {
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

	var code int
	out := captureStdout(t, func() { code = cmdList(cfg, nil) })
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

// writeRunConfig writes a minimal valid config for driving the real Run
// dispatcher and returns its path.
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

	// Drain both pipes concurrently so a chatty command cannot fill either
	// ~64 KiB pipe buffer and block Run.
	var out bytes.Buffer
	outDrained := make(chan struct{})
	go func() {
		io.Copy(&out, outR)
		close(outDrained)
	}()
	errDrained := make(chan struct{})
	go func() {
		io.Copy(io.Discard, errR)
		close(errDrained)
	}()

	code := Run(append([]string{"--config", cfgPath}, args...))

	os.Stdout, os.Stderr = origOut, origErr
	if err := outW.Close(); err != nil {
		t.Fatalf("closing stdout writer: %v", err)
	}
	if err := errW.Close(); err != nil {
		t.Fatalf("closing stderr writer: %v", err)
	}
	<-outDrained
	<-errDrained
	return out.String(), code
}

// TestRun_StatusJSONNothingRunning pins the exit-code + payload contract the
// MCP adapter's result mapping depends on: with nothing running,
// `status --json` exits 1 while still printing a valid JSON array (one
// running=false entry per enabled backend).
func TestRun_StatusJSONNothingRunning(t *testing.T) {
	cfgPath := writeRunConfig(t)

	out, code := runCLI(t, cfgPath, "status", "--json")

	if code != 1 {
		t.Errorf("exit code = %d, want 1 (nothing running)", code)
	}
	entries := decodeStatusJSON(t, out)
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

// TestRun_ExitCodes exercises the documented exit-code contract (TDD §3.3)
// through the real Run dispatcher: usage errors are 2 and a nothing-running
// stop/unload is 1.
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
