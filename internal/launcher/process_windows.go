//go:build windows

// Process control on windows, which has none of the unix mechanisms the
// launcher's server lifecycle is built on: no session to detach a spawned
// server into and no process group to signal it by. Each function answers
// process_unix.go's signature with an error wrapping ErrUnsupported, so the
// package builds here and everything it drives over HTTP — discovery, model
// load/unload, activation against an already-running server — keeps working
// (ADR-0012). A real implementation (Job objects, taskkill) lands here
// additively if a windows user ever needs one.

package launcher

import (
	"fmt"
	"syscall"
)

// detachedSysProcAttr returns nil, the harmless zero value for a spawn that
// never happens: requireProcessControl refuses every fork path before the
// attributes are used.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// signalPID refuses to signal a single process: windows has no kill(2), so
// neither the stop escalation nor the liveness check can act here.
func signalPID(pid int, _ syscall.Signal) error {
	return fmt.Errorf("signalling PID %d: %w", pid, ErrUnsupported)
}

// signalGroup refuses to signal a process group: windows has none.
func signalGroup(pid int, _ syscall.Signal) error {
	return fmt.Errorf("signalling the process group of PID %d: %w", pid, ErrUnsupported)
}

// requireProcessControl refuses the fork paths outright. Forking first and
// failing later would leave a running child no launcher path could stop.
func requireProcessControl() error {
	return fmt.Errorf("starting a server process: %w", ErrUnsupported)
}

// requireProcessStop refuses stopping a server by its PID: with no signal to
// send, a server its native stop hook does not stop stays up, and the stop
// verbs say so with this sentinel rather than a generic failure.
func requireProcessStop() error {
	return fmt.Errorf("stopping a server process: %w", ErrUnsupported)
}

// listProcesses refuses to read the process table: nothing the launcher could
// find there is a process it could signal on windows, so a loading Splash
// stays invisible here (ADR-0015).
func listProcesses() ([]processEntry, error) {
	return nil, fmt.Errorf("listing processes: %w", ErrUnsupported)
}
