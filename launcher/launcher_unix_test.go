//go:build !windows

package launcher_test

import (
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/airiclenz/llama-launcher/launcher"
)

// facadeAuthHelperAddrEnv names the environment variable that turns this
// test binary into an auth-refusing server child
// (TestFacadeAuthRefusingHelperProcess).
const facadeAuthHelperAddrEnv = "LLAMA_LAUNCHER_TEST_FACADE_AUTH_HELPER_ADDR"

// authChildStartTimeout bounds how long startFacadeAuthRefusingChild waits
// for the child to answer, and authChildExitTimeout how long a stopped
// child may take to be reaped.
const (
	authChildStartTimeout = 10 * time.Second
	authChildExitTimeout  = 5 * time.Second
	authChildPollInterval = 50 * time.Millisecond
)

// TestFacadeAuthRefusingHelperProcess is not a test of its own: re-executed
// as a child process with facadeAuthHelperAddrEnv set, it serves 401 to
// every request at that address until signalled — a real listener Stop can
// find by lsof and signal without touching the test process.
func TestFacadeAuthRefusingHelperProcess(t *testing.T) {
	addr := os.Getenv(facadeAuthHelperAddrEnv)
	if addr == "" {
		t.Skip("runs only as the child of the facade's auth-refusing stop tests")
	}
	refuse := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if err := http.ListenAndServe(addr, refuse); err != nil {
		t.Fatalf("serving %s: %v", addr, err)
	}
}

// authRefusingChild is a running TestFacadeAuthRefusingHelperProcess child:
// its address, its process, and a channel closed once it has been reaped.
type authRefusingChild struct {
	addr   string
	cmd    *exec.Cmd
	exited chan struct{}
}

// startFacadeAuthRefusingChild starts TestFacadeAuthRefusingHelperProcess
// as a child in its own session, listening at a fresh loopback address, and
// returns once the child answers 401 there.
func startFacadeAuthRefusingChild(t *testing.T) *authRefusingChild {
	t.Helper()

	addr := deadAddr(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestFacadeAuthRefusingHelperProcess$")
	cmd.Env = append(os.Environ(), facadeAuthHelperAddrEnv+"="+addr)
	// Its own session, so the stop's process-group signal cannot reach the
	// test process.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the auth-refusing child: %v", err)
	}
	child := &authRefusingChild{addr: addr, cmd: cmd, exited: make(chan struct{})}
	// Reap the child as soon as it exits: a zombie still counts as alive,
	// which would stall the stop's wait for the process to go.
	go func() {
		cmd.Wait()
		close(child.exited)
	}()
	t.Cleanup(func() { cmd.Process.Kill() })

	deadline := time.Now().Add(authChildStartTimeout)
	for !answersUnauthorized(addr) {
		if time.Now().After(deadline) {
			t.Fatalf("the auth-refusing child (PID %d) never answered 401 on %s", cmd.Process.Pid, addr)
		}
		time.Sleep(authChildPollInterval)
	}
	return child
}

// answersUnauthorized reports whether a request to addr is answered 401.
func answersUnauthorized(addr string) bool {
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusUnauthorized
}

// TestStop_UnconfiguredAuthRefusingListenerIsNotSignalled pins ADR-0010's
// "at a configured address" through the facade: a real listener answering
// 401 at an address no loaded config names is not running as far as Stop
// is concerned, and is left serving. Not parallel: a parallel LoadConfig
// elsewhere in the package must not be running while the address is
// expected unconfigured.
func TestStop_UnconfiguredAuthRefusingListenerIsNotSignalled(t *testing.T) {
	child := startFacadeAuthRefusingChild(t)

	result, err := launcher.Stop(child.addr)

	if !errors.Is(err, launcher.ErrNotRunning) {
		t.Errorf("err = %v, want it to wrap launcher.ErrNotRunning", err)
	}
	if result == nil {
		t.Error("result = nil, want a non-nil StopResult")
	}
	if !answersUnauthorized(child.addr) {
		t.Errorf("the child at %s no longer answers 401, want it left running", child.addr)
	}
}

// TestStop_ConfiguredAuthRefusingListenerIsStopped: once LoadConfig points a
// backend at the address, the same 401 listener is stopped by signalling it
// alone, and the result names no backend — none identified it. Not
// parallel: LoadConfig sets the process-global configured addresses.
func TestStop_ConfiguredAuthRefusingListenerIsStopped(t *testing.T) {
	child := startFacadeAuthRefusingChild(t)
	host, port := splitAddr(t, child.addr)
	if _, err := launcher.LoadConfig(writeConfig(t, standInConfig, host, port), nil); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	result, err := launcher.Stop(child.addr)

	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if result.Instance == nil || result.Instance.Backend != "" || result.Instance.PID != child.cmd.Process.Pid {
		t.Errorf("Instance = %+v, want Backend \"\" and PID %d", result.Instance, child.cmd.Process.Pid)
	}
	select {
	case <-child.exited:
	case <-time.After(authChildExitTimeout):
		t.Errorf("the child (PID %d) was not reaped after Stop", child.cmd.Process.Pid)
	}
}

// splitAddr splits a host:port address into the host and numeric port
// writeConfig takes.
func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()

	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("splitting %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parsing the port of %q: %v", addr, err)
	}
	return host, port
}
