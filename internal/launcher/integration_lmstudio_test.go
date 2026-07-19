//go:build integration

// Real-LM-Studio lifecycle tests (Layer 2; see integration_test.go for the
// suite conventions). The test needs the lms CLI on PATH and skips
// otherwise.
//
// `lms server start` drives the LM Studio *app instance*: LM Studio runs
// exactly one API server, owned by the (possibly headless) app, so this
// suite starts, moves and stops that instance rather than a private child
// process. Running the suite can therefore interfere with an interactive
// LM Studio session; that is accepted for a manually-invoked host-side
// suite. TryStart forwards only the port from addr (`--port`, see
// backend_lmstudio.go) and LM Studio picks the bind interface itself, so
// freePort here avoids port collisions but — unlike the Ollama suite —
// cannot isolate the run from the user's own server: it is the same
// instance.
//
// The model steps (LoadModel, ListRunningModels, UnloadModel) run only
// when INTEGRATION_MODEL_LMSTUDIO names a model already downloaded in LM
// Studio (e.g. "qwen2.5-0.5b-instruct") — LoadModel does not download.
//
// The stop step goes through the launcher's unified Stop(addr), the
// documented sequence (TDD §6.5): signal the listening PID first, then run
// the `lms server stop` hook — exactly what an interactive
// `llama-launcher stop` does to an LM Studio instance.

package launcher

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// lmStudioHealthyTimeout bounds the wait for the API to answer after
	// TryStart. It stays short because `lms server start` itself blocks
	// until the CLI reports the server up — the app boot happens inside
	// the try-start step, not here.
	lmStudioHealthyTimeout = 30 * time.Second
	// lmStudioStopTimeout bounds the post-stop verification: the SIGTERM →
	// SIGKILL escalation plus the port release.
	lmStudioStopTimeout = 30 * time.Second
	// lmStudioUnloadTimeout bounds the wait for /v1/models to drop a model
	// after UnloadModel has returned.
	lmStudioUnloadTimeout = 30 * time.Second
)

// lmStudioBackend returns the registered lmstudio backend.
func lmStudioBackend(t *testing.T) LLMServer {
	t.Helper()
	b, err := GetLLMServer("lmstudio")
	if err != nil {
		t.Fatalf("lmstudio backend not registered: %v", err)
	}
	return b
}

// findLMStudioModel returns the identifier under which /v1/models lists the
// requested model, if any. The identifier of an API-loaded model defaults
// to the model key itself; loading a key that is already loaded yields an
// instance-suffixed identifier such as "key:2", so a "key:" prefix match
// is accepted too.
func findLMStudioModel(models []RunningModelInfo, model string) (string, bool) {
	for _, m := range models {
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			return m.Name, true
		}
	}
	return "", false
}

// waitForLMStudioModelGone polls /v1/models until identifier is no longer
// listed, failing the test when timeout elapses first.
func waitForLMStudioModelGone(
	t *testing.T,
	lister ModelLister,
	addr, identifier string,
	timeout time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		models, err := lister.ListRunningModels(addr)
		if err == nil {
			if _, listed := findLMStudioModel(models, identifier); !listed {
				return
			}
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("model %q still listed at %s after %v", identifier, addr, timeout)
}

// runLMStudioModelSteps drives the model half of the lifecycle against the
// healthy server at addr: load the model, assert /v1/models lists it, then
// unload it under the identifier the listing reported and wait for the
// listing to drop it again. Runs with the parent test's t so a failed step
// aborts the whole chain.
func runLMStudioModelSteps(t *testing.T, b LLMServer, addr, model string) {
	t.Helper()
	lister, ok := b.(ModelLister)
	if !ok {
		t.Fatal("lmstudio backend does not implement ModelLister")
	}

	if !t.Run("load-model", func(st *testing.T) {
		profile := &ResolvedProfile{
			Name:      "integration-lmstudio",
			Backend:   "lmstudio",
			ModelPath: model,
		}
		if err := b.LoadModel(addr, profile); err != nil {
			st.Fatalf("LoadModel(%s, %q): %v (INTEGRATION_MODEL_LMSTUDIO must name a model already downloaded in LM Studio)", addr, model, err)
		}
	}) {
		t.Fatal("load-model failed; skipping the remaining steps")
	}

	var identifier string
	if !t.Run("list-running-models", func(st *testing.T) {
		models, err := lister.ListRunningModels(addr)
		if err != nil {
			st.Fatalf("ListRunningModels(%s): %v", addr, err)
		}
		id, found := findLMStudioModel(models, model)
		if !found {
			st.Fatalf("loaded model %q not listed by /v1/models: %+v", model, models)
		}
		identifier = id
	}) {
		t.Fatal("list-running-models failed; skipping the remaining steps")
	}

	if !t.Run("unload-model", func(st *testing.T) {
		if err := b.UnloadModel(addr, identifier); err != nil {
			st.Fatalf("UnloadModel(%s, %q): %v", addr, identifier, err)
		}
		waitForLMStudioModelGone(st, lister, addr, identifier, lmStudioUnloadTimeout)
	}) {
		t.Fatal("unload-model failed; skipping the remaining steps")
	}
}

// stopLMStudioOnCleanup registers best-effort teardown for the app server
// at addr: the launcher's unified Stop first (a no-op when nothing
// identifiable answers any more), then a bare TryStop as backstop for a
// half-started server that cannot be identified yet. LM Studio implements
// no PIDTracker — the listener belongs to the LM Studio app instance, not
// to a child this suite spawned — so unlike killServerOnCleanup there is
// no tracked PID to SIGKILL.
func stopLMStudioOnCleanup(t *testing.T, b LLMServer, addr string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = Stop(addr)
		_ = b.TryStop(addr)
	})
}

// TestLMStudioLifecycle drives the real LM Studio app server through the
// launcher's lifecycle, in order: TryStart on a free loopback port, wait
// until healthy, load/list/unload a model when INTEGRATION_MODEL_LMSTUDIO
// is set, Stop, and verify the listener is gone. Each step aborts the
// chain when it fails; the cleanup still tears the server down.
func TestLMStudioLifecycle(t *testing.T) {
	mustFindBinary(t, "lms")
	b := lmStudioBackend(t)

	addr := net.JoinHostPort(loopbackHost, strconv.Itoa(freePort(t)))
	// LM Studio keeps its own logs inside the app; the per-test log dir is
	// passed for the suite's convention but TryStart does not use it.
	cfg := &Config{LogDir: t.TempDir()}

	if !t.Run("try-start", func(st *testing.T) {
		if err := b.TryStart(cfg, addr); err != nil {
			st.Fatalf("TryStart(%s): %v", addr, err)
		}
	}) {
		t.Fatal("try-start failed; skipping the rest of the lifecycle")
	}
	stopLMStudioOnCleanup(t, b, addr)

	if !t.Run("wait-for-healthy", func(st *testing.T) {
		waitForHealthy(st, b, addr, lmStudioHealthyTimeout)
	}) {
		t.Fatal("server never became healthy; skipping the remaining steps")
	}

	if model := os.Getenv("INTEGRATION_MODEL_LMSTUDIO"); model != "" {
		runLMStudioModelSteps(t, b, addr, model)
	} else {
		t.Log("INTEGRATION_MODEL_LMSTUDIO not set; skipping the load/list/unload steps")
	}

	if !t.Run("stop", func(st *testing.T) {
		result, err := Stop(addr)
		if err != nil {
			st.Fatalf("Stop(%s): %v", addr, err)
		}
		if result.Instance == nil || result.Instance.Backend != "lmstudio" {
			st.Errorf("Stop identified instance %+v, want backend lmstudio", result.Instance)
		}
	}) {
		t.Fatal("stop failed; skipping the stop verification")
	}

	t.Run("wait-for-unhealthy", func(st *testing.T) {
		waitForUnhealthy(st, b, addr, lmStudioStopTimeout)
	})
}
