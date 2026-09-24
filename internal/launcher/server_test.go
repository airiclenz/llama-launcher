package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

		got := readLastLines(path, 3, nil)
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

		got := readLastLines(path, 10, nil)
		want := "line1\nline2"
		if got != want {
			t.Errorf("readLastLines = %q, want %q", got, want)
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		t.Parallel()
		got := readLastLines("/nonexistent/path", 5, nil)
		if got != "(could not read log)" {
			t.Errorf("readLastLines = %q, want fallback message", got)
		}
	})
}

// crashingManagedServer is a managed backend whose "server" is a shell that
// echoes the key it was given, both bare and as an --api-key argv pair, and
// exits at once — the start-crash path that folds the log tail into an error.
type crashingManagedServer struct {
	startingStopServer
}

func (s *crashingManagedServer) ServerBinary(*Config) string { return "/bin/sh" }
func (s *crashingManagedServer) BuildServerArgs(*Config, *ResolvedProfile) []string {
	return []string{"-c", `echo "argv: --api-key $LAUNCHER_TEST_KEY --api-key extra-args-key"; echo "key $LAUNCHER_TEST_KEY"; exit 1`}
}
func (s *crashingManagedServer) BuildServerEnv(cfg *Config, profile *ResolvedProfile) []string {
	return []string{"LAUNCHER_TEST_KEY=" + cfg.APIKeyFor(profile.Backend)}
}

// TestStartManagedServer_CrashTailRedactsKeys asserts the start-crash error —
// readLastLines' one production caller — never carries the profile backend's
// configured key or an --api-key argv value.
func TestStartManagedServer_CrashTailRedactsKeys(t *testing.T) {
	t.Parallel()

	const sentinel = "sk-sentinel-crash-tail-91c2"
	stub := &crashingManagedServer{startingStopServer{
		name:     "crashtail",
		starting: func(string) bool { return false },
	}}
	host, portText, err := net.SplitHostPort(deadAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		Servers: map[string]ServerConfig{stub.name: {Enabled: true, APIKey: sentinel}},
		LogDir:  t.TempDir(),
	}
	profile := &ResolvedProfile{Backend: stub.name, ProfileParams: ProfileParams{Host: &host, Port: &port}}

	inst, err := startManagedServer(cfg, profile, stub)

	if err == nil {
		t.Fatalf("startManagedServer = %+v, want the start-crash error", inst)
	}
	msg := err.Error()
	if !strings.Contains(msg, "Log tail:") || !strings.Contains(msg, "[redacted]") {
		t.Fatalf("error is not a redacted start-crash tail: %v", err)
	}
	if strings.Contains(msg, sentinel) || strings.Contains(msg, "extra-args-key") {
		t.Errorf("start-crash error leaks a key: %v", err)
	}
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

	_, _, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, false, nil, nil)

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

// TestLoadProfile_StopsHealthySplashAnswering403 pins the refusal rule's
// boundary: a healthy Splash on the wildcard answers llamacpp's probe of
// 0.0.0.0 with 403, yet it is a foreign occupant to auto-stop — never an
// auth-refusing server — because Splash's own probe finds it healthy.
func TestLoadProfile_StopsHealthySplashAnswering403(t *testing.T) {
	// Not parallel: rewrites PATH.
	port := splashHostCheckingServer(t, http.StatusOK)
	host := "0.0.0.0"
	cfg := sharedAddrCfg(t, host, port, "llamacpp", "splash")
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

	_, _, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, false, nil, nil)

	if errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want no auth refusal while Splash is healthy there", err)
	}
	if want := fmt.Sprintf("%s:%d", host, port); len(stopped) != 1 || stopped[0] != want {
		t.Errorf("stopped addresses = %v, want exactly [%s]", stopped, want)
	}
}

// TestLoadProfile_RefusesAuthFailedServer: with the real probes, a target
// answering every backend with 401 is refused with the authFailedErr
// message and nothing is stopped.
func TestLoadProfile_RefusesAuthFailedServer(t *testing.T) {
	// Not parallel: rewrites PATH.
	host, port := authRefusingServer(t, http.StatusUnauthorized)
	cfg := sharedAddrCfg(t, host, port, "llamacpp", "ollama")
	profile := &ResolvedProfile{
		Name:          "test",
		ModelPath:     "/models/test.gguf",
		Backend:       "llamacpp",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}
	var stopped []string
	t.Setenv("PATH", t.TempDir())

	_, _, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, false, nil, nil)

	if !errors.Is(err, ErrAuthFailed) || !strings.Contains(err.Error(), "check api_key") {
		t.Fatalf("err = %v, want the authFailedErr message", err)
	}
	if len(stopped) != 0 {
		t.Errorf("stopped addresses = %v, want none", stopped)
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

	inst, started, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, false, nil, nil)
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
// stop it — via `llama-launcher stop`, never a manual kill (ADR-0010).
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
		"llama-launcher stop llamacpp",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "kill ") {
		t.Errorf("error %q still suggests a manual kill; ADR-0010 guidance is `llama-launcher stop`", err)
	}
}

// TestLoadProfile_StartupTimeoutIsErrStartupTimeout drives the real health
// wait through the production activation: the stand-in answers 503 forever
// (llama-server's reply while it loads a model), so the wait expires and
// loadProfileManaged decorates the failure exactly as it does in production.
// waitHealthy is the one activationOps operation realWaitOps keeps real (with
// a shortened wait window, because the production 30 s would stall the suite);
// everything else comes from the embedded fakeOps, so nothing is forked,
// signalled or discovered and the PID and log path in the message are the fake
// instance's. The poll loop, the 503s and the decoration are the real thing. A
// library client must be able to recognise this outcome with errors.Is,
// without matching on the message, and must still be told which server was
// left running.
func TestLoadProfile_StartupTimeoutIsErrStartupTimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`))
	}))
	defer srv.Close()

	host, port := hostPort(t, srv.URL)
	f := &fakeOps{}
	profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", host, port)

	_, started, err := loadProfile(realWaitOps{fakeOps: f, window: 1200 * time.Millisecond}, &Config{}, profile, false, nil, nil)

	if !errors.Is(err, ErrStartupTimeout) {
		t.Fatalf("err = %v, want it to wrap ErrStartupTimeout", err)
	}
	if started {
		t.Error("started = true, want false — the server never reported healthy")
	}
	for _, want := range []string{
		"did not become healthy",
		"PID 4242",
		"/logs/fake.log",
		"left running",
		"llama-launcher stop llamacpp",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q — the sentinel must not cost the message", err, want)
		}
	}
	if len(f.stopped) != 0 {
		t.Errorf("stopped = %v, want none — a timed-out start is left running so a slow load can finish", f.stopped)
	}
}

// TestStartupTimeoutErr_ManagedMessageUnchanged pins the managed arm's
// timeout text byte for byte: the external arm gained its own decoration,
// and the managed one must not drift with it.
func TestStartupTimeoutErr_ManagedMessageUnchanged(t *testing.T) {
	t.Parallel()

	base := errors.New("server at 127.0.0.1:8080 did not become healthy within 30s")
	inst := &RunningInstance{PID: 4242, Backend: "llamacpp", LogFile: "/logs/llamacpp.log"}

	err := startupTimeoutErr(base, inst)

	want := "server at 127.0.0.1:8080 did not become healthy within 30s\n" +
		"The server may still be loading its model — it was left running (PID 4242)\n" +
		"Log: /logs/llamacpp.log\n" +
		"Watch it with `llama-launcher logs llamacpp` and retry once it is healthy, or stop it with `llama-launcher stop llamacpp`"
	if err.Error() != want {
		t.Errorf("message =\n%s\nwant\n%s", err, want)
	}
	if !errors.Is(err, ErrStartupTimeout) {
		t.Error("managed timeout must wrap ErrStartupTimeout")
	}
}

// TestConnectExternal_TimeoutIsErrStartupTimeout drives the external arm with
// a backend whose TryStart succeeds and records a PID and log (Ollama's
// shape) but whose health check never passes: the timeout must be
// recognisable with errors.Is and name what was left running, and its
// guidance must not point at `logs`/`stop`, which cannot find a not-yet-
// healthy external server.
func TestConnectExternal_TimeoutIsErrStartupTimeout(t *testing.T) {
	t.Parallel()

	b := &fakeTrackingExternalBackend{pid: 4242, logFile: "/logs/ollama-fake.log"}
	profile := orchProfile("ollama", "chat", "llama3", "127.0.0.1", 11434)

	inst, err := connectExternalServer(&Config{}, profile, b, 10*time.Millisecond)

	if inst != nil {
		t.Errorf("inst = %+v, want nil on timeout", inst)
	}
	if !errors.Is(err, ErrStartupTimeout) {
		t.Fatalf("err = %v, want it to wrap ErrStartupTimeout", err)
	}
	for _, want := range []string{"did not become healthy", "left running", "(PID 4242)", "Log: /logs/ollama-fake.log"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	for _, reject := range []string{"llama-launcher logs", "llama-launcher stop"} {
		if strings.Contains(err.Error(), reject) {
			t.Errorf("error %q suggests %q, which answers \"No server running.\" for a not-yet-healthy external server", err, reject)
		}
	}
}

// TestConnectExternal_UntrackedTimeoutOmitsPIDAndLog covers an LM Studio-
// shaped backend, which implements no PIDTracker: the timeout still wraps
// ErrStartupTimeout, but prints neither a "PID 0" nor an empty "Log:" line.
func TestConnectExternal_UntrackedTimeoutOmitsPIDAndLog(t *testing.T) {
	t.Parallel()

	b := &fakeExternalBackend{}
	profile := orchProfile("lmstudio", "chat", "qwen", "127.0.0.1", 1234)

	_, err := connectExternalServer(&Config{}, profile, b, 10*time.Millisecond)

	if !errors.Is(err, ErrStartupTimeout) {
		t.Fatalf("err = %v, want it to wrap ErrStartupTimeout", err)
	}
	if !strings.Contains(err.Error(), "left running") {
		t.Errorf("error %q missing the left-running guidance", err)
	}
	for _, reject := range []string{"PID", "Log:"} {
		if strings.Contains(err.Error(), reject) {
			t.Errorf("error %q contains %q, want it omitted for a backend that tracks no PID", err, reject)
		}
	}
}

// TestConnectExternal_TryStartErrorSurfaces checks that a TryStart failure
// reaches the caller as itself — its text and any sentinel it wraps — rather
// than as the generic "start it manually" advice, and is not mistaken for a
// startup timeout.
func TestConnectExternal_TryStartErrorSurfaces(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		startErr error
		wantText string
		wantIs   error
	}{
		{"binary missing", errors.New("lms CLI not found in PATH"), "lms CLI not found in PATH", nil},
		{"unsupported platform", fmt.Errorf("starting a server process: %w", ErrUnsupported), "starting a server process", ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := &fakeExternalBackend{startErr: tc.startErr}
			profile := orchProfile("lmstudio", "chat", "qwen", "127.0.0.1", 1234)

			_, err := connectExternalServer(&Config{}, profile, b, 10*time.Millisecond)

			if !errors.Is(err, tc.startErr) {
				t.Fatalf("err = %v, want it to wrap the TryStart error", err)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.wantIs)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error %q missing %q", err, tc.wantText)
			}
			if strings.Contains(err.Error(), "start it manually") {
				t.Errorf("error %q still carries the generic not-reachable advice", err)
			}
			if errors.Is(err, ErrStartupTimeout) {
				t.Error("a TryStart failure must not read as a startup timeout")
			}
		})
	}
}

// fakeExternalBackend is a plain (non-managed) LLMServer whose health check
// never passes and whose TryStart returns startErr. It tracks no PID, like
// LM Studio.
type fakeExternalBackend struct {
	startErr error
}

func (f *fakeExternalBackend) Name() string                                 { return "fake" }
func (f *fakeExternalBackend) DisplayName() string                          { return "Fake" }
func (f *fakeExternalBackend) DefaultAddr() string                          { return "" }
func (f *fakeExternalBackend) HealthCheck(string) error                     { return errors.New("unreachable") }
func (f *fakeExternalBackend) ResolveModel(*Config, string) (string, error) { return "", nil }
func (f *fakeExternalBackend) LoadModel(string, *ResolvedProfile) error     { return nil }
func (f *fakeExternalBackend) UnloadModel(string, string) error             { return nil }
func (f *fakeExternalBackend) TryStart(*Config, string) error               { return f.startErr }
func (f *fakeExternalBackend) TryStop(string) error                         { return nil }
func (f *fakeExternalBackend) ParamSpecs() []ProfileParamSpec               { return nil }

// fakeTrackingExternalBackend adds PIDTracker to fakeExternalBackend, like
// Ollama, reporting a fixed PID and log path for the "spawned" server.
type fakeTrackingExternalBackend struct {
	fakeExternalBackend
	pid     int
	logFile string
}

func (f *fakeTrackingExternalBackend) LastStartedPID() int        { return f.pid }
func (f *fakeTrackingExternalBackend) LastStartedLogFile() string { return f.logFile }

// realWaitOps is a fakeOps whose health wait is the production one: it runs
// waitForHealth — the loop behind WaitForHealth, with the spawned server's
// exit as its liveness probe — against whatever is listening at addr,
// shortening the window the activation asks for to keep the test quick.
// Every other operation stays in memory, so nothing is forked or signalled.
type realWaitOps struct {
	*fakeOps
	window time.Duration
}

func (o realWaitOps) waitHealthy(b LLMServer, addr string, timeout time.Duration, exited <-chan struct{}) error {
	o.waited = append(o.waited, addr)
	return waitForHealth(b, addr, o.window, exited)
}

// exitingStartOps is a realWaitOps whose start forks a real `sh -c script`
// child in place of a server and attaches its reaped exit to the returned
// instance, exactly as startManagedServer does. The script stands in for a
// loading server that ends mid-wait — stopped by a signal, trapping SIGTERM
// and exiting 0, or crashing — while the health wait and the classification
// of that exit are the production ones.
type exitingStartOps struct {
	realWaitOps
	script  string
	logFile string
}

func (o exitingStartOps) start(cfg *Config, profile *ResolvedProfile) (*RunningInstance, error) {
	o.started = append(o.started, profile.Name)
	cmd := exec.Command("sh", "-c", o.script)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &RunningInstance{
		Backend: profile.Backend,
		Host:    *profile.Host,
		Port:    *profile.Port,
		PID:     cmd.Process.Pid,
		LogFile: o.logFile,
		exit:    watchExit(cmd),
	}, nil
}

// TestLoadProfile_ServerExitMidWaitEndsTheLoad drives the managed activation
// against a stand-in that answers 503 forever while the spawned child exits
// after one health poll. The load must return long before the wait window
// runs out: a stop — a signal, a clean exit 0 (Splash traps SIGTERM), a
// relayed 128+SIGTERM — is ErrLoadCanceled; a non-zero exit code is a crash
// carrying the redacted log tail. Neither is ErrStartupTimeout, and neither
// asks for a stop: the server is already gone.
func TestLoadProfile_ServerExitMidWaitEndsTheLoad(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("forks sh; no managed server is spawned on windows (ADR-0012)")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	host, port := hostPort(t, srv.URL)

	const apiKey = "sk-test-exit-secret"
	const window = 10 * time.Second

	tests := []struct {
		name         string
		script       string
		wantCanceled bool
	}{
		{"stopped by a signal", `sleep 0.6; kill -TERM $$`, true},
		{"exit status 0", `sleep 0.6; exit 0`, true},
		{"relayed SIGTERM exit code", `sleep 0.6; exit 143`, true},
		{"crash exit code", `sleep 0.6; exit 1`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logFile := filepath.Join(t.TempDir(), "server.log")
			if err := os.WriteFile(logFile, []byte("loading model\nfatal: bad tensor, key "+apiKey+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			f := &fakeOps{}
			ops := exitingStartOps{realWaitOps: realWaitOps{fakeOps: f, window: window}, script: tc.script, logFile: logFile}
			cfg := &Config{Servers: map[string]ServerConfig{"llamacpp": {APIKey: apiKey}}}
			profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", host, port)

			begin := time.Now()
			_, started, err := loadProfile(ops, cfg, profile, false, nil, nil)
			elapsed := time.Since(begin)

			if err == nil || started {
				t.Fatalf("loadProfile = started %v, err %v; want a failed load", started, err)
			}
			if elapsed > 3*time.Second {
				t.Errorf("load returned after %s, want well under the %s window", elapsed, window)
			}
			if errors.Is(err, ErrStartupTimeout) {
				t.Errorf("err = %v wraps ErrStartupTimeout; the server exited, it was not left running", err)
			}
			if got := errors.Is(err, ErrLoadCanceled); got != tc.wantCanceled {
				t.Errorf("errors.Is(err, ErrLoadCanceled) = %v, want %v (err: %v)", got, tc.wantCanceled, err)
			}
			if !tc.wantCanceled {
				if !strings.Contains(err.Error(), "exit status 1") || !strings.Contains(err.Error(), "fatal: bad tensor") {
					t.Errorf("crash error %q, want the exit status and the log tail", err)
				}
				if strings.Contains(err.Error(), apiKey) {
					t.Errorf("crash error %q leaks the api key; the tail must be redacted", err)
				}
			}
			if len(f.stopped) != 0 {
				t.Errorf("stopped = %v, want none", f.stopped)
			}
		})
	}
}

// TestLoadCanceled_ExitClassification pins serverExitErr's rule at its seam,
// including the cases no forked script produces: a nil Wait result (exit
// status 0) and a status that cannot be read are both a canceled load.
func TestLoadCanceled_ExitClassification(t *testing.T) {
	t.Parallel()

	inst := &RunningInstance{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, LogFile: filepath.Join(t.TempDir(), "missing.log")}
	tests := []struct {
		name    string
		waitErr error
	}{
		{"exit status 0", nil},
		{"unreadable status", errors.New("wait: no child processes")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := serverExitErr(&Config{}, inst, tc.waitErr)
			if !errors.Is(err, ErrLoadCanceled) {
				t.Errorf("err = %v, want it to wrap ErrLoadCanceled", err)
			}
			if errors.Is(err, ErrStartupTimeout) {
				t.Errorf("err = %v wraps ErrStartupTimeout", err)
			}
		})
	}
}

// TestLoadProfile_RefusesDoubleSpawnWhileStartingUp drives the real
// llamacpp StartingUp probe against a live 503 server (llama-server
// answers every request with 503 while loading) through loadProfile
// (ADR-0010): a plain load refuses with guidance and never touches the
// occupant; --restart attempts the displacement stop first, and when the
// occupant survives the stop (this test's stop is a recorder), the
// StartingUp backstop inside startManagedServer still refuses to fork a
// duplicate onto the occupied port.
func TestLoadProfile_RefusesDoubleSpawnWhileStartingUp(t *testing.T) {
	// Not parallel: rewrites PATH.

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	// Empty PATH: if a refusal regresses into a spawn attempt, the managed
	// start fails at the binary lookup with a distinguishable error instead
	// of forking.
	t.Setenv("PATH", t.TempDir())
	targetAddr := addrFromURL(t, srv.URL)

	t.Run("plain load refuses without touching the occupant", func(t *testing.T) {
		var stopped []string
		_, _, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, false, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "still starting up") {
			t.Fatalf("err = %v, want still-starting-up refusal", err)
		}
		if strings.Contains(err.Error(), "server binary not found") {
			t.Errorf("refusal must fire before the spawn attempt, got %v", err)
		}
		for _, want := range []string{"llama-launcher stop llamacpp", "--restart"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q missing the %q guidance", err, want)
			}
		}
		if strings.Contains(err.Error(), "kill ") {
			t.Errorf("refusal %q still suggests a manual kill; ADR-0010 guidance is `llama-launcher stop`", err)
		}
		if len(stopped) != 0 {
			t.Errorf("stopped addresses = %v, want none — a plain load never displaces a Starting occupant", stopped)
		}
	})

	t.Run("restart stops first; a survived occupant still blocks the spawn", func(t *testing.T) {
		var stopped []string
		_, _, err := loadProfile(stopRecordingOps{stopped: &stopped}, cfg, profile, true, nil, nil)
		if want := []string{targetAddr}; !slices.Equal(stopped, want) {
			t.Errorf("stopped addresses = %v, want %v — --restart displaces the Starting occupant", stopped, want)
		}
		// The recording stop left the 503 server alive, so the in-start
		// backstop must refuse rather than fork onto the occupied port.
		if err == nil || !strings.Contains(err.Error(), "still starting up") {
			t.Fatalf("err = %v, want the startManagedServer backstop refusal", err)
		}
		if strings.Contains(err.Error(), "server binary not found") {
			t.Errorf("backstop must fire before the spawn attempt, got %v", err)
		}
	})
}

// fakeOps is a pure in-memory activationOps: every operation records its
// invocation and answers from the configured fields, so the orchestration
// tests exercise loadProfile's decision logic without forking a process,
// sending a signal, or opening a socket.
type fakeOps struct {
	healthyAddrs  map[string]bool   // addr → backend's own server answers there
	startingAddrs map[string]bool   // addr → still-starting server answers there (ADR-0010)
	authAddrs     map[string]bool   // addr → the server there answers only 401/403
	occupants     map[string]string // addr → backend identify names there
	models        map[string]string // addr → currently loaded model
	drift         []string          // liveDrift result for any addr
	instances     []*RunningInstance
	startErr      error
	waitErr       error
	stopErr       error
	loadErr       error

	stopped           []string // stop targets, in call order
	unloadedInstances []string // unloadInstance targets
	started           []string // profile names passed to start
	waited            []string // addresses waited on
	loadedModels      []string // "addr model" per loadModel call
	unloadedModels    []string // "addr model" per unloadModel call
}

func (f *fakeOps) healthy(b LLMServer, addr string) bool { return f.healthyAddrs[addr] }

func (f *fakeOps) starting(b LLMServer, addr string) bool { return f.startingAddrs[addr] }

func (f *fakeOps) loadedModel(b LLMServer, addr string) string { return f.models[addr] }

func (f *fakeOps) liveDrift(b LLMServer, addr string, fresh ProfileParams) []string {
	return f.drift
}

func (f *fakeOps) discover(cfg *Config) []*RunningInstance { return f.instances }

func (f *fakeOps) authRefusal(cfg *Config, addr string) error {
	if f.authAddrs[addr] {
		return fakeAuthErr(addr)
	}
	return nil
}

func (f *fakeOps) identify(addr string) (string, error) {
	if f.authAddrs[addr] {
		return "llamacpp", fakeAuthErr(addr)
	}
	if occupant, ok := f.occupants[addr]; ok {
		return occupant, nil
	}
	return "", ErrNotRunning
}

// fakeAuthErr is the error identifyBackend and authRefusalAt report for a
// server at addr that answers only 401/403.
func fakeAuthErr(addr string) error {
	return fmt.Errorf("server at %s: %w", addr, authFailedErr(http.StatusUnauthorized))
}

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

func (f *fakeOps) waitHealthy(b LLMServer, addr string, timeout time.Duration, exited <-chan struct{}) error {
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
		inst, started, err := loadProfile(f, &Config{}, profile, false, nil, nil)
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

// TestLoadProfile_Orchestration_LongLiveModelIDMatches: a live model id over
// maxModelIDBytes is compared whole, so it still matches the profile and the
// load stays a no-op; only the returned ActiveModel is bounded.
func TestLoadProfile_Orchestration_LongLiveModelIDMatches(t *testing.T) {
	t.Parallel()
	longPath := "/" + strings.Repeat("d/", maxModelIDBytes) + "long-7b.gguf"
	profile := orchProfile("llamacpp", "long", longPath, "127.0.0.1", 8080)
	f := &fakeOps{
		healthyAddrs: map[string]bool{"127.0.0.1:8080": true},
		models:       map[string]string{"127.0.0.1:8080": longPath},
		instances:    []*RunningInstance{orchInstance("llamacpp", "127.0.0.1", 8080, longPath)},
	}

	inst, started, err := loadProfile(f, &Config{}, profile, false, nil, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if started || len(f.stopped) != 0 || len(f.started) != 0 || len(f.loadedModels) != 0 {
		t.Errorf("a matching long id must be a no-op: started=%t stopped=%v spawned=%v loaded=%v",
			started, f.stopped, f.started, f.loadedModels)
	}
	if inst == nil || inst.ActiveProfile != "long" {
		t.Fatalf("instance = %+v, want the matched profile", inst)
	}
	if inst.ActiveModel != boundModelID(longPath) {
		t.Errorf("ActiveModel is %d bytes, want the bounded %d-byte id", len(inst.ActiveModel), len(boundModelID(longPath)))
	}
}

// TestLoadProfile_Orchestration_DriftNoticeReachesSink is the sibling of the
// idempotent-no-op test: on the same ADR-0007 path the drift notice travels
// to the caller's NoticeFunc as a single call carrying the whole formatted
// text, so a library client can render it wherever it wants.
func TestLoadProfile_Orchestration_DriftNoticeReachesSink(t *testing.T) {
	t.Parallel()

	profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
	f := &fakeOps{
		healthyAddrs: map[string]bool{"127.0.0.1:8080": true},
		models:       map[string]string{"127.0.0.1:8080": "/models/test-7b.gguf"},
		drift:        []string{"context_size: 4096 → 8192"},
		instances:    []*RunningInstance{orchInstance("llamacpp", "127.0.0.1", 8080, "/models/test-7b.gguf")},
	}

	var notices []string
	_, _, err := loadProfile(f, &Config{}, profile, false, nil, func(n string) {
		notices = append(notices, n)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly one carrying the full notice text", notices)
	}
	if !strings.Contains(notices[0], "context_size") {
		t.Errorf("notice = %q, want the drifted field name", notices[0])
	}
	if !strings.Contains(notices[0], "--restart") {
		t.Errorf("notice = %q, want the --restart guidance", notices[0])
	}
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

	inst, started, err := loadProfile(f, &Config{}, profile, true, nil, nil)
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
		_, started, err := loadProfile(f, &Config{}, profile, false, nil, nil)
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

	t.Run("an AuthFailed row is skipped while a healthy foreign occupant is still stopped", func(t *testing.T) {
		t.Parallel()
		f, profile := newFake()
		f.instances = []*RunningInstance{
			{Host: "127.0.0.1", Port: 9000, AuthFailed: true},
			orchInstance("ollama", "127.0.0.1", 8080, "llama3.1:8b"), // healthy foreign occupant of the target
		}

		_, _, err := loadProfile(f, &Config{}, profile, false, nil, nil)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []string{"127.0.0.1:8080"}; !slices.Equal(f.stopped, want) {
			t.Errorf("stopped = %v, want only the foreign occupant %v", f.stopped, want)
		}
	})

	t.Run("a failing stop aborts the activation", func(t *testing.T) {
		t.Parallel()
		f, profile := newFake()
		f.stopErr = errors.New("boom")
		_, _, err := loadProfile(f, &Config{}, profile, false, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "auto-stopping") {
			t.Errorf("err = %v, want auto-stopping failure", err)
		}
		if len(f.started) != 0 {
			t.Errorf("started profiles = %v, want none after a failed stop", f.started)
		}
	})
}

// TestLoadProfile_Orchestration_AuthFailed: a target address whose server
// refuses the api_key (discovery's rule, through the authRefusal hook) is
// refused with the auth error before anything is stopped, started or
// loaded; a healthy or Starting profile backend there never consults it.
func TestLoadProfile_Orchestration_AuthFailed(t *testing.T) {
	t.Parallel()

	for _, backend := range []string{"llamacpp", "ollama"} {
		t.Run(backend+": refused with the auth error", func(t *testing.T) {
			t.Parallel()
			profile := orchProfile(backend, "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
			f := &fakeOps{
				authAddrs: map[string]bool{"127.0.0.1:8080": true},
				instances: []*RunningInstance{orchInstance("ollama", "127.0.0.1", 11434, "llama3.1:8b")},
			}

			_, started, err := loadProfile(f, &Config{}, profile, false, nil, nil)

			if !errors.Is(err, ErrAuthFailed) || !strings.Contains(err.Error(), "check api_key") {
				t.Fatalf("err = %v, want the authFailedErr message", err)
			}
			if started {
				t.Error("started = true on a refusal")
			}
			if len(f.stopped) != 0 || len(f.started) != 0 || len(f.loadedModels) != 0 {
				t.Errorf("stopped=%v started=%v loaded=%v, want no effects", f.stopped, f.started, f.loadedModels)
			}
		})
	}

	t.Run("a healthy profile backend is never refused", func(t *testing.T) {
		t.Parallel()
		profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
		f := &fakeOps{
			healthyAddrs: map[string]bool{"127.0.0.1:8080": true},
			authAddrs:    map[string]bool{"127.0.0.1:8080": true},
		}

		if _, _, err := loadProfile(f, &Config{}, profile, false, nil, nil); err != nil {
			t.Errorf("err = %v, want the activation to proceed", err)
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
		_, started, err := loadProfile(f, cfg, profile, false, nil, nil)
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
		_, started, err := loadProfile(f, cfg, profile, false, nil, nil)
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

// TestLoadProfile_Orchestration_StartingOccupant pins the ADR-0010
// activation matrix around Starting instances: a plain load onto a
// Starting target refuses (same or different profile — a Starting
// instance exposes no model that could tell them apart) and touches
// nothing, --restart displaces the occupant (stop, then start, then
// wait), the auto_stop_server sweep stops a Starting instance at another
// address like any other, and auto_unload never touches a Starting
// instance — it has no active model to unload.
func TestLoadProfile_Orchestration_StartingOccupant(t *testing.T) {
	t.Parallel()

	const target = "127.0.0.1:8080"
	startingOccupant := func() *RunningInstance {
		return &RunningInstance{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, Starting: true}
	}

	t.Run("plain load refuses regardless of profile", func(t *testing.T) {
		t.Parallel()
		for _, profile := range []*ResolvedProfile{
			orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080),
			orchProfile("llamacpp", "coder", "/models/coder-13b.gguf", "127.0.0.1", 8080),
		} {
			f := &fakeOps{
				startingAddrs: map[string]bool{target: true},
				instances:     []*RunningInstance{startingOccupant()},
			}

			_, started, err := loadProfile(f, &Config{}, profile, false, nil, nil)

			if err == nil || !strings.Contains(err.Error(), "still starting up") {
				t.Errorf("profile %s: err = %v, want still-starting-up refusal", profile.Name, err)
			}
			if started {
				t.Errorf("profile %s: started = true, want false", profile.Name)
			}
			if len(f.stopped) != 0 || len(f.started) != 0 {
				t.Errorf("profile %s: stopped=%v started=%v, want neither — the sweep skips the target occupant and a plain load never displaces it",
					profile.Name, f.stopped, f.started)
			}
		}
	})

	t.Run("--restart displaces: stop, start, wait", func(t *testing.T) {
		t.Parallel()
		profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
		f := &fakeOps{
			startingAddrs: map[string]bool{target: true},
			instances:     []*RunningInstance{startingOccupant()},
		}

		inst, started, err := loadProfile(f, &Config{}, profile, true, nil, nil)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("started = false, want true for --restart displacement")
		}
		if want := []string{target}; !slices.Equal(f.stopped, want) {
			t.Errorf("stopped = %v, want exactly %v (once, from the managed displacement)", f.stopped, want)
		}
		if want := []string{"chat"}; !slices.Equal(f.started, want) {
			t.Errorf("started profiles = %v, want %v", f.started, want)
		}
		if want := []string{target}; !slices.Equal(f.waited, want) {
			t.Errorf("waited = %v, want %v", f.waited, want)
		}
		if inst == nil || inst.ActiveProfile != "chat" || inst.ActiveModel != "/models/test-7b.gguf" {
			t.Errorf("instance = %+v, want active profile and model set", inst)
		}
	})

	t.Run("auto_stop_server sweeps a Starting instance at another address", func(t *testing.T) {
		t.Parallel()
		profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
		f := &fakeOps{
			instances: []*RunningInstance{
				{Backend: "llamacpp", Host: "127.0.0.1", Port: 8081, Starting: true},
			},
		}

		_, started, err := loadProfile(f, &Config{}, profile, false, nil, nil)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("started = false, want true")
		}
		if want := []string{"127.0.0.1:8081"}; !slices.Equal(f.stopped, want) {
			t.Errorf("stopped = %v, want %v — the sweep includes Starting instances", f.stopped, want)
		}
	})

	t.Run("auto_unload skips a Starting instance", func(t *testing.T) {
		t.Parallel()
		off := false
		on := true
		profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
		// An external-backend instance, so the managed-backend skip in
		// shouldCrossServerUnload cannot mask the gate under test: a
		// Starting instance reports no ActiveModel (ADR-0010), and that
		// empty-model gate is what keeps auto_unload off it.
		f := &fakeOps{
			instances: []*RunningInstance{
				{Backend: "ollama", Host: "127.0.0.1", Port: 11434, Starting: true},
			},
		}
		cfg := &Config{AutoStopServer: &off, AutoUnload: &on}

		_, started, err := loadProfile(f, cfg, profile, false, nil, nil)

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("started = false, want true")
		}
		if len(f.unloadedInstances) != 0 || len(f.stopped) != 0 {
			t.Errorf("unloaded=%v stopped=%v, want neither for a Starting instance under auto_unload",
				f.unloadedInstances, f.stopped)
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
		inst, started, err := loadProfile(f, cfg, profile, false, nil, nil)
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
		_, started, err := loadProfile(f, cfg, profile, false, nil, nil)
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

	t.Run("start failure surfaces and reports not started", func(t *testing.T) {
		t.Parallel()
		profile := orchProfile("ollama", "chat", "llama3.1:8b", "127.0.0.1", 11434)
		f := &fakeOps{
			healthyAddrs: map[string]bool{},
			startErr:     errors.New("ollama binary not found in PATH"),
		}
		cfg := &Config{AutoStopServer: &off, AutoUnload: &on}
		_, started, err := loadProfile(f, cfg, profile, false, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "ollama binary not found") {
			t.Fatalf("err = %v, want the start failure surfaced", err)
		}
		if started {
			t.Error("started = true, want false on start failure")
		}
		if len(f.loadedModels) != 0 {
			t.Errorf("loadedModels = %v, want none after a failed start", f.loadedModels)
		}
	})
}

// steppedOps wraps fakeOps so its stop/unload mechanics emit progress
// steps, letting the unified Stop/Unload tests assert that the steps are
// collected into the StopResult instead of streamed to a UI callback.
type steppedOps struct {
	*fakeOps
}

func (s steppedOps) stop(addr string, progress ProgressFunc) (*RunningInstance, error) {
	reportStep(progress, "Sending stop signal")
	reportStep(progress, "Disconnecting")
	return s.fakeOps.stop(addr, progress)
}

func (s steppedOps) unloadInstance(addr string, progress ProgressFunc) (*RunningInstance, error) {
	reportStep(progress, "Unloading model")
	return s.fakeOps.unloadInstance(addr, progress)
}

// TestUnload_Orchestration pins the single home of the "unload on a
// managed backend means stop the server" rule (ADR-0003/0004) behind the
// unified Unload entry point: a managed backend's unload stops the
// server, an external backend's unload leaves it running, and the steps
// the mechanics reported come back in the result for after-the-fact
// rendering by the CLI and menu.
func TestUnload_Orchestration(t *testing.T) {
	t.Parallel()

	t.Run("managed backend: unload stops the server", func(t *testing.T) {
		t.Parallel()
		f := &fakeOps{occupants: map[string]string{"127.0.0.1:8080": "llamacpp"}}
		res, err := unloadServerModel(steppedOps{f}, "llamacpp", "127.0.0.1:8080")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.ServerStopped {
			t.Error("ServerStopped = false, want true for a managed backend")
		}
		if want := []string{"127.0.0.1:8080"}; !slices.Equal(f.stopped, want) {
			t.Errorf("stopped = %v, want %v", f.stopped, want)
		}
		if len(f.unloadedInstances) != 0 {
			t.Errorf("unloadedInstances = %v, want none for a managed backend", f.unloadedInstances)
		}
		if want := []string{"Sending stop signal", "Disconnecting"}; !slices.Equal(res.Steps, want) {
			t.Errorf("Steps = %v, want %v", res.Steps, want)
		}
	})

	t.Run("external backend: unload keeps the server running", func(t *testing.T) {
		t.Parallel()
		f := &fakeOps{occupants: map[string]string{"127.0.0.1:11434": "ollama"}}
		res, err := unloadServerModel(steppedOps{f}, "ollama", "127.0.0.1:11434")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ServerStopped {
			t.Error("ServerStopped = true, want false for an external backend")
		}
		if want := []string{"127.0.0.1:11434"}; !slices.Equal(f.unloadedInstances, want) {
			t.Errorf("unloadedInstances = %v, want %v", f.unloadedInstances, want)
		}
		if len(f.stopped) != 0 {
			t.Errorf("stopped = %v, want none for an external backend", f.stopped)
		}
		if want := []string{"Unloading model"}; !slices.Equal(res.Steps, want) {
			t.Errorf("Steps = %v, want %v", res.Steps, want)
		}
	})

	for _, backend := range []string{"llamacpp", "ollama"} {
		t.Run(backend+": an auth-refusing server is refused before any mechanics", func(t *testing.T) {
			t.Parallel()
			addr := "127.0.0.1:8080"
			f := &fakeOps{authAddrs: map[string]bool{addr: true}}

			res, err := unloadServerModel(steppedOps{f}, backend, addr)

			if !errors.Is(err, ErrAuthFailed) || !strings.Contains(err.Error(), "check api_key") {
				t.Fatalf("err = %v, want the authFailedErr message", err)
			}
			if res == nil {
				t.Fatal("result must be non-nil on error")
			}
			if len(f.stopped) != 0 || len(f.unloadedInstances) != 0 {
				t.Errorf("stopped=%v unloaded=%v, want nothing stopped or unloaded", f.stopped, f.unloadedInstances)
			}
		})
	}

	t.Run("another backend at the address is refused before any mechanics", func(t *testing.T) {
		t.Parallel()
		addr := "127.0.0.1:8080"
		f := &fakeOps{occupants: map[string]string{addr: "ollama"}}

		res, err := unloadServerModel(steppedOps{f}, "llamacpp", addr)

		if !errors.Is(err, ErrNotRunning) {
			t.Fatalf("err = %v, want it to wrap ErrNotRunning", err)
		}
		if want := "no llamacpp server at 127.0.0.1:8080 (ollama is serving there)"; err.Error() != want {
			t.Errorf("err = %q, want %q", err, want)
		}
		if res == nil {
			t.Fatal("result must be non-nil on error")
		}
		if len(f.stopped) != 0 || len(f.unloadedInstances) != 0 {
			t.Errorf("stopped=%v unloaded=%v, want nothing stopped or unloaded", f.stopped, f.unloadedInstances)
		}
	})

	t.Run("nothing identified at the address falls through to the mechanics", func(t *testing.T) {
		t.Parallel()
		f := &fakeOps{}

		res, err := unloadServerModel(steppedOps{f}, "llamacpp", "127.0.0.1:8080")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.ServerStopped || !slices.Equal(f.stopped, []string{"127.0.0.1:8080"}) {
			t.Errorf("ServerStopped=%v stopped=%v, want the managed stop to run", res.ServerStopped, f.stopped)
		}
	})

	t.Run("unknown backend fails with a non-nil result", func(t *testing.T) {
		t.Parallel()
		f := &fakeOps{}
		res, err := unloadServerModel(steppedOps{f}, "doesnotexist", "127.0.0.1:1")
		if err == nil {
			t.Fatal("want error for an unknown backend")
		}
		if res == nil {
			t.Fatal("result must be non-nil on error")
		}
		if len(f.stopped) != 0 || len(f.unloadedInstances) != 0 {
			t.Errorf("stopped=%v unloaded=%v, want no mechanics run", f.stopped, f.unloadedInstances)
		}
	})

	t.Run("a failing stop surfaces with the steps taken so far", func(t *testing.T) {
		t.Parallel()
		f := &fakeOps{stopErr: errors.New("boom")}
		res, err := unloadServerModel(steppedOps{f}, "llamacpp", "127.0.0.1:8080")
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("err = %v, want the stop failure", err)
		}
		if want := []string{"Sending stop signal", "Disconnecting"}; !slices.Equal(res.Steps, want) {
			t.Errorf("Steps = %v, want the steps taken before the failure %v", res.Steps, want)
		}
	})
}

// TestStop_Orchestration pins the unified Stop entry point: the target
// address is stopped through the seam and the mechanics' steps come back
// in the result.
func TestStop_Orchestration(t *testing.T) {
	t.Parallel()

	f := &fakeOps{}
	res, err := stopServer(steppedOps{f}, "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.ServerStopped {
		t.Error("ServerStopped = false, want true for Stop")
	}
	if want := []string{"127.0.0.1:8080"}; !slices.Equal(f.stopped, want) {
		t.Errorf("stopped = %v, want %v", f.stopped, want)
	}
	if want := []string{"Sending stop signal", "Disconnecting"}; !slices.Equal(res.Steps, want) {
		t.Errorf("Steps = %v, want %v", res.Steps, want)
	}
	if res.Instance == nil {
		t.Error("Instance = nil, want the instance the mechanics reported")
	}
}

// TestIdentifyBackend covers the stop path's backend identification: a
// llamacpp-shaped /health response claims the address for llamacpp, a
// still-loading llama-server is claimed via the StartupProber second pass
// (ADR-0010), and an address nothing answers on yields ErrNotRunning. The
// 401/403 third pass is TestIdentifyBackend_AuthPassNeedsConfiguredAddress's:
// it reads the process-global configured-address snapshot, which a parallel
// subtest cannot pin.
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

	t.Run("still-loading llamacpp server is identified via the startup probe", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// llama-server answers every request with 503 while loading its
			// model. That fails every backend's health check, so only the
			// StartupProber second pass can claim the address (ADR-0010).
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"code":503,"message":"Loading model","type":"unavailable_error"}}`))
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

	t.Run("Splash server is identified as splash, not llamacpp", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Splash sends its Server header on every reply and answers
			// /health with the same body llama-server does.
			w.Header().Set("Server", "Splash")
			switch r.URL.Path {
			case "/health":
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"ok"}`))
			case "/ready":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		backend, err := identifyBackend(addrFromURL(t, srv.URL))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backend != "splash" {
			t.Errorf("backend = %q, want %q", backend, "splash")
		}
	})

	t.Run("wildcard-bound Splash server is identified as splash", func(t *testing.T) {
		t.Parallel()
		// Splash 403s a Host naming the wildcard, so the probe must dial
		// loopback while the caller keeps the configured 0.0.0.0 address.
		port := splashHostCheckingServer(t, http.StatusOK)

		backend, err := identifyBackend(fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backend != "splash" {
			t.Errorf("backend = %q, want %q", backend, "splash")
		}
	})

	t.Run("loading Splash server is identified as splash, not llamacpp", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Server", "Splash")
			switch r.URL.Path {
			case "/health":
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"ok"}`))
			case "/ready":
				w.WriteHeader(http.StatusServiceUnavailable)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		backend, err := identifyBackend(addrFromURL(t, srv.URL))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if backend != "splash" {
			t.Errorf("backend = %q, want %q", backend, "splash")
		}
	})

	t.Run("a Starting server outranks a 401 from another backend's path", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		backend, err := identifyBackend(addrFromURL(t, srv.URL))

		if err != nil || backend != "llamacpp" {
			t.Errorf("identifyBackend = %q, %v; want llamacpp from the startup pass", backend, err)
		}
	})

	t.Run("dead address yields ErrNotRunning", func(t *testing.T) {
		t.Parallel()
		if _, err := identifyBackend(deadAddr(t)); !errors.Is(err, ErrNotRunning) {
			t.Errorf("err = %v, want ErrNotRunning", err)
		}
	})
}

// TestIdentifyBackend_AuthPassNeedsConfiguredAddress pins the third pass's
// scope (ADR-0010's "at a configured address"): a server answering every
// request with 401 is ErrNotRunning at an address no config names, and at a
// configured one it is the sort-first configured backend's, with the auth
// error. Not parallel: it pins the process-global configured-address
// snapshot.
func TestIdentifyBackend_AuthPassNeedsConfiguredAddress(t *testing.T) {
	host, port := authRefusingServer(t, http.StatusUnauthorized)
	addr := fmt.Sprintf("%s:%d", host, port)

	t.Run("unconfigured address is ErrNotRunning", func(t *testing.T) {
		pinConfiguredTargets(t, &Config{})

		backend, err := identifyBackend(addr)

		if !errors.Is(err, ErrNotRunning) || errors.Is(err, ErrAuthFailed) {
			t.Errorf("err = %v, want ErrNotRunning and no auth error", err)
		}
		if backend != "" {
			t.Errorf("backend = %q, want none", backend)
		}
	})

	tests := []struct {
		name        string
		configured  string
		wantBackend string
	}{
		// The configured backend that answered 401 carries the stop
		// verification; it does not identify the server.
		{"configured for llamacpp is llamacpp's", "llamacpp", "llamacpp"},
		{"configured for ollama only is ollama's, not the sort-first llamacpp", "ollama", "ollama"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pinConfiguredTargets(t, startingCfg(t, tc.configured, addr))

			backend, err := identifyBackend(addr)

			if !errors.Is(err, ErrAuthFailed) || !strings.Contains(err.Error(), "check api_key") {
				t.Fatalf("err = %v, want the authFailedErr message wrapping ErrAuthFailed", err)
			}
			if backend != tc.wantBackend {
				t.Errorf("backend = %q, want %q", backend, tc.wantBackend)
			}
		})
	}
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
	identity, err := processIdentity(pid)
	if err != nil {
		t.Fatalf("processIdentity(%d): %v", pid, err)
	}

	terminatePID(pid, identity, nil)

	if IsProcessAlive(pid) {
		t.Errorf("PID %d still alive after terminatePID", pid)
	}
}

// TestTerminatePID_MismatchedIdentity stands in for a reused PID: a real
// child whose start time differs from the recorded identity is never
// signalled, and terminatePID returns without waiting out the escalation.
func TestTerminatePID_MismatchedIdentity(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	pid := cmd.Process.Pid
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })
	identity, err := processIdentity(pid)
	if err != nil {
		t.Fatalf("processIdentity(%d): %v", pid, err)
	}

	started := time.Now()
	terminatePID(pid, identity+1, nil)

	if !IsProcessAlive(pid) {
		t.Errorf("PID %d was signalled despite a mismatching identity", pid)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("terminatePID took %v on a mismatch, want an immediate return", elapsed)
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

// hookStopServer is a registry stub whose health flips to stopped when its
// TryStop hook runs, standing in for a backend (like LM Studio) whose native
// stop command — not a PID signal — is what actually stops the server.
type hookStopServer struct {
	name     string
	stopped  bool
	tryStops []string
}

func (s *hookStopServer) Name() string        { return s.name }
func (s *hookStopServer) DisplayName() string { return s.name }
func (s *hookStopServer) DefaultAddr() string { return "localhost:0" }
func (s *hookStopServer) HealthCheck(string) error {
	if s.stopped {
		return errors.New("stopped")
	}
	return nil
}
func (s *hookStopServer) ResolveModel(_ *Config, ref string) (string, error) { return ref, nil }
func (s *hookStopServer) LoadModel(string, *ResolvedProfile) error           { return nil }
func (s *hookStopServer) UnloadModel(string, string) error                   { return nil }
func (s *hookStopServer) TryStart(*Config, string) error                     { return nil }
func (s *hookStopServer) TryStop(addr string) error {
	s.tryStops = append(s.tryStops, addr)
	s.stopped = true
	return nil
}
func (s *hookStopServer) ParamSpecs() []ProfileParamSpec { return nil }

// TestStopServerAt_TryStopFlipsHealthCheck: when no process listens at the
// address (nothing for the PID path to signal) but the backend's stop hook
// does stop the server, stopServerAt reports success — the hook is a real
// stop mechanism, not just best-effort cleanup after the signal. Not
// parallel: it mutates the global llmServers registry.
func TestStopServerAt_TryStopFlipsHealthCheck(t *testing.T) {
	stub := &hookStopServer{name: "hookstop"}
	RegisterLLMServer(stub)
	t.Cleanup(func() { delete(llmServers, stub.name) })

	_, err := stopServerAt(stub.name, "127.0.0.1:1", true, nil)

	if err != nil {
		t.Fatalf("stopServerAt = %v, want nil once TryStop makes the health check fail", err)
	}
	if want := []string{"127.0.0.1:1"}; !slices.Equal(stub.tryStops, want) {
		t.Errorf("TryStop calls = %v, want %v", stub.tryStops, want)
	}
}

// startingStopServer is a registry stub for a managed-style backend whose
// server answers 503 while loading (ADR-0010): HealthCheck always fails —
// exactly like a real Starting llama-server's — and StartingUp delegates to
// the configured probe, so each test controls whether the stop verification
// sees a survived Starting server.
type startingStopServer struct {
	name     string
	starting func(addr string) bool
	tryStops []string
}

func (s *startingStopServer) Name() string        { return s.name }
func (s *startingStopServer) DisplayName() string { return s.name }
func (s *startingStopServer) DefaultAddr() string { return "localhost:0" }
func (s *startingStopServer) HealthCheck(string) error {
	return errors.New("unhealthy: status 503")
}
func (s *startingStopServer) ResolveModel(_ *Config, ref string) (string, error) { return ref, nil }
func (s *startingStopServer) LoadModel(string, *ResolvedProfile) error           { return nil }
func (s *startingStopServer) UnloadModel(string, string) error                   { return nil }
func (s *startingStopServer) TryStart(*Config, string) error                     { return nil }
func (s *startingStopServer) TryStop(addr string) error {
	s.tryStops = append(s.tryStops, addr)
	return nil
}
func (s *startingStopServer) ParamSpecs() []ProfileParamSpec { return nil }
func (s *startingStopServer) StartingUp(addr string) bool    { return s.starting(addr) }

// TestStopServerAt_StartingOccupant pins the ADR-0010 stop-verification
// rule: stopped means not healthy *and* not still starting up. A Starting
// server fails its health check for the whole model load, so health alone
// would misreport a survived Starting server as stopped. Not parallel: the
// subtests mutate the global llmServers registry.
func TestStopServerAt_StartingOccupant(t *testing.T) {
	t.Run("signalling the listener stops a Starting server and reports its PID", func(t *testing.T) {
		if _, err := exec.LookPath("nc"); err != nil {
			t.Skip("nc not available")
		}

		// A real child process listening at the target address stands in for
		// the loading llama-server: the PID path (lsof + SIGTERM) is what the
		// stop relies on while the server cannot answer its stop hook. The
		// stub's startup probe dials the address, so it flips false exactly
		// when the signalled listener is gone.
		addr := deadAddr(t)
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("splitting %q: %v", addr, err)
		}
		cmd := exec.Command("nc", "-l", host, port)
		cmd.SysProcAttr = detachedSysProcAttr()
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting nc: %v", err)
		}
		// Reap the child as soon as it exits: a zombie still counts as alive
		// for IsProcessAlive, which would stall terminatePID's wait loop.
		go cmd.Wait()
		t.Cleanup(func() { cmd.Process.Kill() })

		deadline := time.Now().Add(5 * time.Second)
		for {
			if pid, err := findListeningPID(addr); err == nil && pid == cmd.Process.Pid {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("nc (PID %d) never showed up listening on %s", cmd.Process.Pid, addr)
			}
			time.Sleep(50 * time.Millisecond)
		}

		stub := &startingStopServer{
			name: "startingstop",
			starting: func(addr string) bool {
				conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
				if err != nil {
					return false
				}
				conn.Close()
				return true
			},
		}
		RegisterLLMServer(stub)
		t.Cleanup(func() { delete(llmServers, stub.name) })

		pid, err := stopServerAt(stub.name, addr, true, nil)

		if err != nil {
			t.Fatalf("stopServerAt = %v, want success once the listener is gone", err)
		}
		if pid != cmd.Process.Pid {
			t.Errorf("reported PID = %d, want the listener's PID %d", pid, cmd.Process.Pid)
		}
		if IsProcessAlive(cmd.Process.Pid) {
			t.Errorf("PID %d still alive after stopServerAt", cmd.Process.Pid)
		}
	})

	t.Run("a survived still-starting server yields the still-reachable error", func(t *testing.T) {
		stub := &startingStopServer{
			name:     "survivestart",
			starting: func(string) bool { return true },
		}
		RegisterLLMServer(stub)
		t.Cleanup(func() { delete(llmServers, stub.name) })

		pid, err := stopServerAt(stub.name, deadAddr(t), true, nil)

		if err == nil || !strings.Contains(err.Error(), "still reachable") {
			t.Fatalf("stopServerAt = %v, want the still-reachable error for a survived Starting server", err)
		}
		if pid != 0 {
			t.Errorf("reported PID = %d, want 0 when nothing was listening", pid)
		}
		if len(stub.tryStops) != 1 {
			t.Errorf("TryStop calls = %v, want exactly one before the verification", stub.tryStops)
		}
	})
}

// TestUnloadInstanceModel_AuthFailed: a server at a configured address
// answering only 401 has no readable model list, so unloading it is refused
// with the auth error instead of reporting "nothing loaded" as success; at
// an address no config names it is ErrNotRunning. Not parallel: it pins the
// process-global configured-address snapshot.
func TestUnloadInstanceModel_AuthFailed(t *testing.T) {
	host, port := authRefusingServer(t, http.StatusUnauthorized)
	addr := fmt.Sprintf("%s:%d", host, port)

	t.Run("configured address is refused with the auth error", func(t *testing.T) {
		pinConfiguredTargets(t, startingCfg(t, "llamacpp", addr))

		inst, err := UnloadInstanceModel(addr, nil)

		if !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("err = %v, want ErrAuthFailed", err)
		}
		if inst != nil {
			t.Errorf("instance = %+v, want nil on a refusal", inst)
		}
	})

	t.Run("unconfigured address is ErrNotRunning", func(t *testing.T) {
		pinConfiguredTargets(t, &Config{})

		inst, err := UnloadInstanceModel(addr, nil)

		if !errors.Is(err, ErrNotRunning) || errors.Is(err, ErrAuthFailed) {
			t.Fatalf("err = %v, want ErrNotRunning and no auth error", err)
		}
		if inst != nil {
			t.Errorf("instance = %+v, want nil", inst)
		}
	})
}

// listingUnloadServer is a registry stub that reports one loaded model and
// records the model id each UnloadModel call receives.
type listingUnloadServer struct {
	hookStopServer
	model    string
	unloaded []string
}

func (s *listingUnloadServer) ListRunningModels(string) ([]RunningModelInfo, error) {
	return []RunningModelInfo{{Name: s.model}}, nil
}

func (s *listingUnloadServer) UnloadModel(_ string, modelID string) error {
	s.unloaded = append(s.unloaded, modelID)
	return nil
}

// TestUnloadInstanceModel_LongModelIDReachesServerWhole: the id handed back
// to the server is never the bounded display copy — an id over
// maxModelIDBytes reaches UnloadModel whole. Not parallel: it mutates the
// global llmServers registry.
func TestUnloadInstanceModel_LongModelIDReachesServerWhole(t *testing.T) {
	longID := strings.Repeat("m", 2*maxModelIDBytes) + ".gguf"
	stub := &listingUnloadServer{hookStopServer: hookStopServer{name: "listunload"}, model: longID}
	RegisterLLMServer(stub)
	t.Cleanup(func() { delete(llmServers, stub.name) })

	if _, err := UnloadInstanceModel(deadAddr(t), nil); err != nil {
		t.Fatalf("UnloadInstanceModel = %v, want success", err)
	}

	if len(stub.unloaded) != 1 || stub.unloaded[0] != longID {
		t.Errorf("UnloadModel received %d ids (first %d bytes), want the whole %d-byte id once",
			len(stub.unloaded), firstLen(stub.unloaded), len(longID))
	}
}

// firstLen returns the byte length of ids[0], or 0 when ids is empty.
func firstLen(ids []string) int {
	if len(ids) == 0 {
		return 0
	}
	return len(ids[0])
}

// authRefusingStopServer is a registry stub for a server that refuses the
// api_key: its health check answers ErrAuthFailed while refuses reports the
// server there, and a connection error otherwise. TryStop is recorded.
type authRefusingStopServer struct {
	hookStopServer
	refuses func(addr string) bool
}

func (s *authRefusingStopServer) HealthCheck(addr string) error {
	if !s.refuses(addr) {
		return errors.New("connection refused")
	}
	return authFailedErr(http.StatusUnauthorized)
}

// acceptsConnection reports whether anything accepts a TCP connection at
// addr.
func acceptsConnection(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// TestStopServerAt_AuthFailedListener pins the stop of a server no backend
// identified because it answers 401/403: the listening PID is signalled,
// the native hook never runs, and a 401/403 answer after the signal counts
// as still reachable. Not parallel: the subtests mutate the global
// llmServers registry.
func TestStopServerAt_AuthFailedListener(t *testing.T) {
	t.Run("the listener is signalled and no native hook runs", func(t *testing.T) {
		if _, err := exec.LookPath("nc"); err != nil {
			t.Skip("nc not available")
		}
		addr := deadAddr(t)
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("splitting %q: %v", addr, err)
		}
		cmd := exec.Command("nc", "-l", host, port)
		cmd.SysProcAttr = detachedSysProcAttr()
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting nc: %v", err)
		}
		go cmd.Wait()
		t.Cleanup(func() { cmd.Process.Kill() })
		deadline := time.Now().Add(5 * time.Second)
		for {
			if pid, err := findListeningPID(addr); err == nil && pid == cmd.Process.Pid {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("nc (PID %d) never showed up listening on %s", cmd.Process.Pid, addr)
			}
			time.Sleep(50 * time.Millisecond)
		}
		stub := &authRefusingStopServer{
			hookStopServer: hookStopServer{name: "authstop"},
			refuses:        acceptsConnection,
		}
		RegisterLLMServer(stub)
		t.Cleanup(func() { delete(llmServers, stub.name) })

		pid, err := stopServerAt(stub.name, addr, false, nil)

		if err != nil {
			t.Fatalf("stopServerAt = %v, want success once the listener is gone", err)
		}
		if pid != cmd.Process.Pid {
			t.Errorf("reported PID = %d, want the listener's PID %d", pid, cmd.Process.Pid)
		}
		if IsProcessAlive(cmd.Process.Pid) {
			t.Errorf("PID %d still alive after stopServerAt", cmd.Process.Pid)
		}
		if len(stub.tryStops) != 0 {
			t.Errorf("TryStop calls = %v, want none for an auth-refusing server", stub.tryStops)
		}
	})

	t.Run("a surviving 401 answer is not reported stopped", func(t *testing.T) {
		// A dead address, so nothing is signalled, while the stub keeps
		// answering 401 — a server that survived the stop.
		stub := &authRefusingStopServer{
			hookStopServer: hookStopServer{name: "authsurvive"},
			refuses:        func(string) bool { return true },
		}
		RegisterLLMServer(stub)
		t.Cleanup(func() { delete(llmServers, stub.name) })

		_, err := stopServerAt(stub.name, deadAddr(t), false, nil)

		if err == nil || !strings.Contains(err.Error(), "still reachable") {
			t.Fatalf("stopServerAt = %v, want the still-reachable error", err)
		}
		if len(stub.tryStops) != 0 {
			t.Errorf("TryStop calls = %v, want none for an auth-refusing server", stub.tryStops)
		}
	})
}

// TestStartServer_BinaryNotFound: a managed backend whose server binary is
// not on PATH fails fast with a clear error before anything is spawned.
// Not parallel: it rewrites PATH.
func TestStartServer_BinaryNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no llama-server anywhere on PATH

	// A provably-closed port: the StartingUp probe runs before the binary
	// lookup, so a hardcoded port with a live listener (answering 503) would
	// divert the test into the double-spawn refusal.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a loopback port: %v", err)
	}
	host := "127.0.0.1"
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	cfg := &Config{
		LogDir:  t.TempDir(),
		Servers: map[string]ServerConfig{"llamacpp": {Enabled: true}},
	}
	profile := &ResolvedProfile{
		Name:          "x",
		Backend:       "llamacpp",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	_, err = StartServer(cfg, profile)
	if err == nil || !strings.Contains(err.Error(), "server binary not found") {
		t.Fatalf("StartServer = %v, want a 'server binary not found' error", err)
	}
}

// TestStartManagedServerBinaryInstallHint: a managed backend implementing
// binaryInstallHinter gets its setup hint appended to the "server binary not
// found" error, while a backend without one keeps the bare message.
// Not parallel: it rewrites PATH.
func TestStartManagedServerBinaryInstallHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no server binary anywhere on PATH

	// A provably-closed port, so the StartingUp probe and the port-occupant
	// check both pass and the start reaches the binary lookup.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a loopback port: %v", err)
	}
	host := "127.0.0.1"
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	cfg := &Config{LogDir: t.TempDir()}
	profile := &ResolvedProfile{
		Name:          "x",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	t.Run("hinter appends its hint", func(t *testing.T) {
		splash := &Splash{}
		_, err := startManagedServer(cfg, profile, splash)
		if err == nil {
			t.Fatal("startManagedServer succeeded, want a 'server binary not found' error")
		}
		want := "server binary not found: splash — " + splash.BinaryInstallHint()
		if err.Error() != want {
			t.Errorf("err = %q, want %q", err, want)
		}
	})

	t.Run("llamacpp message is unchanged", func(t *testing.T) {
		_, err := startManagedServer(cfg, profile, &LlamaCpp{})
		if err == nil || err.Error() != "server binary not found: llama-server" {
			t.Errorf("err = %v, want exactly 'server binary not found: llama-server'", err)
		}
	})
}

// TestLoadProfile_Orchestration_WaitTimeout pins the managed health-wait
// failure: when the started server never turns healthy, loadProfile reports
// started=false and the error carries the recovery guidance — the spawned
// server's PID, its log path, and the `llama-launcher stop` escape hatch
// (ADR-0010) — because the server is deliberately left running (a large
// model may still be loading).
func TestLoadProfile_Orchestration_WaitTimeout(t *testing.T) {
	t.Parallel()

	off := false
	profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", "127.0.0.1", 8080)
	f := &fakeOps{
		healthyAddrs: map[string]bool{},
		waitErr:      errors.New("did not become healthy"),
	}
	cfg := &Config{AutoStopServer: &off, AutoUnload: &off}

	_, started, err := loadProfile(f, cfg, profile, false, nil, nil)

	if err == nil {
		t.Fatal("loadProfile = nil error, want the health-wait timeout surfaced")
	}
	for _, want := range []string{"did not become healthy", "PID 4242", "/logs/fake.log", "llama-launcher stop llamacpp"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
	if started {
		t.Error("started = true, want false on a health-wait timeout")
	}
	if len(f.loadedModels) != 0 {
		t.Errorf("loadedModels = %v, want none after a failed wait", f.loadedModels)
	}
}

// TestLoadProfile_DriftNoticeContent pins the drift notice's actionable
// content (ADR-0007: the notice is the user's cue to act) together with the
// CLI's delivery of it: LoadProfile — the entry point the CLI calls — binds
// the stderr printer itself, so the notice must reach stderr naming the
// profile, listing the drifted field, and pointing at `load --restart`. It
// runs against a llama-server stand-in rather than the fake ops seam so the
// exported entry point is exercised end to end; the stand-in is healthy and
// serving the profile's model, which takes the ADR-0007 idempotent path and
// forks nothing. Not parallel: captureStderr redirects the process-global
// os.Stderr.
func TestLoadProfile_DriftNoticeContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"/models/test-7b.gguf"}]}`))
		case "/props":
			// One slot of 4096 against the profile's 8192 below: the
			// context size is the only parameter that drifts.
			w.Write([]byte(`{"total_slots":1,"default_generation_settings":{"n_ctx":4096}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	host, port, ok := splitHostPort(addrFromURL(t, srv.URL))
	if !ok {
		t.Fatalf("unusable stand-in address %q", srv.URL)
	}
	contextSize, slots := 8192, 1
	profile := orchProfile("llamacpp", "chat", "/models/test-7b.gguf", host, port)
	profile.ContextSize = &contextSize
	profile.Parallel = &slots

	errOut := captureStderr(t, func() {
		if _, _, err := LoadProfile(&Config{}, profile, false, nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	for _, want := range []string{`profile "chat"`, "context_size: 4096 → 8192", "load chat --restart"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("drift notice = %q, want it to contain %q", errOut, want)
		}
	}
}

func TestParseListeningPIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  string
		want []int
	}{
		{"empty", "", nil},
		{"whitespace only", "  \n\n ", nil},
		{"single pid", "15481\n", []int{15481}},
		{
			// lsof -t prints one line per matching socket, so a process
			// listening on several interfaces repeats.
			name: "repeats collapse, order preserved",
			out:  "15481\n46578\n15481\n",
			want: []int{15481, 46578},
		},
		{"unparseable and non-positive lines are dropped", "abc\n0\n-3\n99\n", []int{99}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseListeningPIDs(tc.out); !slices.Equal(got, tc.want) {
				t.Errorf("parseListeningPIDs(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

func TestPortConflictErr(t *testing.T) {
	t.Parallel()

	b := &LlamaCpp{}
	err := portConflictErr(b, "0.0.0.0:1111", []portOccupant{
		{PID: 15481, Name: "llama-server"},
		{PID: 46578, Name: "Code Helper (Plugin)"},
		{PID: 777},
	})

	// The port, the address and every occupant have to be on screen: the
	// whole point of the refusal is that the user never has to run lsof.
	for _, want := range []string{
		"port 1111 is already in use",
		"LLaMA.cpp cannot bind 0.0.0.0:1111",
		"PID 15481 (llama-server)",
		"PID 46578 (Code Helper (Plugin))",
		"different `port`",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q missing %q", err, want)
		}
	}

	// An occupant ps could not name still gets a line, without empty brackets.
	if !strings.Contains(err.Error(), "PID 777") || strings.Contains(err.Error(), "PID 777 ()") {
		t.Errorf("refusal %q should name PID 777 bare, with no empty parentheses", err)
	}
}

func TestStartManagedServer_RefusesWhenPortOccupied(t *testing.T) {
	// Not parallel: rewrites PATH.

	// 200 on every path, so the StartingUp probe reads "not one of ours
	// coming up" and the port-conflict check is what fires.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	addr := addrFromURL(t, srv.URL)
	if pid, err := findListeningPID(addr); err != nil || pid != os.Getpid() {
		t.Skipf("lsof cannot see this test's own listener (pid=%d err=%v)", pid, err)
	}

	host, port := hostPort(t, srv.URL)
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	profile := &ResolvedProfile{
		Name:          "test",
		ModelPath:     "/models/test.gguf",
		Backend:       "llamacpp",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	// A PATH holding only the tools the check itself shells out to: a
	// regression that spawns anyway fails at the binary lookup with a
	// distinguishable error instead of forking a real llama-server.
	t.Setenv("PATH", toolOnlyPATH(t, "lsof", "ps"))

	_, err := StartServer(cfg, profile)
	if err == nil {
		t.Fatal("StartServer succeeded onto an occupied port, want a refusal")
	}
	if strings.Contains(err.Error(), "server binary not found") {
		t.Fatalf("refusal must fire before the spawn attempt, got %v", err)
	}
	for _, want := range []string{
		"is already in use",
		fmt.Sprintf("PID %d", os.Getpid()),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q missing %q", err, want)
		}
	}
}

// toolOnlyPATH builds a directory holding just the named tools, symlinked to
// wherever they resolve now, and returns it for use as PATH. It lets a test
// deny the server binary while leaving the helpers the code under test shells
// out to reachable.
func toolOnlyPATH(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range tools {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not on PATH: %v", tool, err)
		}
		if err := os.Symlink(path, filepath.Join(dir, tool)); err != nil {
			t.Fatalf("linking %s: %v", tool, err)
		}
	}
	return dir
}

// pinLoadingSplash replaces the process table with one holding a single
// launcher-forked Splash (a session leader) loading for addr, and reports
// nothing listening anywhere — the window in which Splash has not bound its
// address yet (ADR-0015). alive decides whether the entry is still listed,
// so a test can make it vanish once the process is gone. Not for parallel
// tests: it rewrites package seams.
func pinLoadingSplash(t *testing.T, pid int, addr string, alive func() bool) {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("splitting %q: %v", addr, err)
	}
	args := strings.Fields("/bin/sh /Users/u/.local/bin/splash serve --model o/r --host " + host + " --port " + port)
	prevTable, prevListener := processTable, addrHasListener
	processTable = func() ([]processEntry, error) {
		if !alive() {
			return nil, nil
		}
		return []processEntry{{PID: pid, PGID: pid, Args: args}}, nil
	}
	addrHasListener = func(string) bool { return false }
	t.Cleanup(func() { processTable, addrHasListener = prevTable, prevListener })
}

// TestStartingUp_LoadingSplash covers ADR-0015: a Splash that is loading
// but has not bound its address is Starting, found by its process; the
// same process is ignored once anything listens at the address, and a
// backend without a LoadingProcessFinder never consults the table.
func TestStartingUp_LoadingSplash(t *testing.T) {
	addr := deadAddr(t)
	pinLoadingSplash(t, 4242, addr, func() bool { return true })
	splash, err := GetLLMServer("splash")
	if err != nil {
		t.Fatal(err)
	}
	llamacpp, err := GetLLMServer("llamacpp")
	if err != nil {
		t.Fatal(err)
	}

	if !startingUp(splash, addr) {
		t.Error("startingUp(splash) = false, want true for a loading Splash process")
	}
	if got := loadingPID(splash, addr); got != 4242 {
		t.Errorf("loadingPID(splash) = %d, want 4242", got)
	}
	if startingUp(llamacpp, addr) {
		t.Error("startingUp(llamacpp) = true, want false: llamacpp has no LoadingProcessFinder")
	}

	addrHasListener = func(string) bool { return true }
	if startingUp(splash, addr) {
		t.Error("startingUp(splash) = true with a listener at the address, want false")
	}
}

func TestDiscoverRunningInstances_ReportsLoadingSplash(t *testing.T) {
	addr := deadAddr(t)
	pinLoadingSplash(t, 4242, addr, func() bool { return true })
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	cfg := &Config{
		Servers:  map[string]ServerConfig{"splash": {Enabled: true}},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	cfg.Defaults = ProfileParams{Server: strPtrLocal("splash"), Host: &host, Port: &port}

	instances := DiscoverRunningInstances(cfg)

	if len(instances) != 1 {
		t.Fatalf("expected 1 Starting instance, got %d: %+v", len(instances), instances)
	}
	inst := instances[0]
	if inst.Backend != "splash" || !inst.Starting || inst.Addr() != addr {
		t.Errorf("instance = %+v, want a Starting splash at %s", inst, addr)
	}
	fillRuntimeDetails(cfg, inst)
	if inst.PID != 4242 {
		t.Errorf("PID = %d, want 4242 from the process table", inst.PID)
	}
}

func TestStartManagedServer_RefusesLoadingSplash(t *testing.T) {
	// Not parallel: rewrites PATH and package seams.
	addr := deadAddr(t)
	pinLoadingSplash(t, 4242, addr, func() bool { return true })
	t.Setenv("PATH", t.TempDir())
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	b, err := GetLLMServer("splash")
	if err != nil {
		t.Fatal(err)
	}
	profile := &ResolvedProfile{
		Name:          "splash",
		ModelPath:     "o/r",
		Backend:       "splash",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	_, err = startManagedServer(&Config{LogDir: t.TempDir()}, profile, b.(ManagedLLMServer))

	if err == nil || !strings.Contains(err.Error(), "still starting up") {
		t.Fatalf("err = %v, want the still-starting-up refusal", err)
	}
	if !strings.Contains(err.Error(), "PID 4242") {
		t.Errorf("err = %v, want it to name the loading PID 4242", err)
	}
}

// TestStopInstance_LoadingSplash covers ADR-0015's stop: a loading Splash
// holds no address, so identification and the PID both come from the
// process table, and the stop signals that process like any other.
func TestStopInstance_LoadingSplash(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a detached child here: %v", err)
	}
	// Reap the child as soon as it exits: a zombie still counts as alive.
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })
	pid := cmd.Process.Pid

	addr := deadAddr(t)
	pinLoadingSplash(t, pid, addr, func() bool { return IsProcessAlive(pid) })

	backend, err := identifyBackend(addr)
	if err != nil || backend != "splash" {
		t.Fatalf("identifyBackend = %q, %v; want splash", backend, err)
	}

	inst, err := StopInstance(addr, nil)

	if err != nil {
		t.Fatalf("StopInstance = %v, want success", err)
	}
	if inst.Backend != "splash" || inst.PID != pid {
		t.Errorf("stopped %+v, want splash with PID %d", inst, pid)
	}
	if IsProcessAlive(pid) {
		t.Errorf("PID %d still alive after the stop", pid)
	}
}

// TestCreateLogPath_SameSecondStartsGetDistinctFiles pins that back-to-back
// starts of one backend — well inside a single second — each get their own
// freshly created log file carrying a millisecond stamp.
func TestCreateLogPath_SameSecondStartsGetDistinctFiles(t *testing.T) {
	t.Parallel()
	cfg := &Config{LogDir: t.TempDir()}

	first, err := createLogPath(cfg, "llamacpp")
	if err != nil {
		t.Fatalf("first createLogPath: %v", err)
	}
	defer first.Close()
	second, err := createLogPath(cfg, "llamacpp")
	if err != nil {
		t.Fatalf("second createLogPath: %v", err)
	}
	defer second.Close()

	if first.Name() == second.Name() {
		t.Fatalf("both starts got %q, want distinct log files", first.Name())
	}
	for _, f := range []*os.File{first, second} {
		if _, err := os.Stat(f.Name()); err != nil {
			t.Errorf("log file %q does not exist: %v", f.Name(), err)
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f.Name()), "llamacpp-"), ".log")
		if _, err := time.Parse(logTimestampFormat, stamp); err != nil {
			t.Errorf("log name %q does not carry a millisecond stamp: %v", f.Name(), err)
		}
	}
	if second.Name() < first.Name() {
		t.Errorf("later log %q sorts before earlier %q", second.Name(), first.Name())
	}
}

// TestCreateLogPath_CollisionRetriesWithoutTruncating pins that a stamp whose
// name already exists is retried with a fresh stamp, and that the existing
// log is never truncated.
func TestCreateLogPath_CollisionRetriesWithoutTruncating(t *testing.T) {
	t.Parallel()
	cfg := &Config{LogDir: t.TempDir()}

	// Occupy the names of the next 20 ms of stamps so the first attempts
	// collide and createLogPath must retry.
	const occupied = 20
	base := time.Now()
	taken := make(map[string]bool, occupied)
	for i := range occupied {
		stamp := base.Add(time.Duration(i) * time.Millisecond).Format(logTimestampFormat)
		path := filepath.Join(cfg.LogDir, "ollama-"+stamp+".log")
		if err := os.WriteFile(path, []byte("live server output"), 0o600); err != nil {
			t.Fatal(err)
		}
		taken[path] = true
	}

	logFile, err := createLogPath(cfg, "ollama")
	if err != nil {
		t.Fatalf("createLogPath: %v", err)
	}
	defer logFile.Close()

	if taken[logFile.Name()] {
		t.Errorf("createLogPath reused existing log %q", logFile.Name())
	}
	for path := range taken {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %q: %v", path, err)
		}
		if string(data) != "live server output" {
			t.Errorf("existing log %q was truncated to %q", path, data)
		}
	}
}

// writeStateFixture writes content to name inside dir and returns its path.
func writeStateFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestCleanupLegacyStateFiles(t *testing.T) {
	const legacyOllama = `{"pid":4242,"backend":"ollama","host":"127.0.0.1","port":11434,"started_at":"2026-05-01T10:00:00Z"}`
	removed := map[string]string{
		"state-llamacpp.json":      `{"pid":1234,"backend":"llamacpp","host":"127.0.0.1","port":8080,"started_at":"2026-05-01T10:00:00Z","log_file":"/tmp/x.log"}`,
		"state-ollama-11434.json":  legacyOllama,
		"state-lmstudio-1234.json": `{"pid":0,"backend":"lmstudio","host":"127.0.0.1","port":1234,"started_at":"2026-05-01T10:00:00Z"}`,
		legacySharedStateFileName:  `{"pid":99,"backend":"llamacpp","host":"127.0.0.1","port":8080,"started_at":"2026-05-01T10:00:00Z"}`,
	}
	kept := map[string]string{
		"state-notes.json":              legacyOllama,
		"state-ollama-backup-2024.json": `{"note":"my ollama backup","models":["llama3"]}`,
		"state-llamacpp-broken.json":    `{"pid":1234,"backend":"llamacpp",`,
		"state-ollama-mismatch.json":    `{"pid":1,"backend":"llamacpp","port":8080}`,
		"state-ollama-nopid.json":       `{"backend":"ollama","port":11434}`,
		"state-ollama-negpid.json":      `{"pid":-1,"backend":"ollama","port":11434}`,
		"state-ollama-noport.json":      `{"pid":1,"backend":"ollama","port":0}`,
		"state-ollama-strpid.json":      `{"pid":"1","backend":"ollama","port":11434}`,
		"state-ollama-array.json":       `[{"pid":1,"backend":"ollama","port":11434}]`,
	}

	t.Run("launcher-written files go, everything else stays", func(t *testing.T) {
		dir := t.TempDir()
		for name, content := range removed {
			writeStateFixture(t, dir, name, content)
		}
		for name, content := range kept {
			writeStateFixture(t, dir, name, content)
		}

		cleanupLegacyStateFiles(dir)

		for name := range removed {
			if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Errorf("%s: want removed, stat err = %v", name, err)
			}
		}
		for name := range kept {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Errorf("%s: want kept, stat err = %v", name, err)
			}
		}
	})

	t.Run("a non-state state.json stays", func(t *testing.T) {
		dir := t.TempDir()
		path := writeStateFixture(t, dir, legacySharedStateFileName, `{"theme":"dark","port":8080,"pid":1}`)

		cleanupLegacyStateFiles(dir)

		if _, err := os.Stat(path); err != nil {
			t.Errorf("state.json without a known backend: want kept, stat err = %v", err)
		}
	})
}

func TestIsLegacyStateFile(t *testing.T) {
	const legacyContent = `{"pid":7,"backend":"llamacpp","host":"127.0.0.1","port":8080}`

	t.Run("oversized file", func(t *testing.T) {
		dir := t.TempDir()
		padding := strings.Repeat(" ", legacyStateFileMaxBytes)
		path := writeStateFixture(t, dir, "state-llamacpp.json", legacyContent+padding)
		if isLegacyStateFile(path) {
			t.Error("a file over legacyStateFileMaxBytes must not be judged a legacy state file")
		}
	})

	t.Run("symlink to a legacy file", func(t *testing.T) {
		dir := t.TempDir()
		target := writeStateFixture(t, dir, "elsewhere.json", legacyContent)
		link := filepath.Join(dir, "state-llamacpp.json")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		if isLegacyStateFile(link) {
			t.Error("a symlink must not be judged a legacy state file")
		}
	})

	t.Run("directory with a legacy name", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state-llamacpp.json")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if isLegacyStateFile(path) {
			t.Error("a directory must not be judged a legacy state file")
		}
	})

	t.Run("legacy file", func(t *testing.T) {
		dir := t.TempDir()
		path := writeStateFixture(t, dir, "state-llamacpp.json", legacyContent)
		if !isLegacyStateFile(path) {
			t.Error("a launcher-written state file must be judged legacy")
		}
	})
}
