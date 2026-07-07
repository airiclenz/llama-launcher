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
