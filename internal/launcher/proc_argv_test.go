package launcher

import (
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// spacedSplashArgv is the argv of a loading Splash installed under a path
// with a space in it: ps prints it joined by spaces, so only the true argv
// keeps each path whole.
var spacedSplashArgv = []string{
	"/opt/My Splash/.venv/bin/python",
	"/opt/My Splash/server/server.py",
	"--binary", "/opt/My Splash/splash",
	"--model", "m",
	"--port", "1234",
}

// procArgs2Buffer builds a kern.procargs2 buffer the way darwin lays it out:
// argc, the executable path, NUL padding, the arguments, then the
// environment.
func procArgs2Buffer(t *testing.T, argc int, execPath string, args, env []string) []byte {
	t.Helper()
	buf := binary.NativeEndian.AppendUint32(nil, uint32(argc))
	buf = append(buf, execPath...)
	buf = append(buf, 0, 0, 0, 0)
	for _, s := range append(slices.Clone(args), env...) {
		buf = append(buf, s...)
		buf = append(buf, 0)
	}
	return buf
}

func TestParseProcArgs2(t *testing.T) {
	t.Parallel()

	const execPath = "/opt/My Splash/.venv/bin/python"
	env := []string{"HOME=/Users/u", "PATH=/usr/bin"}
	full := procArgs2Buffer(t, len(spacedSplashArgv), execPath, spacedSplashArgv, env)

	t.Run("keeps spaced arguments whole and drops the environment", func(t *testing.T) {
		t.Parallel()
		got, err := parseProcArgs2(full)
		if err != nil || !slices.Equal(got, spacedSplashArgv) {
			t.Errorf("parseProcArgs2 = %q, %v; want %q", got, err, spacedSplashArgv)
		}
	})

	failures := map[string][]byte{
		"shorter than argc":         full[:2],
		"zero argc":                 procArgs2Buffer(t, 0, execPath, nil, nil),
		"unterminated exec path":    append(binary.NativeEndian.AppendUint32(nil, 1), "/bin/sh"...),
		"fewer arguments than argc": procArgs2Buffer(t, 3, execPath, []string{"a", "b"}, nil),
	}
	for name, buf := range failures {
		t.Run("errors on "+name, func(t *testing.T) {
			t.Parallel()
			if got, err := parseProcArgs2(buf); err == nil {
				t.Errorf("parseProcArgs2 = %q, want an error", got)
			}
		})
	}
}

func TestParseProcCmdline(t *testing.T) {
	t.Parallel()

	t.Run("keeps spaced arguments whole", func(t *testing.T) {
		t.Parallel()
		data := []byte(strings.Join(spacedSplashArgv, "\x00") + "\x00")
		got, err := parseProcCmdline(data)
		if err != nil || !slices.Equal(got, spacedSplashArgv) {
			t.Errorf("parseProcCmdline = %q, %v; want %q", got, err, spacedSplashArgv)
		}
	})

	t.Run("errors on an empty file", func(t *testing.T) {
		t.Parallel()
		if got, err := parseProcCmdline(nil); err == nil {
			t.Errorf("parseProcCmdline = %q, want an error", got)
		}
	})
}

// TestWithTrueArgv checks that a loading Splash under a spaced install path,
// as ps prints it, is found only once the true argv replaces the
// whitespace-split row (ADR-0015).
func TestWithTrueArgv(t *testing.T) {
	t.Parallel()

	const addr, defaultAddr = "127.0.0.1:1234", "127.0.0.1:8000"
	psOut := "4242 4242 " + strings.Join(spacedSplashArgv, " ") + "\n" +
		"4243 4242 /opt/My Splash/splash --model m\n"
	psRows := parseProcessTable(psOut)
	trueArgv := func(pid int) ([]string, error) {
		if pid == 4242 {
			return spacedSplashArgv, nil
		}
		return nil, errors.New("not a leader")
	}
	unreadable := func(int) ([]string, error) { return nil, errors.New("no such process") }

	t.Run("the ps-split rows do not match", func(t *testing.T) {
		t.Parallel()
		if got := splashLoadingPID(psRows, addr, defaultAddr); got != 0 {
			t.Errorf("splashLoadingPID(ps rows) = %d, want 0", got)
		}
	})

	t.Run("the merged rows match the leader", func(t *testing.T) {
		t.Parallel()
		if got := splashLoadingPID(withTrueArgv(psRows, trueArgv), addr, defaultAddr); got != 4242 {
			t.Errorf("splashLoadingPID(merged rows) = %d, want 4242", got)
		}
	})

	t.Run("only session leaders are read", func(t *testing.T) {
		t.Parallel()
		var read []int
		spy := func(pid int) ([]string, error) {
			read = append(read, pid)
			return trueArgv(pid)
		}
		merged := withTrueArgv(psRows, spy)
		if !slices.Equal(read, []int{4242}) || !slices.Equal(merged[1].Args, psRows[1].Args) {
			t.Errorf("read PIDs %v, non-leader Args %q; want [4242] and %q", read, merged[1].Args, psRows[1].Args)
		}
	})

	t.Run("an unreadable leader keeps its ps-split Args", func(t *testing.T) {
		t.Parallel()
		merged := withTrueArgv(psRows, unreadable)
		if !slices.Equal(merged[0].Args, psRows[0].Args) {
			t.Errorf("Args = %q, want %q", merged[0].Args, psRows[0].Args)
		}
	})
}

// TestProcArgvReadsOwnArgv checks the platform reader against the kernel's
// own record of this test binary's argv, which the parsers' fixtures cannot.
func TestProcArgvReadsOwnArgv(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no argv reader on " + runtime.GOOS)
	}

	got, err := procArgv(os.Getpid())

	if err != nil || !slices.Equal(got, os.Args) {
		t.Errorf("procArgv(self) = %q, %v; want %q", got, err, os.Args)
	}
}
