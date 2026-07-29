// The facade tests live in an external test package on purpose: they may
// only reach the launcher through its exported surface, exactly as a client
// module does, so they prove the ADR-0011 contract compiles and behaves from
// the outside. Nothing here starts a real server — every test is httptest
// and temp-directory only.
package launcher_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/airiclenz/llama-launcher/launcher"
)

// modelFileName is the model every temp config refers to; writeConfig
// materialises an empty file of that name so llama.cpp profiles resolve.
const modelFileName = "test-7b.gguf"

// liveModelName is the model id the stand-in server reports. It shares its
// basename with modelFileName, which is what makes a profile "already
// active" on the ADR-0007 idempotent path.
const liveModelName = "/models/" + modelFileName

// liveOllamaModel is the model the Ollama stand-in reports as running, and
// therefore the one an unload against it must name.
const liveOllamaModel = "llama3.1:8b"

// deprecationConfig enables two servers and lets the "chat" profile omit
// server:, so it relies on the soft-deprecated defaults.server fallback and
// produces exactly one config warning (ADR-0005). Its context_size overrides
// the defaults — the merged value a client reads off the resolved profile.
const deprecationConfig = `
servers:
  llamacpp: true
  ollama: true
models_dir: MODELS_DIR
log_dir: LOG_DIR
defaults:
  server: llamacpp
  context_size: 4096
profiles:
  chat:
    model: test-7b.gguf
    context_size: 8192
  coder:
    server: ollama
    model: llama3.1:8b
`

// standInConfig points a single-server config at the stand-in's address, so
// discovery and activation target that one address and nothing else.
const standInConfig = `
servers:
  llamacpp: true
models_dir: MODELS_DIR
log_dir: LOG_DIR
defaults:
  host: HOST
  port: PORT
profiles:
  chat:
    model: test-7b.gguf
    context_size: 8192
    parallel: 1
`

func TestLoadConfig_DeliversWarningsToNotice(t *testing.T) {
	t.Parallel()
	cfgPath := writeConfig(t, deprecationConfig, "127.0.0.1", 8080)

	var notices []string
	_, err := launcher.LoadConfig(cfgPath, func(n string) { notices = append(notices, n) })

	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly one defaults.server deprecation warning", notices)
	}
	if !strings.Contains(notices[0], `"chat"`) || !strings.Contains(notices[0], "defaults.server") {
		t.Errorf("notice = %q, want it to name profile chat and defaults.server", notices[0])
	}
	if strings.HasPrefix(notices[0], "warning: ") {
		t.Errorf("notice = %q, want the raw text — the %q prefix belongs to the CLI printer", notices[0], "warning: ")
	}
}

func TestLoadConfig_NilNoticeIsSafe(t *testing.T) {
	t.Parallel()
	cfgPath := writeConfig(t, deprecationConfig, "127.0.0.1", 8080)

	cfg, err := launcher.LoadConfig(cfgPath, nil)

	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Warnings) != 1 {
		t.Errorf("Warnings = %v, want exactly one (a nil sink discards notices, it must not suppress them)", cfg.Warnings)
	}
}

// TestLoadConfig_ResolveProfileMergesParams covers the path the first client
// reads its model context info on: load the config, resolve a profile, read
// the merged parameters off it.
func TestLoadConfig_ResolveProfileMergesParams(t *testing.T) {
	t.Parallel()
	cfgPath := writeConfig(t, deprecationConfig, "127.0.0.1", 8080)
	cfg, err := launcher.LoadConfig(cfgPath, nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	profile, err := cfg.ResolveProfile("chat")

	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if profile.ContextSize == nil || *profile.ContextSize != 8192 {
		t.Errorf("ContextSize = %v, want 8192 (the profile overriding the defaults' 4096)", profile.ContextSize)
	}
	if profile.Backend != "llamacpp" {
		t.Errorf("Backend = %q, want llamacpp (inherited from defaults.server)", profile.Backend)
	}
	if filepath.Base(profile.ModelPath) != modelFileName {
		t.Errorf("ModelPath = %q, want it to resolve to %s under models_dir", profile.ModelPath, modelFileName)
	}
}

func TestLoadConfig_MissingFileIsErrConfigNotFound(t *testing.T) {
	t.Parallel()

	_, err := launcher.LoadConfig(filepath.Join(t.TempDir(), "absent.yaml"), nil)

	if !errors.Is(err, launcher.ErrConfigNotFound) {
		t.Errorf("err = %v, want it to wrap launcher.ErrConfigNotFound", err)
	}
}

func TestStop_UnreachableAddressIsErrNotRunning(t *testing.T) {
	t.Parallel()

	result, err := launcher.Stop(deadAddr(t))

	if !errors.Is(err, launcher.ErrNotRunning) {
		t.Errorf("err = %v, want it to wrap launcher.ErrNotRunning", err)
	}
	if result == nil {
		t.Error("result = nil, want a non-nil StopResult carrying the steps taken before the failure")
	}
}

func TestUnload_UnreachableAddressIsErrNotRunning(t *testing.T) {
	t.Parallel()

	result, err := launcher.Unload("llamacpp", deadAddr(t))

	if !errors.Is(err, launcher.ErrNotRunning) {
		t.Errorf("err = %v, want it to wrap launcher.ErrNotRunning", err)
	}
	if result == nil {
		t.Error("result = nil, want a non-nil StopResult carrying the steps taken before the failure")
	}
}

// TestUnload_ExternalBackendUnloadsModelAndLeavesServerRunning covers the
// external half of the ADR-0003/0004 unload rule through the facade: the
// model goes away over the backend's own API and the server is left running,
// which the result reports as ServerStopped false. The stand-in records the
// unload it receives, so the proof is what actually reached the server, not
// the returned struct alone.
func TestUnload_ExternalBackendUnloadsModelAndLeavesServerRunning(t *testing.T) {
	t.Parallel()
	standIn := newOllamaStandIn(t, liveOllamaModel)

	result, err := launcher.Unload("ollama", standIn.addr)

	if err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if result.ServerStopped {
		t.Error("ServerStopped = true, want false — an external backend keeps running once its model is unloaded")
	}
	if want := []string{"Unloading model"}; !slices.Equal(result.Steps, want) {
		t.Errorf("Steps = %v, want %v — the signal-and-disconnect steps belong to the managed path", result.Steps, want)
	}
	if result.Instance == nil || result.Instance.Backend != "ollama" || result.Instance.Addr() != standIn.addr {
		t.Errorf("Instance = %+v, want the ollama instance at %s", result.Instance, standIn.addr)
	}
	requests := standIn.unloadRequests()
	if len(requests) != 1 {
		t.Fatalf("unload requests = %q, want exactly one API unload", requests)
	}
	for _, want := range []string{`"model":"` + liveOllamaModel + `"`, `"keep_alive":0`} {
		if !strings.Contains(requests[0], want) {
			t.Errorf("unload request = %q, want it to contain %q", requests[0], want)
		}
	}
}

// TestUnload_ServerStoppedFollowsBackendKind pins the branch the external
// success path cannot show on its own: Unload picks its mechanism from the
// backend alone — a managed backend's unload stops the server process
// (ADR-0003, ADR-0004), an external backend's never does — and
// StopResult.ServerStopped reports which one it picked. Both cases run
// against an address nothing listens on, so neither mechanism reaches a
// server and the field is the only thing separating them.
//
// The managed *success* path stays out of the facade tests deliberately:
// stopping a managed server signals whatever PID is listening at the
// address, and an in-process httptest stand-in would hand it this test
// binary's own PID. Reaching it needs a real llama-server, so the managed
// stop mechanics are covered inside internal/launcher instead.
func TestUnload_ServerStoppedFollowsBackendKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		backend           string
		wantServerStopped bool
	}{
		{"managed backend unloads by stopping the server", "llamacpp", true},
		{"external backend unloads without stopping the server", "ollama", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := launcher.Unload(tc.backend, deadAddr(t))

			if !errors.Is(err, launcher.ErrNotRunning) {
				t.Fatalf("err = %v, want it to wrap launcher.ErrNotRunning — neither mechanism may find a server here", err)
			}
			if result.ServerStopped != tc.wantServerStopped {
				t.Errorf("ServerStopped = %v, want %v for the %s backend", result.ServerStopped, tc.wantServerStopped, tc.backend)
			}
		})
	}
}

func TestDefaultConfigPath_IsConfigYAMLInDefaultConfigDir(t *testing.T) {
	t.Parallel()

	dir, path := launcher.DefaultConfigDir(), launcher.DefaultConfigPath()

	if want := filepath.Join(dir, "config.yaml"); path != want {
		t.Errorf("DefaultConfigPath = %q, want %q", path, want)
	}
	if !strings.HasSuffix(dir, filepath.Join(".config", "llama-launcher")) {
		t.Errorf("DefaultConfigDir = %q, want it to end in .config/llama-launcher", dir)
	}
}

func TestDiscoverRunningInstances_ReportsStandIn(t *testing.T) {
	t.Parallel()
	host, port := standInServer(t, liveModelName, 4096, 1)
	cfg, err := launcher.LoadConfig(writeConfig(t, standInConfig, host, port), nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	instances := launcher.DiscoverRunningInstances(cfg)

	if len(instances) != 1 {
		t.Fatalf("instances = %+v, want exactly one", instances)
	}
	if got, want := instances[0].Addr(), fmt.Sprintf("%s:%d", host, port); got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
	if instances[0].ActiveModel != liveModelName {
		t.Errorf("ActiveModel = %q, want %q", instances[0].ActiveModel, liveModelName)
	}
}

// TestLoadProfile_DeliversDriftNoticeToSinkNotStderr is the end-to-end proof
// of ADR-0011's first contract decision: the library never writes to its
// host's stderr. The stand-in is healthy and already serving the profile's
// model, so the call takes the ADR-0007 idempotent path — it forks nothing —
// and the context size reported by /props (4096) drifts from the profile's
// 8192, which must arrive as one call on the notice sink. Not parallel:
// captureStderr redirects the process-global os.Stderr.
func TestLoadProfile_DeliversDriftNoticeToSinkNotStderr(t *testing.T) {
	host, port := standInServer(t, liveModelName, 4096, 1)
	cfg, err := launcher.LoadConfig(writeConfig(t, standInConfig, host, port), nil)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	profile, err := cfg.ResolveProfile("chat")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}

	var notices []string
	var instance *launcher.RunningInstance
	var started bool
	errOut := captureStderr(t, func() {
		instance, started, err = launcher.LoadProfile(cfg, profile, false, nil, func(n string) {
			notices = append(notices, n)
		})
	})

	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing — the library delivers notices through the sink", errOut)
	}
	if len(notices) != 1 {
		t.Fatalf("notices = %q, want exactly one drift notice", notices)
	}
	for _, want := range []string{`profile "chat"`, "context_size: 4096 → 8192", "load chat --restart"} {
		if !strings.Contains(notices[0], want) {
			t.Errorf("drift notice = %q, want it to contain %q", notices[0], want)
		}
	}
	if started {
		t.Error("started = true, want false — an already-serving profile is not re-activated")
	}
	if instance == nil || instance.ActiveModel != liveModelName {
		t.Errorf("instance = %+v, want one reporting the live model %q", instance, liveModelName)
	}
}

// writeConfig materialises a config file in a fresh temp directory: the YAML
// body with its MODELS_DIR, LOG_DIR, HOST and PORT placeholders substituted,
// plus an empty model file so llama.cpp profiles referring to modelFileName
// resolve. Nothing outside the temp directory is touched.
func writeConfig(t *testing.T, body, host string, port int) string {
	t.Helper()

	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatalf("creating models dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelsDir, modelFileName), nil, 0o644); err != nil {
		t.Fatalf("creating model file: %v", err)
	}

	yaml := strings.NewReplacer(
		"MODELS_DIR", modelsDir,
		"LOG_DIR", filepath.Join(dir, "logs"),
		"HOST", host,
		"PORT", strconv.Itoa(port),
	).Replace(body)

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

// standInServer starts a llama-server stand-in on loopback: healthy, serving
// model, and answering /props with slots slots of ctxPerSlot tokens each —
// the shape the live-parameter probe reads (total context = n_ctx × slots).
func standInServer(t *testing.T, model string, ctxPerSlot, slots int) (string, int) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/v1/models":
			fmt.Fprintf(w, `{"data":[{"id":%q}]}`, model)
		case "/props":
			fmt.Fprintf(w, `{"total_slots":%d,"default_generation_settings":{"n_ctx":%d}}`, slots, ctxPerSlot)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing stand-in URL %q: %v", srv.URL, err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("splitting stand-in address %q: %v", u.Host, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing stand-in port %q: %v", portStr, err)
	}
	return host, port
}

// ollamaStandIn is a stand-in Ollama server on loopback. It answers exactly
// the endpoints an external unload touches — the root probe that identifies
// the backend, /api/ps for the live model, /api/generate for the unload
// itself — and records every unload it receives. Everything else 404s, which
// is what keeps the llama.cpp and LM Studio health checks from claiming the
// address during backend identification.
type ollamaStandIn struct {
	addr string

	mu      sync.Mutex
	unloads []string
}

// newOllamaStandIn starts a stand-in reporting model as its one running
// model. The server shuts down with the test.
func newOllamaStandIn(t *testing.T, model string) *ollamaStandIn {
	t.Helper()

	standIn := &ollamaStandIn{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, "Ollama is running")
		case "/api/ps":
			fmt.Fprintf(w, `{"models":[{"name":%q,"size":4700000000}]}`, model)
		case "/api/generate":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			standIn.record(string(body))
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing stand-in URL %q: %v", srv.URL, err)
	}
	standIn.addr = u.Host
	return standIn
}

// record stores one /api/generate body. Called on the server's goroutine,
// hence the mutex.
func (s *ollamaStandIn) record(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unloads = append(s.unloads, body)
}

// unloadRequests returns a copy of the /api/generate bodies received so far,
// safe to read from the test goroutine.
func (s *ollamaStandIn) unloadRequests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.unloads)
}

// deadAddr returns a loopback host:port nothing listens on: a listener claims
// a free port and gives it up again immediately.
func deadAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing the reserved port: %v", err)
	}
	return addr
}

// captureStderr redirects the process-global os.Stderr for the duration of
// fn and returns everything written to it. Tests using it must not call
// t.Parallel, and fn must not call t.Fatal — the redirect is undone on the
// normal return path only.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	original := os.Stderr
	os.Stderr = w

	fn()

	os.Stderr = original
	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("closing pipe reader: %v", err)
	}
	return string(out)
}
