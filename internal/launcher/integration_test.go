//go:build integration

// Shared helpers for the Layer-2 integration suite (backend-tests-plan.md
// Layer 2; validated spec: docs/plans/2026-07-19-starting-stop-and-
// integration-tests.md, Items 10–15). These tests talk to real local LLM
// servers, so the whole suite is run manually on the host via
// `make test-integration` — never as part of the untagged unit run.
//
// Conventions for every integration file in this package:
//   - No t.Parallel() anywhere: real servers contend for ports, GPU memory
//     and the per-backend singletons, so subtests run strictly in order.
//   - Log directories are per-test t.TempDir() paths, never the user's real
//     log directory.
package launcher

import (
	"net"
	"os/exec"
	"testing"
	"time"
)

// integrationPollInterval is the delay between probes while waiting for a
// real server to change state.
const integrationPollInterval = 250 * time.Millisecond

// mustFindBinary resolves the named executable via PATH and returns its
// location, skipping the test when the host does not have it installed.
func mustFindBinary(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("binary %q not found in PATH: %v", name, err)
	}
	return path
}

// freePort returns a loopback TCP port that was free at the time of the
// call: the port comes from a just-closed listener, so a server started
// immediately afterwards can bind it. The close-then-reuse race is accepted
// for this sequential, manually-invoked suite.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("releasing free-port probe listener: %v", err)
	}
	return port
}

// waitForHealthy polls b's health endpoint at addr until it answers
// healthy, failing the test when timeout elapses first.
func waitForHealthy(t *testing.T, b LLMServer, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = b.HealthCheck(addr)
		if lastErr == nil {
			return
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("server at %s not healthy after %v: %v", addr, timeout, lastErr)
}

// waitForUnhealthy polls until nothing of b's answers at addr any more,
// failing the test when timeout elapses first. Gone means not healthy *and*
// not StartingUp: per the ADR-0010 stop-verification rule, a surviving
// Starting server also fails the plain health check (503 for the whole
// model load), so health alone would report it as stopped.
func waitForUnhealthy(t *testing.T, b LLMServer, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b.HealthCheck(addr) != nil && !startingUp(b, addr) {
			return
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatalf("server at %s still reachable after %v", addr, timeout)
}
