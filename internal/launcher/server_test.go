package launcher

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunningInstance_Addr(t *testing.T) {
	t.Parallel()

	inst := &RunningInstance{Host: "127.0.0.1", Port: 8080}
	if got := inst.Addr(); got != "127.0.0.1:8080" {
		t.Errorf("Addr() = %q, want %q", got, "127.0.0.1:8080")
	}
}

func TestRunningInstance_Uptime(t *testing.T) {
	t.Parallel()

	inst := &RunningInstance{StartedAt: time.Now().Add(-5 * time.Second)}
	uptime := inst.Uptime()
	if uptime < 4*time.Second || uptime > 6*time.Second {
		t.Errorf("Uptime() = %v, want ~5s", uptime)
	}
}

func TestRunningInstance_Uptime_ZeroStart(t *testing.T) {
	t.Parallel()

	inst := &RunningInstance{}
	if uptime := inst.Uptime(); uptime != 0 {
		t.Errorf("Uptime() = %v, want 0 when StartedAt is zero", uptime)
	}
}

func TestIsProcessAlive_CurrentPID(t *testing.T) {
	t.Parallel()

	if !IsProcessAlive(os.Getpid()) {
		t.Error("IsProcessAlive(os.Getpid()) = false, want true")
	}
}

func TestIsProcessAlive_ZeroPID(t *testing.T) {
	t.Parallel()

	if IsProcessAlive(0) {
		t.Error("IsProcessAlive(0) = true, want false")
	}
}

func TestIsProcessAlive_NegativePID(t *testing.T) {
	t.Parallel()

	if IsProcessAlive(-1) {
		t.Error("IsProcessAlive(-1) = true, want false")
	}
}

func TestIsProcessAlive_InvalidPID(t *testing.T) {
	t.Parallel()

	if IsProcessAlive(99999999) {
		t.Error("IsProcessAlive(99999999) = true, want false")
	}
}

func TestReadLastLines(t *testing.T) {
	t.Parallel()

	t.Run("more lines than requested", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		content := "line1\nline2\nline3\nline4\nline5\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got := readLastLines(path, 3)
		want := "line3\nline4\nline5"
		if got != want {
			t.Errorf("readLastLines = %q, want %q", got, want)
		}
	})

	t.Run("fewer lines than requested", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		content := "line1\nline2\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got := readLastLines(path, 10)
		want := "line1\nline2"
		if got != want {
			t.Errorf("readLastLines = %q, want %q", got, want)
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		t.Parallel()
		got := readLastLines("/nonexistent/path", 5)
		if got != "(could not read log)" {
			t.Errorf("readLastLines = %q, want fallback message", got)
		}
	})
}

func TestShouldCrossServerUnload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		inst          *RunningInstance
		targetBackend string
		want          bool
	}{
		{
			name:          "nil instance",
			inst:          nil,
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "same backend as target",
			inst:          &RunningInstance{Backend: "ollama", ActiveModel: "llama3.1:8b"},
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "no model loaded",
			inst:          &RunningInstance{Backend: "ollama", ActiveModel: ""},
			targetBackend: "lmstudio",
			want:          false,
		},
		{
			name:          "managed backend is skipped",
			inst:          &RunningInstance{Backend: "llamacpp", ActiveModel: "/models/foo.gguf"},
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "external backend with model loaded",
			inst:          &RunningInstance{Backend: "ollama", ActiveModel: "llama3.1:8b"},
			targetBackend: "lmstudio",
			want:          true,
		},
		{
			name:          "unknown backend",
			inst:          &RunningInstance{Backend: "doesnotexist", ActiveModel: "x"},
			targetBackend: "ollama",
			want:          false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldCrossServerUnload(tc.inst, tc.targetBackend); got != tc.want {
				t.Errorf("shouldCrossServerUnload = %v, want %v", got, tc.want)
			}
		})
	}
}

// stopRecordingOps runs the real activation operations (discovery, health
// probes, managed starts) but records stop targets instead of signalling
// real processes. Used by the tests that drive loadProfile against live
// httptest fakes: the real StopInstance would lsof the listening PID — the
// test process itself — and SIGTERM it.
type stopRecordingOps struct {
	realOps
	stopped *[]string
}

func (s stopRecordingOps) stop(addr string, progress ProgressFunc) (*RunningInstance, error) {
	*s.stopped = append(*s.stopped, addr)
	return &RunningInstance{}, nil
}

// TestLoadProfile_StopsForeignBackendAtSharedAddr covers the shared-port
// design: when a *different* backend already occupies the target address,
// LoadProfile's auto-stop loop must stop it — it is the one instance that
// blocks the new server from binding (ADR-0004, ADR-0006).
func TestLoadProfile_StopsForeignBackendAtSharedAddr(t *testing.T) {
	// Not parallel: rewrites PATH.

	// A fake Ollama serving at the shared address.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte("Ollama is running"))
		case "/api/ps":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{{"name": "llama3.1:8b", "size": 1}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true},
			"ollama":   {Enabled: true},
		},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	backend := "llamacpp"
	cfg.Defaults = ProfileParams{Server: &backend, Host: &host, Port: &port}

	profile := &ResolvedProfile{
		Name:          "test",
		ModelPath:     "/models/test.gguf",
		Backend:       "llamacpp",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	var stopped []string
	// Empty PATH so the managed start fails at the binary lookup instead of
	// forking a real llama-server.
	t.Setenv("PATH", t.TempDir())

	_, _, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, false, nil)

	// The auto-stop loop must have cleared the foreign occupant before the
	// managed start was attempted: the recorded stop targets the shared
	// address, and the returned error comes from the start step, which runs
	// after the loop.
	targetAddr := addrFromURL(t, srv.URL)
	if len(stopped) != 1 || stopped[0] != targetAddr {
		t.Errorf("stopped addresses = %v, want exactly [%s]", stopped, targetAddr)
	}
	if err == nil || !strings.Contains(err.Error(), "server binary not found") {
		t.Errorf("err = %v, want the managed-start binary lookup failure", err)
	}
}

// TestLoadProfile_SameBackendSameModelIsNoOp guards ADR-0007: reloading the
// profile a server at the target address is already serving must not stop or
// restart anything.
func TestLoadProfile_SameBackendSameModelIsNoOp(t *testing.T) {
	// Not parallel: rewrites PATH.

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{"id": "/models/test-7b.gguf"}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	backend := "llamacpp"
	cfg.Defaults = ProfileParams{Server: &backend, Host: &host, Port: &port}

	profile := &ResolvedProfile{
		Name:          "test",
		ModelPath:     "/models/test-7b.gguf",
		Backend:       "llamacpp",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	var stopped []string
	// Safety net: if the no-op check regresses, fail at the binary lookup
	// instead of forking a real llama-server.
	t.Setenv("PATH", t.TempDir())

	inst, started, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, false, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if started {
		t.Error("started = true, want false for an idempotent reload")
	}
	if len(stopped) != 0 {
		t.Errorf("stopped addresses = %v, want none for an idempotent reload", stopped)
	}
	if inst == nil || inst.ActiveModel != "/models/test-7b.gguf" {
		t.Errorf("instance = %+v, want ActiveModel /models/test-7b.gguf", inst)
	}
}

// TestWaitForHealth covers the poll loop against llama-server's startup
// behaviour: /health answers 503 while the model loads, so a wait that
// never sees a healthy response times out with the address and window in
// the error, while one that flips healthy mid-wait returns promptly.
func TestWaitForHealth(t *testing.T) {
	t.Parallel()

	t.Run("still-loading 503 until the deadline times out", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`))
		}))
		defer srv.Close()

		err := WaitForHealth(&LlamaCpp{}, addrFromURL(t, srv.URL), 1200*time.Millisecond)
		if err == nil || !strings.Contains(err.Error(), "did not become healthy") {
			t.Errorf("err = %v, want health timeout", err)
		}
	})

	t.Run("flips healthy mid-wait and returns promptly", func(t *testing.T) {
		t.Parallel()
		var requests atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requests.Add(1) < 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		}))
		defer srv.Close()

		start := time.Now()
		err := WaitForHealth(&LlamaCpp{}, addrFromURL(t, srv.URL), 10*time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("WaitForHealth took %v, want a prompt return well under the 10s window", elapsed)
		}
	})
}

// TestWaitForHealth_TimeoutErrNamesPIDAndLog pins the contract for a
// managed start whose health wait expires: the spawned server is left
// running (killing a legitimately slow model load would be worse) and
// the error must name its PID and log path so the user can watch or
// stop it.
func TestWaitForHealth_TimeoutErrNamesPIDAndLog(t *testing.T) {
	t.Parallel()

	base := errors.New("server at 127.0.0.1:8080 did not become healthy within 30s")
	inst := &RunningInstance{
		PID:     4242,
		Backend: "llamacpp",
		Host:    "127.0.0.1",
		Port:    8080,
		LogFile: "/logs/llamacpp-20260719-120000.log",
	}

	err := startupTimeoutErr(base, inst)
	if !errors.Is(err, base) {
		t.Error("startupTimeoutErr must wrap the original timeout error")
	}
	for _, want := range []string{
		"PID 4242",
		"/logs/llamacpp-20260719-120000.log",
		"left running",
		"llama-launcher logs llamacpp",
		"kill 4242",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

// TestLoadProfile_RefusesDoubleSpawnWhileStartingUp covers the retry
// after a health-wait timeout: while the earlier-spawned llama-server is
// still loading its model (health answers 503), a second load — with or
// without --restart — must neither kill it nor fork a duplicate onto the
// occupied port; it refuses with guidance instead.
func TestLoadProfile_RefusesDoubleSpawnWhileStartingUp(t *testing.T) {
	// Not parallel: rewrites PATH.

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// llama-server answers every request with 503 while loading.
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`))
	}))
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	backend := "llamacpp"
	cfg.Defaults = ProfileParams{Server: &backend, Host: &host, Port: &port}

	profile := &ResolvedProfile{
		Name:          "test",
		ModelPath:     "/models/test.gguf",
		Backend:       "llamacpp",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	var stopped []string
	// Empty PATH: if the refusal regresses, the managed start fails at the
	// binary lookup with a distinguishable error instead of forking.
	t.Setenv("PATH", t.TempDir())

	for _, restart := range []bool{false, true} {
		_, _, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, restart, nil)
		if err == nil || !strings.Contains(err.Error(), "still starting up") {
			t.Errorf("restart=%v: err = %v, want still-starting-up refusal", restart, err)
			continue
		}
		if strings.Contains(err.Error(), "server binary not found") {
			t.Errorf("restart=%v: refusal must fire before the spawn attempt, got %v", restart, err)
		}
	}
	if len(stopped) != 0 {
		t.Errorf("stopped addresses = %v, want none — a still-loading server is never killed", stopped)
	}
}

// fakeOps is a pure in-memory activationOps: every operation records its
// invocation and answers from the configured fields, so the orchestration
// tests exercise loadProfile's decision logic without forking a process,
// sending a signal, or opening a socket.
type fakeOps struct {
	healthyAddrs map[string]bool   // addr → backend's own server answers there
	models       map[string]string // addr → currently loaded model
	drift        []string          // liveDrift result for any addr
	instances    []*RunningInstance
	startErr     error
	waitErr      error
	stopErr      error
	loadErr      error

	stopped           []string // stop targets, in call order
	unloadedInstances []string // unloadInstance targets
	started           []string // profile names passed to start
	waited            []string // addresses waited on
	loadedModels      []string // "addr model" per loadModel call
	unloadedModels    []string // "addr model" per unloadModel call
}

func (f *fakeOps) healthy(b LLMServer, addr string) bool { return f.healthyAddrs[addr] }

func (f *fakeOps) loadedModel(b LLMServer, addr string) string { return f.models[addr] }

func (f *fakeOps) liveDrift(b LLMServer, addr string, fresh ProfileParams) []string {
	return f.drift
}

func (f *fakeOps) discover(cfg *Config) []*RunningInstance { return f.instances }

func (f *fakeOps) start(cfg *Config, profile *ResolvedProfile) (*RunningInstance, error) {
	f.started = append(f.started, profile.Name)
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &RunningInstance{
		Backend: profile.Backend,
		Host:    *profile.Host,
		Port:    *profile.Port,
		PID:     4242,
		LogFile: "/logs/fake.log",
	}, nil
}

func (f *fakeOps) waitHealthy(b LLMServer, addr string, timeout time.Duration) error {
	f.waited = append(f.waited, addr)
	return f.waitErr
}

func (f *fakeOps) stop(addr string, progress ProgressFunc) (*RunningInstance, error) {
	f.stopped = append(f.stopped, addr)
	if f.stopErr != nil {
		return nil, f.stopErr
	}
	return &RunningInstance{}, nil
}

func (f *fakeOps) unloadInstance(addr string, progress ProgressFunc) (*RunningInstance, error) {
	f.unloadedInstances = append(f.unloadedInstances, addr)
	return &RunningInstance{}, nil
}

func (f *fakeOps) loadModel(b LLMServer, addr string, profile *ResolvedProfile) error {
	f.loadedModels = append(f.loadedModels, addr+" "+profile.ModelPath)
	return f.loadErr
}

func (f *fakeOps) unloadModel(b LLMServer, addr, modelID string) error {
	f.unloadedModels = append(f.unloadedModels, addr+" "+modelID)
	return nil
}

// orchProfile builds a resolved profile for the fake-driven orchestration
// tests. The backend name must be one of the registered backends — the
// orchestration resolves it via GetLLMServer — but no I/O ever reaches it:
// every effect goes through the fakeOps seam.
func orchProfile(backend, name, model, host string, port int) *ResolvedProfile {
	h, p := host, port
	return &ResolvedProfile{
		Name:          name,
		ModelPath:     model,
		Backend:       backend,
		ProfileParams: ProfileParams{Host: &h, Port: &p},
	}
}

func orchInstance(backend, host string, port int, model string) *RunningInstance {
	return &RunningInstance{Backend: backend, Host: host, Port: port, ActiveModel: model}
}

// TestLoadProfile_Orchestration_IdempotentNoOp pins ADR-0007 through the
// activation seam: a healthy target already serving the profile's model is
// a no-op — nothing is stopped, started, or loaded — even when drift is
// reported (the notice is informational; only --restart acts on it).
func TestLoadProfile_Orchestration_IdempotentNoOp(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, drift []string) *fakeOps {
		t.Helper()
		profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
		f := &fakeOps{
			healthyAddrs: map[string]bool{"127.0.0.1:8080": true},
			models:       map[string]string{"127.0.0.1:8080": "/models/test-7b.gguf"},
			drift:        drift,
			instances:    []*RunningInstance{orchInstance("llamacpp", "127.0.0.1", 8080, "/models/test-7b.gguf")},
		}
		inst, started, err := loadProfile(f, &Config{}, profile, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if started {
			t.Error("started = true, want false for an idempotent reload")
		}
		if inst == nil || inst.ActiveModel != "/models/test-7b.gguf" || inst.ActiveProfile != "chat" {
			t.Errorf("instance = %+v, want the live model and profile name", inst)
		}
		if len(f.stopped) != 0 || len(f.started) != 0 || len(f.loadedModels) != 0 || len(f.unloadedInstances) != 0 {
			t.Errorf("no-op must not touch anything: stopped=%v started=%v loaded=%v unloaded=%v",
				f.stopped, f.started, f.loadedModels, f.unloadedInstances)
		}
		return f
	}

	t.Run("matching model with no drift", func(t *testing.T) {
		t.Parallel()
		run(t, nil)
	})

	t.Run("drift notice does not trigger a restart", func(t *testing.T) {
		t.Parallel()
		run(t, []string{"context_size: 4096 → 8192"})
	})
}

// TestLoadProfile_Orchestration_Restart pins the --restart path for a
// managed backend: the same-backend instance at the target address is
// skipped by the auto-stop loop (it is the one being re-activated), then
// stopped exactly once by the managed path, and a fresh server is started
// and waited on.
func TestLoadProfile_Orchestration_Restart(t *testing.T) {
	t.Parallel()

	profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
	f := &fakeOps{
		healthyAddrs: map[string]bool{"127.0.0.1:8080": true},
		models:       map[string]string{"127.0.0.1:8080": "/models/test-7b.gguf"},
		instances:    []*RunningInstance{orchInstance("llamacpp", "127.0.0.1", 8080, "/models/test-7b.gguf")},
	}

	inst, started, err := loadProfile(f, &Config{}, profile, true, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !started {
		t.Error("started = false, want true for --restart")
	}
	if want := []string{"127.0.0.1:8080"}; !slices.Equal(f.stopped, want) {
		t.Errorf("stopped = %v, want exactly %v (once, from the managed path)", f.stopped, want)
	}
	if want := []string{"chat"}; !slices.Equal(f.started, want) {
		t.Errorf("started profiles = %v, want %v", f.started, want)
	}
	if want := []string{"127.0.0.1:8080"}; !slices.Equal(f.waited, want) {
		t.Errorf("waited = %v, want %v", f.waited, want)
	}
	if inst == nil || inst.ActiveProfile != "chat" || inst.ActiveModel != "/models/test-7b.gguf" {
		t.Errorf("instance = %+v, want active profile and model set", inst)
	}
}

// TestLoadProfile_Orchestration_AutoStop pins the auto_stop_server rule
// (ADR-0004/0006) through the seam: every other instance — including a
// foreign backend occupying the shared target address — is stopped before
// the target server is started; a stop failure aborts the activation.
func TestLoadProfile_Orchestration_AutoStop(t *testing.T) {
	t.Parallel()

	newFake := func() (*fakeOps, *ResolvedProfile) {
		profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
		f := &fakeOps{
			healthyAddrs: map[string]bool{}, // target not yet serving llamacpp
			instances: []*RunningInstance{
				orchInstance("ollama", "127.0.0.1", 8080, "llama3.1:8b"), // foreign occupant of the target
				orchInstance("ollama", "127.0.0.1", 11434, "llama3.1:8b"),
				orchInstance("llamacpp", "127.0.0.1", 8081, "/models/other.gguf"),
			},
		}
		return f, profile
	}

	t.Run("stops every other instance including the foreign occupant", func(t *testing.T) {
		t.Parallel()
		f, profile := newFake()
		_, started, err := loadProfile(f, &Config{}, profile, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("started = false, want true")
		}
		want := []string{"127.0.0.1:8080", "127.0.0.1:11434", "127.0.0.1:8081"}
		if !slices.Equal(f.stopped, want) {
			t.Errorf("stopped = %v, want %v", f.stopped, want)
		}
		if len(f.unloadedInstances) != 0 {
			t.Errorf("unloadedInstances = %v, want none under auto_stop", f.unloadedInstances)
		}
		if want := []string{"chat"}; !slices.Equal(f.started, want) {
			t.Errorf("started profiles = %v, want %v", f.started, want)
		}
	})

	t.Run("a failing stop aborts the activation", func(t *testing.T) {
		t.Parallel()
		f, profile := newFake()
		f.stopErr = errors.New("boom")
		_, _, err := loadProfile(f, &Config{}, profile, false, nil)
		if err == nil || !strings.Contains(err.Error(), "auto-stopping") {
			t.Errorf("err = %v, want auto-stopping failure", err)
		}
		if len(f.started) != 0 {
			t.Errorf("started profiles = %v, want none after a failed stop", f.started)
		}
	})
}

// TestLoadProfile_Orchestration_AutoUnload pins the auto_unload matrix with
// auto_stop_server disabled (ADR-0004's one rule): only external instances
// with a model loaded are unload candidates — managed instances (unload
// requires a restart, ADR-0003) and idle instances are skipped, and nothing
// is ever stopped. With both flags off, other instances are untouched.
func TestLoadProfile_Orchestration_AutoUnload(t *testing.T) {
	t.Parallel()

	off := false
	on := true

	newFake := func() (*fakeOps, *ResolvedProfile) {
		profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
		f := &fakeOps{
			healthyAddrs: map[string]bool{},
			instances: []*RunningInstance{
				orchInstance("ollama", "127.0.0.1", 11434, "llama3.1:8b"),     // external, loaded → unload
				orchInstance("llamacpp", "127.0.0.1", 8081, "/models/o.gguf"), // managed → skipped
				orchInstance("lmstudio", "127.0.0.1", 1234, ""),               // idle → skipped
			},
		}
		return f, profile
	}

	t.Run("auto_unload unloads only loaded external instances", func(t *testing.T) {
		t.Parallel()
		f, profile := newFake()
		cfg := &Config{AutoStopServer: &off, AutoUnload: &on}
		_, started, err := loadProfile(f, cfg, profile, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("started = false, want true")
		}
		if want := []string{"127.0.0.1:11434"}; !slices.Equal(f.unloadedInstances, want) {
			t.Errorf("unloadedInstances = %v, want %v", f.unloadedInstances, want)
		}
		if len(f.stopped) != 0 {
			t.Errorf("stopped = %v, want none with auto_stop_server: false", f.stopped)
		}
	})

	t.Run("both flags off leave other instances untouched", func(t *testing.T) {
		t.Parallel()
		f, profile := newFake()
		cfg := &Config{AutoStopServer: &off, AutoUnload: &off}
		_, started, err := loadProfile(f, cfg, profile, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("started = false, want true")
		}
		if len(f.stopped) != 0 || len(f.unloadedInstances) != 0 {
			t.Errorf("stopped=%v unloaded=%v, want none with both flags off", f.stopped, f.unloadedInstances)
		}
	})
}

// TestLoadProfile_Orchestration_External pins the external-backend fork:
// a healthy server with a different model gets a same-server swap (unload
// current, load new — no stop, no start), an unreachable one is started
// and then loaded.
func TestLoadProfile_Orchestration_External(t *testing.T) {
	t.Parallel()

	off := false
	on := true

	t.Run("healthy server swaps the model in place", func(t *testing.T) {
		t.Parallel()
		profile := orchProfile("ollama", "chat", "llama3.1:8b", "127.0.0.1", 11434)
		f := &fakeOps{
			healthyAddrs: map[string]bool{"127.0.0.1:11434": true},
			models:       map[string]string{"127.0.0.1:11434": "qwen2.5:7b"},
			instances:    []*RunningInstance{orchInstance("ollama", "127.0.0.1", 11434, "qwen2.5:7b")},
		}
		cfg := &Config{AutoStopServer: &off, AutoUnload: &on}
		inst, started, err := loadProfile(f, cfg, profile, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("started = false, want true for a model swap")
		}
		if want := []string{"127.0.0.1:11434 qwen2.5:7b"}; !slices.Equal(f.unloadedModels, want) {
			t.Errorf("unloadedModels = %v, want %v", f.unloadedModels, want)
		}
		if want := []string{"127.0.0.1:11434 llama3.1:8b"}; !slices.Equal(f.loadedModels, want) {
			t.Errorf("loadedModels = %v, want %v", f.loadedModels, want)
		}
		if len(f.stopped) != 0 || len(f.started) != 0 {
			t.Errorf("stopped=%v started=%v, want neither for an in-place swap", f.stopped, f.started)
		}
		if inst == nil || inst.ActiveProfile != "chat" {
			t.Errorf("instance = %+v, want active profile set", inst)
		}
	})

	t.Run("unreachable server is started before the load", func(t *testing.T) {
		t.Parallel()
		profile := orchProfile("ollama", "chat", "llama3.1:8b", "127.0.0.1", 11434)
		f := &fakeOps{healthyAddrs: map[string]bool{}}
		cfg := &Config{AutoStopServer: &off, AutoUnload: &on}
		_, started, err := loadProfile(f, cfg, profile, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("started = false, want true")
		}
		if want := []string{"chat"}; !slices.Equal(f.started, want) {
			t.Errorf("started profiles = %v, want %v", f.started, want)
		}
		if want := []string{"127.0.0.1:11434 llama3.1:8b"}; !slices.Equal(f.loadedModels, want) {
			t.Errorf("loadedModels = %v, want %v", f.loadedModels, want)
		}
		if len(f.unloadedModels) != 0 {
			t.Errorf("unloadedModels = %v, want none when nothing was loaded", f.unloadedModels)
		}
	})
}

// TestIdentifyBackend covers the stop path's backend identification: a
// llamacpp-shaped /health response claims the address for llamacpp, and an
// address nothing answers on yields ErrNotRunning.
func TestIdentifyBackend(t *testing.T) {
	t.Parallel()

	t.Run("fake llamacpp server is identified", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"ok"}`))
				return
			}
			// 404 elsewhere fails the Ollama ("/") and LM Studio
			// ("/v1/models") health checks, so only llamacpp matches.
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		backend, err := identifyBackend(addrFromURL(t, srv.URL))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backend != "llamacpp" {
			t.Errorf("backend = %q, want %q", backend, "llamacpp")
		}
	})

	t.Run("dead address yields ErrNotRunning", func(t *testing.T) {
		t.Parallel()
		if _, err := identifyBackend(deadAddr(t)); !errors.Is(err, ErrNotRunning) {
			t.Errorf("err = %v, want ErrNotRunning", err)
		}
	})
}

// TestStopInstance covers the decision layer ahead of any signalling: bad
// input and nothing-running both fail before a PID is ever looked up.
func TestStopInstance(t *testing.T) {
	t.Parallel()

	t.Run("invalid address", func(t *testing.T) {
		t.Parallel()
		_, err := StopInstance("garbage", nil)
		if err == nil || !strings.Contains(err.Error(), "invalid address") {
			t.Errorf("err = %v, want invalid-address error", err)
		}
	})

	t.Run("dead address yields ErrNotRunning", func(t *testing.T) {
		t.Parallel()
		if _, err := StopInstance(deadAddr(t), nil); !errors.Is(err, ErrNotRunning) {
			t.Errorf("err = %v, want ErrNotRunning", err)
		}
	})
}

// TestTerminatePID exercises the escalation's first rung against a real
// child: a process that honours SIGTERM is gone when terminatePID returns.
func TestTerminatePID(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap the child as soon as it exits: a zombie still counts as alive for
	// IsProcessAlive (kill(pid, 0) succeeds), which would stall the wait loop.
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })

	terminatePID(pid, nil)

	if IsProcessAlive(pid) {
		t.Errorf("PID %d still alive after terminatePID", pid)
	}
}

func TestParamDrift(t *testing.T) {
	t.Parallel()

	intPtr := func(n int) *int { return &n }
	boolPtr := func(b bool) *bool { return &b }
	floatPtr := func(f float64) *float64 { return &f }

	t.Run("identical params produce no drift", func(t *testing.T) {
		t.Parallel()
		p := ProfileParams{ContextSize: intPtr(8192), Temperature: floatPtr(0.7), FlashAttn: boolPtr(true)}
		if d := paramDrift(p, p); len(d) != 0 {
			t.Errorf("want no drift, got %v", d)
		}
	})

	t.Run("nil-vs-nil fields are ignored", func(t *testing.T) {
		t.Parallel()
		a := ProfileParams{ContextSize: intPtr(8192)}
		b := ProfileParams{ContextSize: intPtr(8192)}
		if d := paramDrift(a, b); len(d) != 0 {
			t.Errorf("want no drift for nil-vs-nil siblings, got %v", d)
		}
	})

	t.Run("changed int field appears in drift", func(t *testing.T) {
		t.Parallel()
		a := ProfileParams{ContextSize: intPtr(8192)}
		b := ProfileParams{ContextSize: intPtr(16384)}
		d := paramDrift(a, b)
		if len(d) != 1 || d[0] != "context_size: 8192 → 16384" {
			t.Errorf("unexpected drift: %v", d)
		}
	})

	t.Run("set-vs-unset is reported", func(t *testing.T) {
		t.Parallel()
		a := ProfileParams{ContextSize: intPtr(8192)}
		b := ProfileParams{}
		d := paramDrift(a, b)
		if len(d) != 1 || d[0] != "context_size: 8192 → (unset)" {
			t.Errorf("unexpected drift: %v", d)
		}
	})

	t.Run("bool and float fields are compared", func(t *testing.T) {
		t.Parallel()
		a := ProfileParams{FlashAttn: boolPtr(true), Temperature: floatPtr(0.7)}
		b := ProfileParams{FlashAttn: boolPtr(false), Temperature: floatPtr(0.3)}
		d := paramDrift(a, b)
		if len(d) != 2 {
			t.Fatalf("want 2 drifts, got %d: %v", len(d), d)
		}
	})

	t.Run("slot identity fields are not compared", func(t *testing.T) {
		t.Parallel()
		host := "127.0.0.1"
		host2 := "192.168.0.1"
		port1 := 8080
		port2 := 8081
		server1 := "llamacpp"
		server2 := "ollama"
		a := ProfileParams{Host: &host, Port: &port1, Server: &server1}
		b := ProfileParams{Host: &host2, Port: &port2, Server: &server2}
		if d := paramDrift(a, b); len(d) != 0 {
			t.Errorf("slot identity should not produce drift, got %v", d)
		}
	})
}

func TestLiveParamDrift(t *testing.T) {
	t.Parallel()

	intPtr := func(n int) *int { return &n }
	boolPtr := func(b bool) *bool { return &b }
	floatPtr := func(f float64) *float64 { return &f }

	// defaultBlockParams mirrors the shipped defaults block in
	// defaults/config.yaml: every llamacpp parameter set.
	defaultBlockParams := func() ProfileParams {
		return ProfileParams{
			GPULayers:     intPtr(99),
			Threads:       intPtr(8),
			ThreadsBatch:  intPtr(8),
			BatchSize:     intPtr(512),
			ContextSize:   intPtr(4096),
			FlashAttn:     boolPtr(true),
			ContBatching:  boolPtr(true),
			Parallel:      intPtr(1),
			Mlock:         boolPtr(false),
			NoMmap:        boolPtr(false),
			Embedding:     boolPtr(false),
			Jinja:         boolPtr(false),
			Temperature:   floatPtr(0.7),
			RepeatPenalty: floatPtr(1.1),
			TopK:          intPtr(40),
			TopP:          floatPtr(0.95),
			MinP:          floatPtr(0.05),
		}
	}

	propsServer := func(t *testing.T, body string) string {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/props" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return addrFromURL(t, srv.URL)
	}

	b := &LlamaCpp{}

	t.Run("unreported fields produce no drift on an idempotent load", func(t *testing.T) {
		t.Parallel()
		// Current llama-server /props: n_ctx (per slot) + total_slots, with
		// sampling nested under default_generation_settings.params; none of
		// gpu_layers/threads/flash_attn/... are reported. The server-default
		// sampling values (temp 0.8, repeat 1.0) deliberately differ from the
		// profile (0.7, 1.1): the launcher never passes sampling flags to
		// llama-server, so they must not be flagged as drift.
		addr := propsServer(t, `{
			"default_generation_settings": {
				"n_ctx": 4096,
				"params": {"temperature": 0.8, "top_k": 40, "top_p": 0.95, "min_p": 0.05, "repeat_penalty": 1.0}
			},
			"total_slots": 1,
			"model_path": "/models/x.gguf"
		}`)
		if d := liveParamDrift(b, addr, defaultBlockParams()); len(d) != 0 {
			t.Errorf("want no drift, got %v", d)
		}
	})

	t.Run("per-slot n_ctx does not drift when parallel > 1", func(t *testing.T) {
		t.Parallel()
		// llama-server reports n_ctx per slot: -c 4096 -np 2 shows n_ctx 2048.
		addr := propsServer(t, `{
			"default_generation_settings": {"n_ctx": 2048},
			"total_slots": 2
		}`)
		fresh := defaultBlockParams()
		fresh.ContextSize = intPtr(4096)
		fresh.Parallel = intPtr(2)
		if d := liveParamDrift(b, addr, fresh); len(d) != 0 {
			t.Errorf("want no drift for per-slot n_ctx, got %v", d)
		}
	})

	t.Run("old-style top-level sampling is ignored", func(t *testing.T) {
		t.Parallel()
		// Pre-refactor llama-server builds reported sampling at the top level
		// of default_generation_settings; those are excluded from the live
		// diff for the same reason as the nested form.
		addr := propsServer(t, `{
			"default_generation_settings": {"n_ctx": 4096, "temperature": 0.8, "top_k": 50},
			"total_slots": 1
		}`)
		if d := liveParamDrift(b, addr, defaultBlockParams()); len(d) != 0 {
			t.Errorf("want no drift, got %v", d)
		}
	})

	t.Run("genuine context_size drift is reported", func(t *testing.T) {
		t.Parallel()
		addr := propsServer(t, `{
			"default_generation_settings": {"n_ctx": 4096},
			"total_slots": 1
		}`)
		fresh := defaultBlockParams()
		fresh.ContextSize = intPtr(8192)
		d := liveParamDrift(b, addr, fresh)
		if len(d) != 1 || d[0] != "context_size: 4096 → 8192" {
			t.Errorf("unexpected drift: %v", d)
		}
	})

	t.Run("genuine parallel drift is reported", func(t *testing.T) {
		t.Parallel()
		addr := propsServer(t, `{
			"default_generation_settings": {"n_ctx": 4096},
			"total_slots": 2
		}`)
		fresh := defaultBlockParams()
		fresh.ContextSize = intPtr(8192)
		fresh.Parallel = intPtr(1)
		d := liveParamDrift(b, addr, fresh)
		if len(d) != 1 || d[0] != "parallel: 2 → 1" {
			t.Errorf("unexpected drift: %v", d)
		}
	})

	t.Run("backend without LiveParamsQuerier contributes no drift", func(t *testing.T) {
		t.Parallel()
		if d := liveParamDrift(&Ollama{}, "127.0.0.1:1", defaultBlockParams()); d != nil {
			t.Errorf("want nil drift for non-querier backend, got %v", d)
		}
	})
}
