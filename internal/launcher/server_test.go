package launcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunningInstance_Addr(t *testing.T) {
	t.Parallel()

	inst := &RunningInstance{Host: "127.0.0.1", Port: 8080}
	if got := inst.Addr(); got != "127.0.0.1:8080" {
		t.Errorf("Addr() = %q, want %q", got, "127.0.0.1:8080")
	}
}

func TestRunningInstance_Uptime(t *testing.T) {
	t.Parallel()

	inst := &RunningInstance{StartedAt: time.Now().Add(-5 * time.Second)}
	uptime := inst.Uptime()
	if uptime < 4*time.Second || uptime > 6*time.Second {
		t.Errorf("Uptime() = %v, want ~5s", uptime)
	}
}

func TestRunningInstance_Uptime_ZeroStart(t *testing.T) {
	t.Parallel()

	inst := &RunningInstance{}
	if uptime := inst.Uptime(); uptime != 0 {
		t.Errorf("Uptime() = %v, want 0 when StartedAt is zero", uptime)
	}
}

func TestIsProcessAlive_CurrentPID(t *testing.T) {
	t.Parallel()

	if !IsProcessAlive(os.Getpid()) {
		t.Error("IsProcessAlive(os.Getpid()) = false, want true")
	}
}

func TestIsProcessAlive_ZeroPID(t *testing.T) {
	t.Parallel()

	if IsProcessAlive(0) {
		t.Error("IsProcessAlive(0) = true, want false")
	}
}

func TestIsProcessAlive_NegativePID(t *testing.T) {
	t.Parallel()

	if IsProcessAlive(-1) {
		t.Error("IsProcessAlive(-1) = true, want false")
	}
}

func TestIsProcessAlive_InvalidPID(t *testing.T) {
	t.Parallel()

	if IsProcessAlive(99999999) {
		t.Error("IsProcessAlive(99999999) = true, want false")
	}
}

func TestReadLastLines(t *testing.T) {
	t.Parallel()

	t.Run("more lines than requested", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		content := "line1\nline2\nline3\nline4\nline5\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got := readLastLines(path, 3)
		want := "line3\nline4\nline5"
		if got != want {
			t.Errorf("readLastLines = %q, want %q", got, want)
		}
	})

	t.Run("fewer lines than requested", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		content := "line1\nline2\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got := readLastLines(path, 10)
		want := "line1\nline2"
		if got != want {
			t.Errorf("readLastLines = %q, want %q", got, want)
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		t.Parallel()
		got := readLastLines("/nonexistent/path", 5)
		if got != "(could not read log)" {
			t.Errorf("readLastLines = %q, want fallback message", got)
		}
	})
}

func TestShouldCrossServerUnload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		inst          *RunningInstance
		targetBackend string
		want          bool
	}{
		{
			name:          "nil instance",
			inst:          nil,
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "same backend as target",
			inst:          &RunningInstance{Backend: "ollama", ActiveModel: "llama3.1:8b"},
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "no model loaded",
			inst:          &RunningInstance{Backend: "ollama", ActiveModel: ""},
			targetBackend: "lmstudio",
			want:          false,
		},
		{
			name:          "managed backend is skipped",
			inst:          &RunningInstance{Backend: "llamacpp", ActiveModel: "/models/foo.gguf"},
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "external backend with model loaded",
			inst:          &RunningInstance{Backend: "ollama", ActiveModel: "llama3.1:8b"},
			targetBackend: "lmstudio",
			want:          true,
		},
		{
			name:          "unknown backend",
			inst:          &RunningInstance{Backend: "doesnotexist", ActiveModel: "x"},
			targetBackend: "ollama",
			want:          false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldCrossServerUnload(tc.inst, tc.targetBackend); got != tc.want {
				t.Errorf("shouldCrossServerUnload = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParamDrift(t *testing.T) {
	t.Parallel()

	intPtr := func(n int) *int { return &n }
	boolPtr := func(b bool) *bool { return &b }
	floatPtr := func(f float64) *float64 { return &f }

	t.Run("identical params produce no drift", func(t *testing.T) {
		t.Parallel()
		p := ProfileParams{ContextSize: intPtr(8192), Temperature: floatPtr(0.7), FlashAttn: boolPtr(true)}
		if d := paramDrift(p, p); len(d) != 0 {
			t.Errorf("want no drift, got %v", d)
		}
	})

	t.Run("nil-vs-nil fields are ignored", func(t *testing.T) {
		t.Parallel()
		a := ProfileParams{ContextSize: intPtr(8192)}
		b := ProfileParams{ContextSize: intPtr(8192)}
		if d := paramDrift(a, b); len(d) != 0 {
			t.Errorf("want no drift for nil-vs-nil siblings, got %v", d)
		}
	})

	t.Run("changed int field appears in drift", func(t *testing.T) {
		t.Parallel()
		a := ProfileParams{ContextSize: intPtr(8192)}
		b := ProfileParams{ContextSize: intPtr(16384)}
		d := paramDrift(a, b)
		if len(d) != 1 || d[0] != "context_size: 8192 → 16384" {
			t.Errorf("unexpected drift: %v", d)
		}
	})

	t.Run("set-vs-unset is reported", func(t *testing.T) {
		t.Parallel()
		a := ProfileParams{ContextSize: intPtr(8192)}
		b := ProfileParams{}
		d := paramDrift(a, b)
		if len(d) != 1 || d[0] != "context_size: 8192 → (unset)" {
			t.Errorf("unexpected drift: %v", d)
		}
	})

	t.Run("bool and float fields are compared", func(t *testing.T) {
		t.Parallel()
		a := ProfileParams{FlashAttn: boolPtr(true), Temperature: floatPtr(0.7)}
		b := ProfileParams{FlashAttn: boolPtr(false), Temperature: floatPtr(0.3)}
		d := paramDrift(a, b)
		if len(d) != 2 {
			t.Fatalf("want 2 drifts, got %d: %v", len(d), d)
		}
	})

	t.Run("slot identity fields are not compared", func(t *testing.T) {
		t.Parallel()
		host := "127.0.0.1"
		host2 := "192.168.0.1"
		port1 := 8080
		port2 := 8081
		server1 := "llamacpp"
		server2 := "ollama"
		a := ProfileParams{Host: &host, Port: &port1, Server: &server1}
		b := ProfileParams{Host: &host2, Port: &port2, Server: &server2}
		if d := paramDrift(a, b); len(d) != 0 {
			t.Errorf("slot identity should not produce drift, got %v", d)
		}
	})
}
