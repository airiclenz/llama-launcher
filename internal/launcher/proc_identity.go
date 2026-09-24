// A process's identity: the kernel's record of when it started. A PID alone
// does not name a process — once the process exits, the kernel may hand the
// same number to an unrelated one — so the stop path records the start time
// when it resolves a PID and signals only while that record still matches
// (terminatePID). The parser here decodes linux's /proc/<pid>/stat; only the
// readers that fetch an identity (proc_identity_darwin.go,
// proc_identity_linux.go, proc_identity_windows.go) are platform-specific, so
// it runs and is tested on every platform.

package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

// procStatStartTimeField is the 1-based position of starttime in
// /proc/<pid>/stat (proc(5)).
const procStatStartTimeField = 22

// procStatFirstFieldAfterComm is the 1-based position of the first field
// after the parenthesised comm (the state letter).
const procStatFirstFieldAfterComm = 3

// sameProcess reports whether pid still names the process whose identity was
// recorded. An identity that can no longer be read — the process exited and
// was reaped — counts as a mismatch, exactly like a reused PID.
func sameProcess(pid int, identity int64) bool {
	current, err := processIdentity(pid)
	return err == nil && current == identity
}

// parseProcStatStartTime returns the starttime field (22, clock ticks since
// boot) of a linux /proc/<pid>/stat line. The comm field is parenthesised and
// may itself contain spaces and parentheses, so the fields are counted from
// the last ')' onwards.
func parseProcStatStartTime(data []byte) (int64, error) {
	commEnd := bytes.LastIndexByte(data, ')')
	if commEnd < 0 {
		return 0, errors.New("/proc stat: no closing parenthesis after comm")
	}
	fields := bytes.Fields(data[commEnd+1:])
	index := procStatStartTimeField - procStatFirstFieldAfterComm
	if len(fields) <= index {
		return 0, fmt.Errorf("/proc stat: %d fields after comm, starttime needs %d", len(fields), index+1)
	}
	start, err := strconv.ParseInt(string(fields[index]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("/proc stat: starttime: %w", err)
	}
	return start, nil
}
