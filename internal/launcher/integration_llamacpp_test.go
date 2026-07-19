//go:build integration

// Real-llama-server lifecycle tests (Layer 2; see integration_test.go for
// the suite conventions). Both tests need the llama-server binary on PATH
// and INTEGRATION_MODEL_LLAMACPP set to an absolute .gguf path — they skip
// otherwise. Unlike the 2026-05-20 Layer-2 sketch, the start path here is
// the real in-package one (StartServer with a constructed Config +
// ResolvedProfile), so Setsid, the reaper goroutine and the startup-grace
// crash detection are exercised for real instead of being re-implemented
// around exec.Command.

package launcher

import (
	"net"
	"os"
	"syscall"
	"testing"
	"time"
)

const (
	// llamaCppHealthyTimeout bounds the real model load. Generous because a
	// cold-cache .gguf takes a while; the suite-wide go-test timeout still
	// caps the total run.
	llamaCppHealthyTimeout = 2 * time.Minute
	// llamaCppStopTimeout bounds the post-stop verification: the SIGTERM →
	// SIGKILL escalation plus the port release.
	llamaCppStopTimeout = 30 * time.Second
)

// integrationLlamaCppModel returns the absolute .gguf path from
// INTEGRATION_MODEL_LLAMACPP, skipping the test when the variable is unset.
func integrationLlamaCppModel(t *testing.T) string {
	t.Helper()
	model := os.Getenv("INTEGRATION_MODEL_LLAMACPP")
	if model == "" {
		t.Skip("INTEGRATION_MODEL_LLAMACPP not set (absolute path to a .gguf model)")
	}
	return model
}

// llamaCppBackend returns the registered llamacpp backend.
func llamaCppBackend(t *testing.T) LLMServer {
	t.Helper()
	b, err := GetLLMServer("llamacpp")
	if err != nil {
		t.Fatalf("llamacpp backend not registered: %v", err)
	}
	return b
}

// startRealLlamaServer spawns a real llama-server on a free loopback port
// through the managed start path and returns the running instance. logDir
// is passed in rather than derived from t so a start subtest can log into
// a directory owned by its parent test, where the server outlives the
// subtest. The caller registers teardown via killServerOnCleanup — none of
// StartServer's error paths leaves a process behind, so a fatal here needs
// no cleanup of its own.
func startRealLlamaServer(t *testing.T, logDir, model string) *RunningInstance {
	t.Helper()

	host := loopbackHost
	port := freePort(t)
	cfg := &Config{
		LogDir:  logDir,
		Servers: map[string]ServerConfig{"llamacpp": {Enabled: true}},
	}
	profile := &ResolvedProfile{
		Name:          "integration-llamacpp",
		Backend:       "llamacpp",
		ModelPath:     model,
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	inst, err := StartServer(cfg, profile)
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	return inst
}

// killServerOnCleanup registers best-effort teardown for inst on t: a
// normal Stop first, then SIGKILL for anything that survived, so a failed
// test does not leave a server running on the host. Shared by the ollama
// suite too — both backends spawn with Setsid, which gives the child
// PGID == PID, so the group is swept along with the process itself.
func killServerOnCleanup(t *testing.T, inst *RunningInstance) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = Stop(inst.Addr())
		if inst.PID > 0 && IsProcessAlive(inst.PID) {
			_ = syscall.Kill(-inst.PID, syscall.SIGKILL)
			_ = syscall.Kill(inst.PID, syscall.SIGKILL)
		}
	})
}

// waitForStarting polls until the server at addr answers as Starting
// (llama-server holds /health at 503 for the whole model load, ADR-0010).
// A model that finishes loading before the window is observed leaves
// nothing to test, so the test skips rather than fails.
func waitForStarting(t *testing.T, b LLMServer, addr string) {
	t.Helper()
	deadline := time.Now().Add(llamaCppHealthyTimeout)
	for time.Now().Before(deadline) {
		if startingUp(b, addr) {
			return
		}
		if b.HealthCheck(addr) == nil {
			t.Skip("model loaded before the Starting window could be observed; use a larger INTEGRATION_MODEL_LLAMACPP model")
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("server at %s neither Starting nor healthy after %v", addr, llamaCppHealthyTimeout)
}

// TestLlamaCppLifecycle drives a real llama-server through the full
// managed lifecycle, in order: start, wait until healthy, Stop, and verify
// it is gone. Each step aborts the chain when it fails, so a dead server
// is never asked to stop.
func TestLlamaCppLifecycle(t *testing.T) {
	mustFindBinary(t, "llama-server")
	model := integrationLlamaCppModel(t)
	b := llamaCppBackend(t)
	logDir := t.TempDir()

	var inst *RunningInstance
	started := t.Run("start", func(st *testing.T) {
		inst = startRealLlamaServer(st, logDir, model)
	})
	if !started || inst == nil {
		t.Fatal("start step did not produce a running server")
	}
	killServerOnCleanup(t, inst)
	addr := inst.Addr()

	if !t.Run("wait-for-healthy", func(st *testing.T) {
		waitForHealthy(st, b, addr, llamaCppHealthyTimeout)
	}) {
		t.Fatal("server never became healthy; skipping the stop steps")
	}

	if !t.Run("stop", func(st *testing.T) {
		result, err := Stop(addr)
		if err != nil {
			st.Fatalf("Stop(%s): %v", addr, err)
		}
		if result.Instance == nil || result.Instance.PID != inst.PID {
			st.Errorf("Stop reported instance %+v, want the started PID %d", result.Instance, inst.PID)
		}
	}) {
		t.Fatal("stop failed; skipping the stop verification")
	}

	t.Run("wait-for-unhealthy", func(st *testing.T) {
		waitForUnhealthy(st, b, addr, llamaCppStopTimeout)
	})
}

// TestLlamaCppStopWhileStarting is the flagship ADR-0010 scenario against
// a real server: llama-server answers /health with 503 while it loads its
// model, and an explicit Stop must identify and take down that Starting
// instance mid-load — through the StartupProber second pass, with the
// hardened stopped-means-not-healthy-and-not-Starting verification — and
// release the port.
func TestLlamaCppStopWhileStarting(t *testing.T) {
	mustFindBinary(t, "llama-server")
	model := integrationLlamaCppModel(t)
	b := llamaCppBackend(t)

	inst := startRealLlamaServer(t, t.TempDir(), model)
	killServerOnCleanup(t, inst)
	addr := inst.Addr()

	waitForStarting(t, b, addr)

	result, err := Stop(addr)
	if err != nil {
		t.Fatalf("Stop(%s) while Starting: %v", addr, err)
	}
	if result.Instance == nil || result.Instance.PID != inst.PID {
		t.Errorf("Stop reported instance %+v, want the started PID %d", result.Instance, inst.PID)
	}

	waitForUnhealthy(t, b, addr, llamaCppStopTimeout)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s not released after stopping the Starting server: %v", addr, err)
	}
	if err := listener.Close(); err != nil {
		t.Errorf("closing the port-release probe listener: %v", err)
	}
}
