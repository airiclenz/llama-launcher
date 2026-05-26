package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadInstanceState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	backend := "testbackend"

	state := &ServerState{
		PID:           12345,
		Backend:       backend,
		Host:          "127.0.0.1",
		Port:          8080,
		StartedAt:     time.Now().Truncate(time.Second),
		ActiveProfile: "test-profile",
		ActiveModel:   "/path/to/model.gguf",
		ContextSize:   4096,
	}

	statePath := filepath.Join(dir, "state-testbackend-8080.json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshaling state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatalf("writing state: %v", err)
	}

	readData, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}

	var readState ServerState
	if err := json.Unmarshal(readData, &readState); err != nil {
		t.Fatalf("unmarshaling state: %v", err)
	}

	if readState.PID != state.PID {
		t.Errorf("PID = %d, want %d", readState.PID, state.PID)
	}
	if readState.Backend != state.Backend {
		t.Errorf("Backend = %q, want %q", readState.Backend, state.Backend)
	}
	if readState.ActiveProfile != state.ActiveProfile {
		t.Errorf("ActiveProfile = %q, want %q", readState.ActiveProfile, state.ActiveProfile)
	}
}

func TestServerState_Addr(t *testing.T) {
	t.Parallel()

	state := &ServerState{Host: "127.0.0.1", Port: 8080}
	if got := state.Addr(); got != "127.0.0.1:8080" {
		t.Errorf("Addr() = %q, want %q", got, "127.0.0.1:8080")
	}
}

func TestServerState_Uptime(t *testing.T) {
	t.Parallel()

	state := &ServerState{StartedAt: time.Now().Add(-5 * time.Second)}
	uptime := state.Uptime()
	if uptime < 4*time.Second || uptime > 6*time.Second {
		t.Errorf("Uptime() = %v, want ~5s", uptime)
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

func TestInstanceStatePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		backend  string
		host     string
		port     int
		wantBase string
	}{
		{
			name:     "loopback host is omitted",
			backend:  "llamacpp",
			host:     "127.0.0.1",
			port:     8080,
			wantBase: "state-llamacpp-8080.json",
		},
		{
			name:     "empty host treated as loopback",
			backend:  "ollama",
			host:     "",
			port:     11434,
			wantBase: "state-ollama-11434.json",
		},
		{
			name:     "non-loopback host included",
			backend:  "llamacpp",
			host:     "192.168.1.50",
			port:     8080,
			wantBase: "state-llamacpp-192.168.1.50-8080.json",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := instanceStatePath(tc.backend, tc.host, tc.port)
			if !filepath.IsAbs(path) {
				t.Errorf("instanceStatePath returned relative path: %q", path)
			}
			if base := filepath.Base(path); base != tc.wantBase {
				t.Errorf("base = %q, want %q", base, tc.wantBase)
			}
		})
	}
}

func TestShouldCrossServerUnload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		state          *ServerState
		targetBackend  string
		want           bool
	}{
		{
			name:          "nil state",
			state:         nil,
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "same backend as target",
			state:         &ServerState{Backend: "ollama", ActiveModel: "llama3.1:8b"},
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "no model loaded",
			state:         &ServerState{Backend: "ollama", ActiveModel: ""},
			targetBackend: "lmstudio",
			want:          false,
		},
		{
			name:          "managed backend is skipped",
			state:         &ServerState{Backend: "llamacpp", ActiveModel: "/models/foo.gguf"},
			targetBackend: "ollama",
			want:          false,
		},
		{
			name:          "external backend with model loaded",
			state:         &ServerState{Backend: "ollama", ActiveModel: "llama3.1:8b"},
			targetBackend: "lmstudio",
			want:          true,
		},
		{
			name:          "unknown backend",
			state:         &ServerState{Backend: "doesnotexist", ActiveModel: "x"},
			targetBackend: "ollama",
			want:          false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldCrossServerUnload(tc.state, tc.targetBackend); got != tc.want {
				t.Errorf("shouldCrossServerUnload = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStateMigration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Legacy files that should be removed.
	legacy := []string{
		"state.json",
		"state-llamacpp.json",
		"state-ollama.json",
	}
	for _, name := range legacy {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Per-instance files that must be preserved.
	keep := []string{
		"state-llamacpp-8080.json",
		"state-llamacpp-192.168.1.50-8081.json",
	}
	for _, name := range keep {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removeLegacyStateFiles(dir)

	for _, name := range legacy {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("legacy file %s should be removed", name)
		}
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("per-instance file %s should be preserved: %v", name, err)
		}
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

func TestWriteBackendState_Permissions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state-test")
	statePath := filepath.Join(stateDir, "state-testbackend.json")

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}

	state := &ServerState{
		Backend: "testbackend",
		Host:    "127.0.0.1",
		Port:    8080,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file permissions = %o, want 600", perm)
	}
}
