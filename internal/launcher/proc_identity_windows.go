//go:build windows

package launcher

import "fmt"

// processIdentity refuses to read a start time on windows, where the stop
// path signals nothing (process_windows.go): with no identity recorded,
// stopServerAt never reaches terminatePID.
func processIdentity(pid int) (int64, error) {
	return 0, fmt.Errorf("reading the start time of PID %d: %w", pid, ErrUnsupported)
}
