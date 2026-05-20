package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadBackendState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	backend := "testbackend"

	state := &ServerState{
		PID:           12345,
		Managed:       true,
		Backend:       backend,
		Host:          "127.0.0.1",
		Port:          8080,
		StartedAt:     time.Now().Truncate(time.Second),
		ActiveProfile: "test-profile",
		ActiveModel:   "/path/to/model.gguf",
		ContextSize:   4096,
	}

	statePath := filepath.Join(dir, "state-testbackend.json")
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
	if readState.Managed != state.Managed {
		t.Errorf("Managed = %v, want %v", readState.Managed, state.Managed)
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

func TestBackendStatePath(t *testing.T) {
	t.Parallel()

	path := backendStatePath("llamacpp")
	if !filepath.IsAbs(path) {
		t.Errorf("backendStatePath returned relative path: %q", path)
	}
	base := filepath.Base(path)
	if base != "state-llamacpp.json" {
		t.Errorf("base = %q, want state-llamacpp.json", base)
	}
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
