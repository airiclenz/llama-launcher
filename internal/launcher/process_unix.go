//go:build unix

// Process control on the platforms that have it. Every unix-only primitive
// the launcher needs to fork, find and stop a server lives behind these six
// functions; process_windows.go answers the same signatures with the
// unsupported sentinel so the package builds — and actuates over HTTP —
// there too (ADR-0012).

package launcher

import (
	"os/exec"
	"syscall"
)

// detachedSysProcAttr returns the attributes that put a spawned server in a
// session of its own, so it outlives the launcher process and can later be
// stopped as a whole process group (Setsid gives the child PGID = PID).
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// signalPID sends sig to the single process pid. Signal 0 delivers nothing
// and only reports whether the process still exists.
func signalPID(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

// signalGroup sends sig to every process in the group led by pid, so a
// server's own children go down with it and no worker is left holding the
// port.
func signalGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}

// requireProcessControl reports whether this build may fork a server it is
// expected to be able to stop again. Unix can, so the fork paths proceed.
func requireProcessControl() error {
	return nil
}

// requireProcessStop reports whether this build may stop a server by its
// PID. Unix can, so a stop that leaves the server reachable reports the
// mechanism that failed instead.
func requireProcessStop() error {
	return nil
}

// listProcesses returns every process on the machine with its process group
// and command line, read from ps. A session leader's command line is its true
// argv, read from the kernel (procArgv), so an argument containing spaces
// stays whole; every other row keeps ps's whitespace-split command, as does a
// leader whose argv cannot be read. It backs the search for a loading Splash,
// which holds no address yet and so cannot be found through lsof (ADR-0015).
func listProcesses() ([]processEntry, error) {
	out, err := exec.Command("ps", "-A", "-ww", "-o", "pid=,pgid=,command=").Output()
	if err != nil {
		return nil, err
	}
	return withTrueArgv(parseProcessTable(string(out)), procArgv), nil
}
