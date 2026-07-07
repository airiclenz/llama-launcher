package launcher

import (
	"errors"
	"strings"
	"testing"
)

// fakeManagedBackend is an in-package ManagedLLMServer used to drive
// startManagedServer's fork/reap path without a real llama-server. ServerBinary
// and BuildServerArgs point at a short-lived shell command so the forked child
// exits immediately, exercising the start-crash detection. Its fields are fixed
// at construction, so concurrent registry readers cannot race it.
type fakeManagedBackend struct {
	name   string
	binary string
	args   []string
}

func (f *fakeManagedBackend) Name() string                                 { return f.name }
func (f *fakeManagedBackend) DisplayName() string                          { return f.name }
func (f *fakeManagedBackend) DefaultAddr() string                          { return "" }
func (f *fakeManagedBackend) HealthCheck(string) error                     { return errors.New("unreachable") }
func (f *fakeManagedBackend) ResolveModel(*Config, string) (string, error) { return "", nil }
func (f *fakeManagedBackend) LoadModel(string, *ResolvedProfile) error     { return nil }
func (f *fakeManagedBackend) UnloadModel(string, string) error             { return nil }
func (f *fakeManagedBackend) TryStart(*Config, string) error               { return nil }
func (f *fakeManagedBackend) TryStop(string) error                         { return nil }

func (f *fakeManagedBackend) ServerBinary(*Config) string { return f.binary }
func (f *fakeManagedBackend) BuildServerArgs(*Config, *ResolvedProfile) []string {
	return f.args
}
func (f *fakeManagedBackend) BuildServerEnv(*Config, *ResolvedProfile) []string { return nil }

// fakeManagedFastExit forks a child that exits immediately with a non-zero
// status, standing in for a server that dies during startup (port conflict,
// bad args, ...).
var fakeManagedFastExit = &fakeManagedBackend{
	name:   "fake-managed-fastexit",
	binary: "sh",
	args:   []string{"-c", "exit 1"},
}

func init() {
	RegisterLLMServer(fakeManagedFastExit)
}

// TestStartServer_ManagedChildExitsImmediately: a managed server whose forked
// child exits within the startup grace period must surface a start-crash error
// rather than reporting success. Before the child was reaped, the exited
// process lingered as a zombie that still satisfied kill(pid, 0), so the
// liveness check reported it as alive and StartServer wrongly returned a
// RunningInstance for a server that had already died.
func TestStartServer_ManagedChildExitsImmediately(t *testing.T) {
	host := "127.0.0.1"
	port := 0
	cfg := &Config{
		LogDir:  t.TempDir(),
		Servers: map[string]ServerConfig{fakeManagedFastExit.name: {Enabled: true}},
	}
	profile := &ResolvedProfile{
		Name:          "fastexit",
		Backend:       fakeManagedFastExit.name,
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	inst, err := StartServer(cfg, profile)
	if err == nil {
		t.Fatalf("StartServer = %+v, nil error; want a start-crash error for a child that exits immediately", inst)
	}
	if !strings.Contains(err.Error(), "exited immediately") {
		t.Errorf("err = %v, want an 'exited immediately' start-crash error", err)
	}
}
