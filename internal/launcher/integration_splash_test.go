//go:build integration

// Real-Splash lifecycle test (Layer 2; see integration_test.go for the suite
// conventions). It needs Splash's `splash` command on PATH and
// INTEGRATION_MODEL_SPLASH set to an installed Hugging Face `owner/repo` —
// it skips otherwise. The profile is resolved through Config.ResolveProfile,
// so the Hugging Face cache install check runs for real, and the server is
// started through the managed StartServer path and stopped through the
// launcher's normal Stop(addr) SIGTERM path. Splash binds its port only once
// the model has loaded, so the Starting state is observed through the
// process table (ADR-0015): TestSplashLifecycle requires it on the way to
// healthy, and TestSplashStopWhileLoading stops a server mid-load.
//
// Liveness checks on the start PID and its group go through the process
// seam (signalPID / signalGroup with signal 0), never syscall.Kill, so the
// file still vets under GOOS=windows.

package launcher

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	// splashHealthyTimeout bounds the real model load. Splash maps a large
	// model into memory before /ready turns 200, so it is longer than the
	// llama-server bound; the suite-wide go-test timeout still caps the run.
	splashHealthyTimeout = 3 * time.Minute
	// splashStopTimeout bounds the post-stop verification: the SIGTERM →
	// SIGKILL escalation plus the port release and process-group exit.
	splashStopTimeout = 30 * time.Second
	// splashProfileName is the profile the test resolves.
	splashProfileName = "integration-splash"
)

// integrationSplashModel returns the Hugging Face repo id from
// INTEGRATION_MODEL_SPLASH, skipping the test when the variable is unset.
func integrationSplashModel(t *testing.T) string {
	t.Helper()
	model := os.Getenv("INTEGRATION_MODEL_SPLASH")
	if model == "" {
		t.Skip("INTEGRATION_MODEL_SPLASH not set (an installed Hugging Face owner/repo)")
	}
	return model
}

// resolveSplashProfile builds a one-profile config for model on a free
// loopback port and resolves it the way the CLI does.
func resolveSplashProfile(t *testing.T, logDir, model string) (*Config, *ResolvedProfile) {
	t.Helper()

	server := "splash"
	host := loopbackHost
	port := freePort(t)
	cfg := &Config{
		LogDir:  logDir,
		Servers: map[string]ServerConfig{"splash": {Enabled: true}},
		Profiles: map[string]Profile{
			splashProfileName: {
				Model:         model,
				ProfileParams: ProfileParams{Server: &server, Host: &host, Port: &port},
			},
		},
	}

	profile, err := cfg.ResolveProfile(splashProfileName)
	if err != nil {
		t.Fatalf("ResolveProfile(%s): %v", splashProfileName, err)
	}
	return cfg, profile
}

// waitForSplashHealthy polls until the Splash server at addr answers
// healthy, failing when splashHealthyTimeout elapses first. It reports
// whether the Starting state was seen on the way — for a Splash that has
// not bound its port yet, through the process table (ADR-0015).
func waitForSplashHealthy(t *testing.T, b LLMServer, addr string) bool {
	t.Helper()
	sawStarting := false
	deadline := time.Now().Add(splashHealthyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = b.HealthCheck(addr)
		if lastErr == nil {
			return sawStarting
		}
		if startingUp(b, addr) {
			sawStarting = true
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("splash at %s not healthy after %v: %v", addr, splashHealthyTimeout, lastErr)
	return sawStarting
}

// waitForProcessGone polls until neither pid nor any member of its process
// group exists, failing when timeout elapses first.
func waitForProcessGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) && signalGroup(pid, 0) != nil {
			return
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("PID %d or its process group still alive %v after Stop", pid, timeout)
}

// TestSplashLifecycle drives a real Splash server through one managed
// cycle, in order: resolve, start, wait until healthy (noting Starting on
// the way), list the served model, confirm llamacpp's health check rejects
// the address, Stop, and verify the port is free and the process group is
// gone. Each step aborts the chain when it fails, so a dead server is never
// asked to stop.
func TestSplashLifecycle(t *testing.T) {
	mustFindBinary(t, "splash")
	model := integrationSplashModel(t)
	b, err := GetLLMServer("splash")
	if err != nil {
		t.Fatalf("splash backend not registered: %v", err)
	}
	cfg, profile := resolveSplashProfile(t, t.TempDir(), model)

	inst, err := StartServer(cfg, profile)
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	killServerOnCleanup(t, inst)
	addr := inst.Addr()

	if !t.Run("wait-for-healthy", func(st *testing.T) {
		if !waitForSplashHealthy(st, b, addr) {
			st.Error("the loading server was never observed as Starting")
		}
	}) {
		t.Fatal("server never became healthy; skipping the remaining steps")
	}

	t.Run("list-running-models", func(st *testing.T) {
		lister, ok := b.(ModelLister)
		if !ok {
			st.Fatal("splash backend does not implement ModelLister")
		}
		models, err := lister.ListRunningModels(addr)
		if err != nil {
			st.Fatalf("ListRunningModels(%s): %v", addr, err)
		}
		for _, m := range models {
			if m.Name == model {
				return
			}
		}
		st.Errorf("ListRunningModels(%s) = %+v, want an entry named %q", addr, models, model)
	})

	t.Run("llamacpp-rejects", func(st *testing.T) {
		if err := (&LlamaCpp{}).HealthCheck(addr); err == nil {
			st.Errorf("LlamaCpp.HealthCheck(%s) accepted a Splash server", addr)
		}
	})

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

	t.Run("stopped", func(st *testing.T) {
		waitForUnhealthy(st, b, addr, splashStopTimeout)
		waitForProcessGone(st, inst.PID, splashStopTimeout)

		listener, err := net.Listen("tcp", addr)
		if err != nil {
			st.Fatalf("port %s not released after Stop: %v", addr, err)
		}
		if err := listener.Close(); err != nil {
			st.Errorf("closing the port-release probe listener: %v", err)
		}
	})
}

// TestSplashStopWhileLoading starts a real Splash server and, while it is
// still loading its model and has not bound its port, checks that discovery
// reports it as Starting, that a second start is refused instead of forking a
// duplicate, and that Stop(addr) signals the started PID and takes its
// process group down (ADR-0015).
func TestSplashStopWhileLoading(t *testing.T) {
	mustFindBinary(t, "splash")
	model := integrationSplashModel(t)
	b, err := GetLLMServer("splash")
	if err != nil {
		t.Fatalf("splash backend not registered: %v", err)
	}
	cfg, profile := resolveSplashProfile(t, t.TempDir(), model)

	inst, err := StartServer(cfg, profile)
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	killServerOnCleanup(t, inst)
	addr := inst.Addr()

	if !startingUp(b, addr) {
		if b.HealthCheck(addr) == nil {
			t.Skip("model loaded before the Starting state could be observed")
		}
		t.Fatalf("a loading splash at %s is not reported as Starting", addr)
	}

	t.Run("discovery-reports-starting", func(st *testing.T) {
		found := findInstance(DiscoverRunningInstances(cfg), addr)
		if found == nil || !found.Starting || found.Backend != "splash" {
			st.Errorf("discovery at %s = %+v, want a Starting splash", addr, found)
		}
	})

	t.Run("second-start-refused", func(st *testing.T) {
		_, err := StartServer(cfg, profile)
		if err == nil || !strings.Contains(err.Error(), "still starting up") {
			st.Errorf("second StartServer = %v, want the still-starting-up refusal", err)
		}
	})

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

	waitForProcessGone(t, inst.PID, splashStopTimeout)
}
