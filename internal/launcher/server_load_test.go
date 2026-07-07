package launcher

import (
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLifecycleBackend is an in-package LLMServer used to exercise LoadProfile's
// orchestration and the address-scoped stop lifecycle without forking or
// signalling any real process. Reachability, the reported model, and the live
// parameters are configured per test; TryStart flips an address reachable and
// TryStop flips it unreachable, mirroring a real start/stop. Every field access
// is mutex-guarded so the parallel probes DiscoverRunningInstances issues (and
// identifyBackend's registry sweep) cannot race the test goroutine.
type fakeLifecycleBackend struct {
	name string

	mu          sync.Mutex
	reachable   map[string]bool
	models      map[string]string
	params      map[string]*ProfileParams
	startErr    error
	loadCalls   []string
	unloadCalls []string
	stopCalls   []string
	startCalls  []string
}

func newFakeLifecycleBackend(name string) *fakeLifecycleBackend {
	f := &fakeLifecycleBackend{name: name}
	f.reset()
	return f
}

func (f *fakeLifecycleBackend) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reachable = map[string]bool{}
	f.models = map[string]string{}
	f.params = map[string]*ProfileParams{}
	f.startErr = nil
	f.loadCalls = nil
	f.unloadCalls = nil
	f.stopCalls = nil
	f.startCalls = nil
}

// setReachable marks addr healthy and, optionally, records the model it serves
// and the parameters it reports live.
func (f *fakeLifecycleBackend) setReachable(addr, model string, params *ProfileParams) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reachable[addr] = true
	if model != "" {
		f.models[addr] = model
	}
	if params != nil {
		f.params[addr] = params
	}
}

func (f *fakeLifecycleBackend) failStart(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startErr = err
}

func (f *fakeLifecycleBackend) Name() string        { return f.name }
func (f *fakeLifecycleBackend) DisplayName() string { return f.name }
func (f *fakeLifecycleBackend) DefaultAddr() string { return "" }

func (f *fakeLifecycleBackend) HealthCheck(addr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reachable[addr] {
		return nil
	}
	return errors.New("unreachable")
}

func (f *fakeLifecycleBackend) ResolveModel(*Config, string) (string, error) { return "", nil }

func (f *fakeLifecycleBackend) LoadModel(addr string, _ *ResolvedProfile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadCalls = append(f.loadCalls, addr)
	return nil
}

func (f *fakeLifecycleBackend) UnloadModel(addr string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unloadCalls = append(f.unloadCalls, addr)
	return nil
}

func (f *fakeLifecycleBackend) TryStart(_ *Config, addr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls = append(f.startCalls, addr)
	if f.startErr != nil {
		return f.startErr
	}
	f.reachable[addr] = true
	return nil
}

func (f *fakeLifecycleBackend) TryStop(addr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls = append(f.stopCalls, addr)
	f.reachable[addr] = false
	return nil
}

func (f *fakeLifecycleBackend) ListRunningModels(addr string) ([]RunningModelInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.models[addr]; m != "" {
		return []RunningModelInfo{{Name: m}}, nil
	}
	return nil, nil
}

func (f *fakeLifecycleBackend) QueryLiveParams(addr string) (*ProfileParams, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.params[addr], nil
}

func (f *fakeLifecycleBackend) stops() []string  { return f.snapshot(f.stopCalls) }
func (f *fakeLifecycleBackend) loads() []string  { return f.snapshot(f.loadCalls) }
func (f *fakeLifecycleBackend) starts() []string { return f.snapshot(f.startCalls) }

func (f *fakeLifecycleBackend) snapshot(field []string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), field...)
}

var (
	fakeLCTarget = newFakeLifecycleBackend("fakelc-target")
	fakeLCOther  = newFakeLifecycleBackend("fakelc-other")
)

func init() {
	RegisterLLMServer(fakeLCTarget)
	RegisterLLMServer(fakeLCOther)
}

func resetFakes() {
	fakeLCTarget.reset()
	fakeLCOther.reset()
}

func ptrBool(v bool) *bool { return &v }

// freeLoopbackAddr reserves a loopback port, then releases it so nothing is
// listening there. The address is syntactically valid (so splitHostPort and
// StopInstance accept it) yet findListeningPID's `lsof -sTCP:LISTEN` scan finds
// no process — so the stop path never signals a real PID. (An ephemeral port
// reused for an outbound socket would be in ESTABLISHED state, which the LISTEN
// filter excludes.)
func freeLoopbackAddr(t *testing.T) (addr, host string, port int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a free loopback port: %v", err)
	}
	tcp := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	return tcp.String(), "127.0.0.1", tcp.Port
}

// TestLoadProfile_IdempotentNoOp: a healthy target already serving the profile's
// model, with live params matching the freshly resolved profile, is a no-op —
// LoadProfile returns started==false and issues no stop or load. A field the
// backend does not report (gpu_layers here) must NOT be counted as drift, so no
// notice is printed (item 2's fix; pre-fix this printed a bogus drift notice).
func TestLoadProfile_IdempotentNoOp(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)

	addr, host, port := freeLoopbackAddr(t)
	// /props reports only context_size (the subset a real llama-server exposes).
	fakeLCTarget.setReachable(addr, "/models/x.gguf", &ProfileParams{ContextSize: ptrInt(8192)})

	profile := &ResolvedProfile{
		Name:      "idem",
		Backend:   fakeLCTarget.name,
		ModelPath: "/models/x.gguf",
		ProfileParams: ProfileParams{
			Host:        &host,
			Port:        &port,
			ContextSize: ptrInt(8192),
			GPULayers:   ptrInt(99), // set in the profile, never reported by the backend
		},
	}
	cfg := &Config{
		LogDir:  t.TempDir(),
		Servers: map[string]ServerConfig{fakeLCTarget.name: {Enabled: true}},
	}

	var inst *RunningInstance
	var started bool
	var err error
	out := captureStderr(func() {
		inst, started, err = LoadProfile(cfg, profile, false, nil)
	})

	if err != nil {
		t.Fatalf("LoadProfile error: %v", err)
	}
	if started {
		t.Errorf("started = true, want false for an idempotent no-op")
	}
	if inst == nil || inst.ActiveModel != "/models/x.gguf" {
		t.Errorf("inst = %+v, want ActiveModel /models/x.gguf", inst)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("stderr = %q, want no drift notice (a field the backend does not report is not drift)", out)
	}
	if s := fakeLCTarget.stops(); len(s) != 0 {
		t.Errorf("stop calls = %v, want none on an idempotent no-op", s)
	}
	if l := fakeLCTarget.loads(); len(l) != 0 {
		t.Errorf("load calls = %v, want none on an idempotent no-op", l)
	}
}

// TestLoadProfile_DriftNoOp: a shared, reported field that differs between the
// live server and the resolved profile prints a drift notice naming that field,
// but the activation still no-ops (started==false) without --restart.
func TestLoadProfile_DriftNoOp(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)

	addr, host, port := freeLoopbackAddr(t)
	fakeLCTarget.setReachable(addr, "/models/x.gguf", &ProfileParams{ContextSize: ptrInt(4096)})

	profile := &ResolvedProfile{
		Name:      "drift",
		Backend:   fakeLCTarget.name,
		ModelPath: "/models/x.gguf",
		ProfileParams: ProfileParams{
			Host:        &host,
			Port:        &port,
			ContextSize: ptrInt(8192),
		},
	}
	cfg := &Config{
		LogDir:  t.TempDir(),
		Servers: map[string]ServerConfig{fakeLCTarget.name: {Enabled: true}},
	}

	var started bool
	var err error
	out := captureStderr(func() {
		_, started, err = LoadProfile(cfg, profile, false, nil)
	})

	if err != nil {
		t.Fatalf("LoadProfile error: %v", err)
	}
	if started {
		t.Errorf("started = true, want false — drift must still no-op without --restart")
	}
	for _, want := range []string{"context_size", "4096", "8192", "--restart", "drift"} {
		if !strings.Contains(out, want) {
			t.Errorf("drift notice %q missing %q", out, want)
		}
	}
	if s := fakeLCTarget.stops(); len(s) != 0 {
		t.Errorf("stop calls = %v, want none — a drift notice must not stop the server", s)
	}
	if l := fakeLCTarget.loads(); len(l) != 0 {
		t.Errorf("load calls = %v, want none — a drift notice must not reload", l)
	}
}

// TestLoadProfile_AutoStopServer_StopsOtherAddress: with auto_stop_server the
// launcher stops every discovered instance except the one it is activating. A
// second instance of the same backend at a different address receives the stop
// path; the target address is left alone.
func TestLoadProfile_AutoStopServer_StopsOtherAddress(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)

	targetAddr, tHost, tPort := freeLoopbackAddr(t)
	otherAddr, _, oPort := freeLoopbackAddr(t)

	// Target is healthy but serving a different model, so the idempotency no-op
	// is skipped and the auto-stop loop runs.
	fakeLCTarget.setReachable(targetAddr, "loaded-other", nil)
	fakeLCTarget.setReachable(otherAddr, "loaded-elsewhere", nil)

	name := fakeLCTarget.name
	oHost := "127.0.0.1"
	cfg := &Config{
		LogDir:         t.TempDir(),
		Servers:        map[string]ServerConfig{fakeLCTarget.name: {Enabled: true}},
		AutoStopServer: ptrBool(true),
		AutoUnload:     ptrBool(false),
		// The backend's configured address is the OTHER instance; the profile
		// overrides to the target address — so discovery finds both.
		Defaults: ProfileParams{Server: &name, Host: &oHost, Port: &oPort},
		Profiles: map[string]Profile{
			"tgt": {ProfileParams: ProfileParams{Host: &tHost, Port: &tPort}},
		},
	}

	profile := &ResolvedProfile{
		Name:          "tgt",
		Backend:       fakeLCTarget.name,
		ModelPath:     "/models/right.gguf",
		ProfileParams: ProfileParams{Host: &tHost, Port: &tPort},
	}

	_, started, err := LoadProfile(cfg, profile, false, nil)
	if err != nil {
		t.Fatalf("LoadProfile error: %v", err)
	}
	if !started {
		t.Errorf("started = false, want true after auto-stop + activation")
	}
	stops := fakeLCTarget.stops()
	if len(stops) != 1 || stops[0] != otherAddr {
		t.Errorf("stop calls = %v, want exactly [%s] (only the other address)", stops, otherAddr)
	}
	for _, s := range stops {
		if s == targetAddr {
			t.Errorf("target address %s was stopped, want it left alone", targetAddr)
		}
	}
}

// TestLoadProfile_AutoStop_StopsDifferentBackendBlocker: a different backend
// squatting on the target address is a blocker, not the target — auto_stop must
// stop it (item 12: match by address AND backend). Pre-fix it matched on address
// alone and skipped the blocker, so it was never stopped.
func TestLoadProfile_AutoStop_StopsDifferentBackendBlocker(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)

	addr, host, port := freeLoopbackAddr(t)
	// A different backend occupies the target address; the target backend is not
	// reachable there yet (only TryStart will bring it up).
	fakeLCOther.setReachable(addr, "blocker-model", nil)

	name := fakeLCTarget.name
	cfg := &Config{
		LogDir: t.TempDir(),
		Servers: map[string]ServerConfig{
			fakeLCTarget.name: {Enabled: true},
			fakeLCOther.name:  {Enabled: true},
		},
		AutoStopServer: ptrBool(true),
		AutoUnload:     ptrBool(false),
		Defaults:       ProfileParams{Server: &name, Host: &host, Port: &port},
		Profiles:       map[string]Profile{},
	}

	profile := &ResolvedProfile{
		Name:          "blk",
		Backend:       fakeLCTarget.name,
		ModelPath:     "/models/new.gguf",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	_, started, err := LoadProfile(cfg, profile, false, nil)
	if err != nil {
		t.Fatalf("LoadProfile error: %v", err)
	}
	if !started {
		t.Errorf("started = false, want true after clearing the blocker and starting")
	}
	if s := fakeLCOther.stops(); len(s) == 0 || s[0] != addr {
		t.Errorf("blocker stop calls = %v, want [%s] — a different backend at the target address must be stopped", s, addr)
	}
	if s := fakeLCTarget.starts(); len(s) == 0 || s[0] != addr {
		t.Errorf("target start calls = %v, want [%s] — the target must start once the blocker is cleared", s, addr)
	}
}

// TestStopInstance_ErrorPaths covers the two guard clauses at the top of the
// address-scoped stop path.
func TestStopInstance_ErrorPaths(t *testing.T) {
	t.Run("unreachable address returns ErrNotRunning", func(t *testing.T) {
		_, err := StopInstance("127.0.0.1:1", nil)
		if !errors.Is(err, ErrNotRunning) {
			t.Errorf("StopInstance = %v, want ErrNotRunning when nothing is reachable", err)
		}
	})

	t.Run("invalid address returns an error", func(t *testing.T) {
		_, err := StopInstance("not-an-address", nil)
		if err == nil || !strings.Contains(err.Error(), "invalid address") {
			t.Errorf("StopInstance = %v, want an invalid-address error", err)
		}
	})
}

// TestEnsureStopped_TryStopFlipsHealthCheck: when the backend's TryStop makes the
// address stop responding, EnsureStopped returns nil without reaching the PID
// path (it never signals a process).
func TestEnsureStopped_TryStopFlipsHealthCheck(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)

	addr, _, _ := freeLoopbackAddr(t)
	fakeLCTarget.setReachable(addr, "", nil) // healthy until TryStop flips it

	if err := EnsureStopped(fakeLCTarget.name, addr, nil); err != nil {
		t.Fatalf("EnsureStopped = %v, want nil once TryStop makes the health check fail", err)
	}
	if s := fakeLCTarget.stops(); len(s) != 1 || s[0] != addr {
		t.Errorf("stop calls = %v, want [%s]", s, addr)
	}
}

// TestWaitForHealth_TimeoutNamesAddress: a server that never becomes healthy
// yields a timeout error that names the address.
func TestWaitForHealth_TimeoutNamesAddress(t *testing.T) {
	t.Parallel()

	err := WaitForHealth(&LlamaCpp{}, "127.0.0.1:1", time.Millisecond)
	if err == nil {
		t.Fatal("WaitForHealth = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") || !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("err = %v, want it to name the address and the timeout", err)
	}
}

// TestLoadProfile_ExternalStartFailure: an unreachable external backend whose
// TryStart fails surfaces a "not reachable" error and does not report a start.
func TestLoadProfile_ExternalStartFailure(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)

	_, host, port := freeLoopbackAddr(t)
	fakeLCTarget.failStart(errors.New("cannot launch")) // not reachable, and TryStart fails

	cfg := &Config{
		LogDir:         t.TempDir(),
		Servers:        map[string]ServerConfig{fakeLCTarget.name: {Enabled: true}},
		AutoStopServer: ptrBool(false),
		AutoUnload:     ptrBool(false),
	}
	profile := &ResolvedProfile{
		Name:          "x",
		Backend:       fakeLCTarget.name,
		ModelPath:     "/models/x.gguf",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	_, started, err := LoadProfile(cfg, profile, false, nil)
	if err == nil {
		t.Fatal("LoadProfile = nil error, want a start-failure error")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("err = %v, want a 'not reachable' start-failure error", err)
	}
	if started {
		t.Errorf("started = true, want false on start failure")
	}
}

// TestStartServer_BinaryNotFound: a managed backend whose server binary is not on
// PATH fails fast with a clear error before anything is spawned.
func TestStartServer_BinaryNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no llama-server anywhere on PATH

	host := "127.0.0.1"
	port := 18080
	cfg := &Config{
		LogDir:  t.TempDir(),
		Servers: map[string]ServerConfig{"llamacpp": {Enabled: true}},
	}
	profile := &ResolvedProfile{
		Name:          "x",
		Backend:       "llamacpp",
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	_, err := StartServer(cfg, profile)
	if err == nil || !strings.Contains(err.Error(), "server binary not found") {
		t.Fatalf("StartServer = %v, want a 'server binary not found' error", err)
	}
}
