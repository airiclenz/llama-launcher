//go:build darwin

// The interactive menu's stdin poll on darwin, where syscall.Select has the
// BSD one-value signature. ui_poll_linux.go answers the same two functions
// with the linux two-value form and ui_poll_windows.go refuses the menu, so
// the package builds on all three platforms (ADR-0012).

package launcher

import (
	"syscall"
	"time"
)

// pollStdin reports whether stdin has a byte ready to read before timeout
// elapses. A select failure counts as "nothing ready": the menu then treats
// the wait as a refresh tick rather than a keystroke.
func pollStdin(timeout time.Duration) bool {
	tv := syscall.NsecToTimeval(int64(timeout))
	var fds syscall.FdSet
	fds.Bits[0] = 1
	if err := syscall.Select(1, &fds, nil, nil, &tv); err != nil {
		return false
	}
	return fds.Bits[0]&1 != 0
}

// requireInteractiveMenu reports whether this build can run the interactive
// menu. darwin can poll stdin, so it can.
func requireInteractiveMenu() error {
	return nil
}
