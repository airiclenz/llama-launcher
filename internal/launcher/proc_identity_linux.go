//go:build linux

package launcher

import (
	"fmt"
	"os"
	"strconv"
)

// processIdentity returns the start time of process pid in clock ticks since
// boot, read from /proc/<pid>/stat. It errors when the process has exited.
func processIdentity(pid int) (int64, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, fmt.Errorf("reading the start time of PID %d: %w", pid, err)
	}
	return parseProcStatStartTime(data)
}
