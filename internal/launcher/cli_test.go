package launcher

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
