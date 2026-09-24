package launcher

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	t.Parallel()

	// Fields 3..21 are placeholders; field 22 (starttime) is 4242.
	const tail = " S 1 1 1 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 4242 1000 100"
	tests := []struct {
		name    string
		stat    string
		want    int64
		wantErr bool
	}{
		{name: "plain comm", stat: "123 (sleep)" + tail, want: 4242},
		{name: "comm with spaces and parentheses", stat: "123 (evil) (name)" + tail, want: 4242},
		{name: "comm ending in a parenthesis", stat: "123 (x))" + tail, want: 4242},
		{name: "no closing parenthesis", stat: "123 (sleep", wantErr: true},
		{name: "truncated fields", stat: "123 (sleep) S 1 1", wantErr: true},
		{name: "non-numeric starttime", stat: "123 (sleep) S 1 1 1 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseProcStatStartTime([]byte(tt.stat + "\n"))

			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("parseProcStatStartTime = %d, %v; want %d, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// TestProcessIdentity checks the platform reader against real processes,
// which the parser's fixtures cannot.
func TestProcessIdentity(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no process identity reader on " + runtime.GOOS)
	}

	t.Run("stable for one process", func(t *testing.T) {
		t.Parallel()

		first, err1 := processIdentity(os.Getpid())
		second, err2 := processIdentity(os.Getpid())

		if err1 != nil || err2 != nil || first != second || first == 0 {
			t.Errorf("processIdentity(self) = %d, %v then %d, %v; want one stable non-zero value", first, err1, second, err2)
		}
	})

	t.Run("differs between processes started apart", func(t *testing.T) {
		t.Parallel()

		// Linux counts starttime in 10 ms clock ticks, so the children are
		// started more than one tick apart.
		first := startSleeper(t)
		time.Sleep(50 * time.Millisecond)
		second := startSleeper(t)

		a, errA := processIdentity(first)
		b, errB := processIdentity(second)

		if errA != nil || errB != nil || a == b {
			t.Errorf("identities = %d, %v and %d, %v; want two distinct values", a, errA, b, errB)
		}
	})

	t.Run("exited process has none", func(t *testing.T) {
		t.Parallel()
		cmd := exec.Command("true")
		if err := cmd.Run(); err != nil {
			t.Fatalf("running true: %v", err)
		}

		if _, err := processIdentity(cmd.Process.Pid); err == nil {
			t.Errorf("processIdentity(%d) of a reaped process succeeded, want an error", cmd.Process.Pid)
		}
	})
}

// startSleeper starts a `sleep 60` child, reaped as soon as it exits and
// killed when the test ends, and returns its PID.
func startSleeper(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	go cmd.Wait()
	t.Cleanup(func() { cmd.Process.Kill() })
	return cmd.Process.Pid
}
