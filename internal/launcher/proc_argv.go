// A process's true argument vector, as the kernel holds it. ps prints a
// command line with its arguments joined by spaces, so an argument that itself
// contains a space — an install path like "/opt/My Splash/splash" — cannot be
// told apart from two arguments. The parsers here decode the kernel's
// NUL-separated form instead; only the readers that fetch it
// (proc_argv_darwin.go, proc_argv_linux.go) are platform-specific, so these
// run and are tested on every platform.

package launcher

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// procArgs2CountSize is the byte length of the argc word that opens a
// kern.procargs2 buffer.
const procArgs2CountSize = 4

// withTrueArgv returns entries with the Args of every session leader
// (PID == PGID) replaced by the argv argvFn reads for its PID. Only session
// leaders are read because only they can be a launcher-forked server (the
// launcher forks each server into a session of its own); every other row
// keeps its whitespace-split Args. A leader whose argv cannot be read — it
// exited, or it belongs to another user — keeps its ps-split Args as well, so
// the table degrades to the old matching rather than losing the row.
func withTrueArgv(entries []processEntry, argvFn func(pid int) ([]string, error)) []processEntry {
	merged := make([]processEntry, len(entries))
	for i, entry := range entries {
		merged[i] = entry
		if entry.PID != entry.PGID {
			continue
		}
		if argv, err := argvFn(entry.PID); err == nil && len(argv) > 0 {
			merged[i].Args = argv
		}
	}
	return merged
}

// parseProcArgs2 decodes a darwin kern.procargs2 buffer: a native-endian
// int32 argc, the executable path and its NUL padding, then argc
// NUL-terminated arguments (the environment follows and is ignored). It
// errors when the buffer is shorter than its argc claims.
func parseProcArgs2(buf []byte) ([]string, error) {
	if len(buf) < procArgs2CountSize {
		return nil, errors.New("kern.procargs2: buffer too short for argc")
	}
	argc := int(int32(binary.NativeEndian.Uint32(buf[:procArgs2CountSize])))
	if argc <= 0 {
		return nil, fmt.Errorf("kern.procargs2: argc %d", argc)
	}
	rest := buf[procArgs2CountSize:]

	// Skip the executable path, then the NUL padding that aligns argv[0].
	pathEnd := bytes.IndexByte(rest, 0)
	if pathEnd < 0 {
		return nil, errors.New("kern.procargs2: unterminated executable path")
	}
	rest = rest[pathEnd:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}

	argv := make([]string, 0, min(argc, len(rest)))
	for len(argv) < argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, fmt.Errorf("kern.procargs2: %d of %d arguments present", len(argv), argc)
		}
		argv = append(argv, string(rest[:end]))
		rest = rest[end+1:]
	}
	return argv, nil
}

// parseProcCmdline decodes a linux /proc/<pid>/cmdline file: the arguments,
// each terminated by a NUL. It errors on an empty file, which is what a
// kernel thread or a zombie reports.
func parseProcCmdline(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, errors.New("/proc cmdline: empty")
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), nil
}
