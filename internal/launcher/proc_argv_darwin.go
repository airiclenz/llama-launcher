//go:build darwin

package launcher

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// procArgv returns the true argv of process pid, read from the kernel's
// kern.procargs2 sysctl. It errors when the process has exited or belongs to
// another user (the kernel refuses to show another user's arguments).
func procArgv(pid int) ([]string, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, fmt.Errorf("reading kern.procargs2 of PID %d: %w", pid, err)
	}
	return parseProcArgs2(buf)
}
