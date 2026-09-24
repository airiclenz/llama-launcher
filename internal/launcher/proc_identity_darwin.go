//go:build darwin

package launcher

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// processIdentity returns the start time of process pid in nanoseconds since
// the epoch, read from the kernel's kern.proc.pid sysctl. It errors when the
// process has exited.
func processIdentity(pid int) (int64, error) {
	kinfo, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, fmt.Errorf("reading the start time of PID %d: %w", pid, err)
	}
	start := kinfo.Proc.P_starttime.Nano()
	if start == 0 {
		return 0, fmt.Errorf("reading the start time of PID %d: kernel reports none", pid)
	}
	return start, nil
}
