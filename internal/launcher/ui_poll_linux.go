//go:build linux

// The interactive menu's stdin poll on linux, which is ui_poll_darwin.go's
// poll with the one signature difference between the platforms: linux's
// syscall.Select also returns the ready-descriptor count (ADR-0012).

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
	// The descriptor count is redundant here — the descriptor set carries the
	// same answer, and reading it keeps this poll identical to darwin's.
	if _, err := syscall.Select(1, &fds, nil, nil, &tv); err != nil {
		return false
	}
	return fds.Bits[0]&1 != 0
}

// requireInteractiveMenu reports whether this build can run the interactive
// menu. linux can poll stdin, so it can.
func requireInteractiveMenu() error {
	return nil
}
