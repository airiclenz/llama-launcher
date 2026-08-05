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
	Backend  string `json:"backend"`
	Running  bool   `json:"running"`
	Starting bool   `json:"starting"`
	Address  string `json:"address"`
	// ActiveModel is the machine contract this plan deliberately leaves raw:
	// clients match on the id the server reported, path and all.
	ActiveModel string `json:"active_model"`
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
	for _, key := range []string{"backend", "running", "starting", "address", "active_profile", "active_model", "pid", "uptime_seconds"} {
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

// newFakeStartingLlamaCppServer returns an httptest server that mimics a
// llama-server still loading its model: every path answers 503, which fails
// the health check but satisfies the StartingUp probe (ADR-0010).
func newFakeStartingLlamaCppServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startingCfg returns a config whose single enabled backend is expected at
// addr, so discovery probes exactly that address.
func startingCfg(t *testing.T, backend, addr string) *Config {
	t.Helper()

	host, port, ok := splitHostPort(addr)
	if !ok {
		t.Fatalf("splitting %q", addr)
	}
	cfg := &Config{
		Servers:  map[string]ServerConfig{backend: {Enabled: true}},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	cfg.Defaults = ProfileParams{
		Server: strPtrLocal(backend),
		Host:   &host,
		Port:   &port,
	}
	return cfg
}

// TestCmdStatus_RendersStartingInstance pins the human rendering of a
// Starting instance (ADR-0010): the state column says "starting…" instead
// of "running", and the details block still appears — its PID and log are
// what a user watching a long model load needs.
func TestCmdStatus_RendersStartingInstance(t *testing.T) {
	srv := newFakeStartingLlamaCppServer(t)
	cfg := startingCfg(t, "llamacpp", addrFromURL(t, srv.URL))

	var code int
	out := captureStdout(t, func() { code = cmdStatus(cfg, nil) })

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (a Starting instance counts as present)", code)
	}
	if !strings.Contains(out, "starting…") {
		t.Errorf("output does not show the starting… state:\n%s", out)
	}
	if strings.Contains(out, "running") {
		t.Errorf("output labels a Starting instance as running:\n%s", out)
	}
	if !strings.Contains(out, "Starting: LLaMA.cpp") {
		t.Errorf("output is missing the Starting details line:\n%s", out)
	}
}

// TestCmdStatusJSON_ReportsStartingInstance pins the machine contract for a
// Starting instance (ADR-0010): running keeps meaning healthy — the entry
// reports running=false with starting=true — while the exit code matches
// the human path, which counts the instance as present.
func TestCmdStatusJSON_ReportsStartingInstance(t *testing.T) {
	srv := newFakeStartingLlamaCppServer(t)
	addr := addrFromURL(t, srv.URL)
	cfg := startingCfg(t, "llamacpp", addr)

	var code int
	out := captureStdout(t, func() { code = cmdStatusJSON(cfg) })

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (a Starting instance counts as present)", code)
	}
	entries := decodeStatusJSON(t, out)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Running {
		t.Error("running = true, want false — running means healthy, not Starting")
	}
	if !e.Starting {
		t.Error("starting = false, want true for a 503 /health answer")
	}
	if e.Address != addr {
		t.Errorf("address = %q, want %q", e.Address, addr)
	}
}

// modelPathFixture is the shape llama-server actually reports on /v1/models:
// the absolute --model path it was launched with. Split into its directory
// and base name so the display tests can assert on each half.
const (
	modelFixtureDir  = "/Users/airic/LL-Models/Qwen"
	modelFixtureBase = "qwen3.6-35B-A3B-Q4_K_M.gguf"
	modelFixturePath = modelFixtureDir + "/" + modelFixtureBase
)

// newFakeLlamaCppServerServingModel returns a healthy llamacpp stand-in whose
// /v1/models answer carries modelID, so discovery fills ActiveModel with it.
func newFakeLlamaCppServerServingModel(t *testing.T, modelID string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": modelID}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCmdStatus_ShortensModelPath pins the human rendering of a server whose
// reported model id is an absolute path: both the status row and the
// "Active:" details line (reached because no profile matches) name the file,
// not the directory it lives in.
func TestCmdStatus_ShortensModelPath(t *testing.T) {
	srv := newFakeLlamaCppServerServingModel(t, modelFixturePath)
	cfg := startingCfg(t, "llamacpp", addrFromURL(t, srv.URL))

	var code int
	out := captureStdout(t, func() { code = cmdStatus(cfg, nil) })

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (an instance is running); output:\n%s", code, out)
	}
	if !strings.Contains(out, modelFixtureBase) {
		t.Errorf("output does not name the model file %q:\n%s", modelFixtureBase, out)
	}
	if strings.Contains(out, modelFixtureDir) {
		t.Errorf("output still shows the model's directory %q:\n%s", modelFixtureDir, out)
	}
	if !strings.Contains(out, "Active: "+modelFixtureBase) {
		t.Errorf("the Active: line does not fall back to the shortened model name:\n%s", out)
	}
}

// TestCmdStatusJSON_KeepsRawModelPath is the regression guard on the machine
// contract (TDD §3.2): shortening is a render-time concern, so `status
// --json` keeps emitting the raw server-reported id.
func TestCmdStatusJSON_KeepsRawModelPath(t *testing.T) {
	srv := newFakeLlamaCppServerServingModel(t, modelFixturePath)
	cfg := startingCfg(t, "llamacpp", addrFromURL(t, srv.URL))

	var code int
	out := captureStdout(t, func() { code = cmdStatusJSON(cfg) })

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (an instance is running); output:\n%s", code, out)
	}
	entries := decodeStatusJSON(t, out)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].ActiveModel != modelFixturePath {
		t.Errorf("active_model = %q, want the raw path %q", entries[0].ActiveModel, modelFixturePath)
	}
}

// TestCmdStop_StopsStartingInstance drives the full CLI stop path against a
// Starting instance (ADR-0010): discovery must surface it so a bare `stop`
// resolves it as the single target, and the stop mechanics must run the
// backend's stop hook. The registry stub keeps every real backend away from
// the dead address, so no real process is signalled. Not parallel: it
// mutates the global llmServers registry.
func TestCmdStop_StopsStartingInstance(t *testing.T) {
	addr := deadAddr(t)
	stub := &startingStopServer{name: "startingcli"}
	// Starting until the stop hook has run, so the post-stop verification
	// sees the occupant gone.
	stub.starting = func(string) bool { return len(stub.tryStops) == 0 }
	RegisterLLMServer(stub)
	t.Cleanup(func() { delete(llmServers, stub.name) })
	cfg := startingCfg(t, stub.name, addr)

	var code int
	out := captureStdout(t, func() { code = cmdStop(cfg, nil) })

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", code, out)
	}
	if len(stub.tryStops) != 1 || stub.tryStops[0] != addr {
		t.Errorf("TryStop calls = %v, want exactly one for %s", stub.tryStops, addr)
	}
	if !strings.Contains(out, "Stopped") {
		t.Errorf("output does not report the stop:\n%s", out)
	}
}

// startingManagedServer is startingStopServer with the ManagedLLMServer
// surface, so Unload routes it through the managed rule (unload = stop the
// server, ADR-0003/0004) — the case ADR-0010 extends to Starting instances.
type startingManagedServer struct {
	startingStopServer
}

func (s *startingManagedServer) ServerBinary(*Config) string                        { return s.name }
func (s *startingManagedServer) BuildServerArgs(*Config, *ResolvedProfile) []string { return nil }
func (s *startingManagedServer) BuildServerEnv(*Config, *ResolvedProfile) []string  { return nil }

// TestCmdUnload_StartingManagedInstanceIsStopped: `unload <profile>` on a
// managed instance still loading its model must stop it — unload on a
// managed backend reduces to stop, and ADR-0010 extends that to Starting
// instances — instead of answering "No model loaded", which is what the
// pre-ADR-0010 ActiveModel gate did. Not parallel: it mutates the global
// llmServers registry.
func TestCmdUnload_StartingManagedInstanceIsStopped(t *testing.T) {
	addr := deadAddr(t)
	stub := &startingManagedServer{startingStopServer{name: "startingunload"}}
	stub.starting = func(string) bool { return len(stub.tryStops) == 0 }
	RegisterLLMServer(stub)
	t.Cleanup(func() { delete(llmServers, stub.name) })
	cfg := startingCfg(t, stub.name, addr)
	cfg.Profiles["big"] = Profile{Model: "some-model"}

	var code int
	out := captureStdout(t, func() { code = cmdUnload(cfg, []string{"big"}) })

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output:\n%s", code, out)
	}
	if len(stub.tryStops) != 1 || stub.tryStops[0] != addr {
		t.Errorf("TryStop calls = %v, want exactly one for %s", stub.tryStops, addr)
	}
	if !strings.Contains(out, "server stopped") {
		t.Errorf("output does not report the server stop:\n%s", out)
	}
}

// TestCmdUnload_AmbiguousListingLabelsStarting: with several unload
// candidates a bare `unload` refuses and lists them — a Starting instance
// appears with the "(starting…)" label instead of a model name (ADR-0010).
// Not parallel: it mutates the global llmServers registry.
func TestCmdUnload_AmbiguousListingLabelsStarting(t *testing.T) {
	addrA := deadAddr(t)
	addrB := deadAddr(t)
	stub := &startingStopServer{
		name:     "startingpick",
		starting: func(string) bool { return true },
	}
	RegisterLLMServer(stub)
	t.Cleanup(func() { delete(llmServers, stub.name) })
	cfg := startingCfg(t, stub.name, addrA)
	_, portB, ok := splitHostPort(addrB)
	if !ok {
		t.Fatalf("splitting %q", addrB)
	}
	// A second instance address enters discovery via a profile whose port
	// overrides the default.
	cfg.Profiles["second"] = Profile{ProfileParams: ProfileParams{Port: &portB}}

	var code int
	errOut := captureStderr(t, func() {
		_ = captureStdout(t, func() { code = cmdUnload(cfg, nil) })
	})

	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (ambiguous target); stderr:\n%s", code, errOut)
	}
	if got := strings.Count(errOut, "(starting…)"); got != 2 {
		t.Errorf("stderr lists %d \"(starting…)\" labels, want 2:\n%s", got, errOut)
	}
	if len(stub.tryStops) != 0 {
		t.Errorf("TryStop calls = %v, want none on an ambiguous unload", stub.tryStops)
	}
}

// TestUnloadTargetLabel_ShortensModelPath: the ambiguous-unload listing names
// each candidate by its model file, not by the absolute path llama-server
// reports, while the matched-profile half of the label is untouched.
func TestUnloadTargetLabel_ShortensModelPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		inst *RunningInstance
		want string
	}{
		{
			name: "path-shaped id with a matched profile",
			inst: &RunningInstance{ActiveModel: modelFixturePath, ActiveProfile: "qwen"},
			want: modelFixtureBase + " (qwen)",
		},
		{
			name: "path-shaped id with no matched profile",
			inst: &RunningInstance{ActiveModel: modelFixturePath},
			want: modelFixtureBase + " (no matching profile)",
		},
		{
			name: "name-shaped id is left verbatim",
			inst: &RunningInstance{ActiveModel: "qwen/qwen3-8b", ActiveProfile: "lms"},
			want: "qwen/qwen3-8b (lms)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := unloadTargetLabel(tt.inst); got != tt.want {
				t.Errorf("unloadTargetLabel() = %q, want %q", got, tt.want)
			}
		})
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

// TestCmdList_ContextColumnAlignment pins the context-size column of the text
// `list` output: the cells are right-aligned between the name and the tag, the
// Ollama row stays blank because its LLM Server never receives the parameter,
// and a multibyte profile name keeps every following column in place (the row
// is measured by visible width, not bytes).
func TestCmdList_ContextColumnAlignment(t *testing.T) {
	host := "127.0.0.1"
	port := 8080
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true},
			"ollama":   {Enabled: true},
		},
		LogDir: t.TempDir(),
		Profiles: map[string]Profile{
			"bravo": {ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(65536)}},
			"café":  {ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(131072)}},
			"olla":  {ProfileParams: ProfileParams{Server: strPtrLocal("ollama"), ContextSize: ptrInt(32768)}},
		},
	}
	cfg.Defaults = ProfileParams{Host: &host, Port: &port}

	var code int
	out := captureStdout(t, func() { code = cmdList(cfg, nil) })

	if code != 0 {
		t.Fatalf("cmdList exit = %d, want 0", code)
	}
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "[") {
			rows = append(rows, line)
		}
	}
	want := []string{
		"  bravo   65K  [LLaMA.cpp] -",
		"  café   131K  [LLaMA.cpp] -",
		"  olla         [Ollama   ] -",
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d profile rows, want %d in:\n%s", len(rows), len(want), out)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, rows[i], want[i])
		}
	}

	// The tag column keeps one visible position across every row, including the
	// one whose name carries a multibyte rune.
	for _, row := range rows[1:] {
		if got, want := visibleColumnOf(t, row, "["), visibleColumnOf(t, rows[0], "["); got != want {
			t.Errorf("\"[\" column of %q = %d, want %d", row, got, want)
		}
	}

	// Nothing but spaces stands between the Ollama name and its tag: the cell is
	// blank because the server's ParamSpecs omit the context size.
	if got := strings.TrimPrefix(rows[2], "  olla"); strings.TrimLeft(got, " ") != "[Ollama   ] -" {
		t.Errorf("Ollama row %q does not carry a blank context cell", rows[2])
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
