//go:build !darwin && !linux

package launcher

import "fmt"

// procArgv refuses to read a true argv on a platform with no reader for it.
// Nothing calls it on windows, which reads no process table at all
// (ADR-0015); it is defined so the package and its tests build there.
func procArgv(pid int) ([]string, error) {
	return nil, fmt.Errorf("reading the argv of PID %d: %w", pid, ErrUnsupported)
}
