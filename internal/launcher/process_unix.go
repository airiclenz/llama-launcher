//go:build unix

// Process control on the platforms that have it. Every unix-only primitive
// the launcher needs to fork and stop a server lives behind these four
// functions; process_windows.go answers the same signatures with the
// unsupported sentinel so the package builds — and actuates over HTTP —
// there too (ADR-0012).

package launcher

import "syscall"

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
