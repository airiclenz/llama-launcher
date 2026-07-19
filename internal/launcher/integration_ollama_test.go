//go:build integration

// Real-Ollama lifecycle tests (Layer 2; see integration_test.go for the
// suite conventions). The test needs the ollama binary on PATH and skips
// otherwise. TryStart binds the requested address by exporting
// OLLAMA_HOST=<addr> into the child environment (backend_ollama.go), so
// the suite serves on a freePort loopback address and never touches a real
// instance at the default 11434.
//
// The model steps (LoadModel, ListRunningModels, UnloadModel) run only
// when INTEGRATION_MODEL_OLLAMA names a model that is already pulled
// locally (e.g. "qwen3:0.6b") — LoadModel does not pull.
//
// The stop step goes through the launcher's unified Stop(addr): Ollama's
// TryStop is deliberately a no-op (the address-scoped lsof/PID path in
// stopServerAt does the stopping), so Stop is the call that actually makes
// the listener go away — and it still runs the TryStop hook inside the
// real stop sequence.

package launcher

import (
	"os"
	"testing"
	"time"
)

const (
	// ollamaHealthyTimeout bounds `ollama serve` startup: the API binds
	// and answers without loading any model, so this stays short.
	ollamaHealthyTimeout = 30 * time.Second
	// ollamaStopTimeout bounds the post-stop verification: the SIGTERM →
	// SIGKILL escalation plus the port release.
	ollamaStopTimeout = 30 * time.Second
	// ollamaUnloadTimeout bounds the wait for /api/ps to drop a model
	// after UnloadModel's keep_alive:0 call has returned.
	ollamaUnloadTimeout = 30 * time.Second
)

// ollamaBackend returns the registered ollama backend.
func ollamaBackend(t *testing.T) LLMServer {
	t.Helper()
	b, err := GetLLMServer("ollama")
	if err != nil {
		t.Fatalf("ollama backend not registered: %v", err)
	}
	return b
}

// ollamaModelListed reports whether /api/ps lists model. Ollama normalises
// a bare model name with a ":latest" tag, so a requested "qwen3" matches a
// reported "qwen3:latest".
func ollamaModelListed(models []RunningModelInfo, model string) bool {
	for _, m := range models {
		if m.Name == model || m.Name == model+":latest" {
			return true
		}
	}
	return false
}

// waitForOllamaModelGone polls /api/ps until model is no longer listed,
// failing the test when timeout elapses first: UnloadModel's keep_alive:0
// generate call can return before the runner has released the model.
func waitForOllamaModelGone(
	t *testing.T,
	lister ModelLister,
	addr, model string,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		models, err := lister.ListRunningModels(addr)
		if err == nil && !ollamaModelListed(models, model) {
			return
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("model %q still listed at %s after %v", model, addr, timeout)
}

// runOllamaModelSteps drives the model half of the lifecycle against the
// healthy server at addr: load the model, assert /api/ps lists it, unload
// it, and wait for /api/ps to drop it again. Runs with the parent test's t
// so a failed step aborts the whole chain.
func runOllamaModelSteps(t *testing.T, b LLMServer, addr, model string) {
	t.Helper()
	lister, ok := b.(ModelLister)
	if !ok {
		t.Fatal("ollama backend does not implement ModelLister")
	}

	if !t.Run("load-model", func(st *testing.T) {
		profile := &ResolvedProfile{
			Name:      "integration-ollama",
			Backend:   "ollama",
			ModelPath: model,
		}
		if err := b.LoadModel(addr, profile); err != nil {
			st.Fatalf("LoadModel(%s, %q): %v (INTEGRATION_MODEL_OLLAMA must name a pre-pulled model)", addr, model, err)
		}
	}) {
		t.Fatal("load-model failed; skipping the remaining steps")
	}

	if !t.Run("list-running-models", func(st *testing.T) {
		models, err := lister.ListRunningModels(addr)
		if err != nil {
			st.Fatalf("ListRunningModels(%s): %v", addr, err)
		}
		if !ollamaModelListed(models, model) {
			st.Fatalf("loaded model %q not listed by /api/ps: %+v", model, models)
		}
	}) {
		t.Fatal("list-running-models failed; skipping the remaining steps")
	}

	if !t.Run("unload-model", func(st *testing.T) {
		if err := b.UnloadModel(addr, model); err != nil {
			st.Fatalf("UnloadModel(%s, %q): %v", addr, model, err)
		}
		waitForOllamaModelGone(st, lister, addr, model, ollamaUnloadTimeout)
	}) {
		t.Fatal("unload-model failed; skipping the remaining steps")
	}
}

// TestOllamaLifecycle drives a real `ollama serve` through the launcher's
// lifecycle, in order: TryStart on a private loopback port, wait until
// healthy, load/list/unload a model when INTEGRATION_MODEL_OLLAMA is set,
// Stop, and verify the listener is gone. Each step aborts the chain when
// it fails; the kill cleanup still tears the server down.
func TestOllamaLifecycle(t *testing.T) {
	mustFindBinary(t, "ollama")
	b := ollamaBackend(t)
	pt, ok := b.(PIDTracker)
	if !ok {
		t.Fatal("ollama backend does not implement PIDTracker; cannot track the spawned PID for cleanup")
	}

	inst := &RunningInstance{Backend: "ollama", Host: loopbackHost, Port: freePort(t)}
	addr := inst.Addr()
	cfg := &Config{LogDir: t.TempDir()}

	if !t.Run("try-start", func(st *testing.T) {
		if err := b.TryStart(cfg, addr); err != nil {
			st.Fatalf("TryStart(%s): %v", addr, err)
		}
	}) {
		t.Fatal("try-start failed; skipping the rest of the lifecycle")
	}
	inst.PID = pt.LastStartedPID()
	killServerOnCleanup(t, inst)

	if !t.Run("wait-for-healthy", func(st *testing.T) {
		waitForHealthy(st, b, addr, ollamaHealthyTimeout)
	}) {
		t.Fatal("server never became healthy; skipping the remaining steps")
	}

	if model := os.Getenv("INTEGRATION_MODEL_OLLAMA"); model != "" {
		runOllamaModelSteps(t, b, addr, model)
	} else {
		t.Log("INTEGRATION_MODEL_OLLAMA not set; skipping the load/list/unload steps")
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
		waitForUnhealthy(st, b, addr, ollamaStopTimeout)
	})
}
