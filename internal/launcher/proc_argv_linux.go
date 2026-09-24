//go:build linux

package launcher

import (
	"fmt"
	"os"
	"strconv"
)

// procArgv returns the true argv of process pid, read from
// /proc/<pid>/cmdline. It errors when the process has exited or the file is
// empty (a kernel thread or a zombie).
func procArgv(pid int) ([]string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return nil, fmt.Errorf("reading the command line of PID %d: %w", pid, err)
	}
	return parseProcCmdline(data)
}
