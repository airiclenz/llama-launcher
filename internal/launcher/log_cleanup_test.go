package launcher

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseLogTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		wantErr  bool
		wantTime time.Time
	}{
		{
			name:     "llamacpp log",
			filename: "llamacpp-20260521-150405.log",
			wantTime: time.Date(2026, 5, 21, 15, 4, 5, 0, time.UTC),
		},
		{
			name:     "ollama log",
			filename: "ollama-20260101-000000.log",
			wantTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "no extension",
			filename: "llamacpp-20260521-150405",
			wantErr:  true,
		},
		{
			name:     "too few parts",
			filename: "llamacpp.log",
			wantErr:  true,
		},
		{
			name:     "invalid timestamp",
			filename: "llamacpp-notadate-nottime.log",
			wantErr:  true,
		},
		{
			name:     "no log suffix",
			filename: "llamacpp-20260521-150405.txt",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseLogTimestamp(tt.filename)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.wantTime) {
				t.Errorf("got %v, want %v", got, tt.wantTime)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input int64
		want  string
	}{
		{0, "0B"},
		{500, "500B"},
		{1023, "1023B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048576, "1.0MB"},
		{1073741824, "1.0GB"},
		{2522702096, "2.3GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := formatBytes(tt.input); got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCleanupLogs_NonexistentDir(t *testing.T) {
	t.Parallel()

	result, err := cleanupLogs(nil, "/nonexistent/path", time.Hour, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Removed != 0 || result.Freed != 0 {
		t.Errorf("expected zero result, got %+v", result)
	}
}

func TestCleanupLogs_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	result, err := cleanupLogs(nil, dir, time.Hour, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Removed != 0 {
		t.Errorf("Removed = %d, want 0", result.Removed)
	}
}

func TestCleanupLogs_OldFilesRemoved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	oldFile := filepath.Join(dir, "llamacpp-20200101-000000.log")
	os.WriteFile(oldFile, []byte("old log data here"), 0o600)

	newTs := time.Now().Format(logTimestampFormat)
	newFile := filepath.Join(dir, "llamacpp-"+newTs+".log")
	os.WriteFile(newFile, []byte("new"), 0o600)

	maxAge := 24 * time.Hour
	result, err := cleanupLogs(nil, dir, maxAge, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Removed != 1 {
		t.Errorf("Removed = %d, want 1", result.Removed)
	}
	if result.Freed != 17 {
		t.Errorf("Freed = %d, want 17", result.Freed)
	}

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file should have been deleted")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Error("new file should still exist")
	}
}

func TestCleanupLogs_DeleteAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	newTs := time.Now().Format(logTimestampFormat)
	f1 := filepath.Join(dir, "llamacpp-20200101-000000.log")
	f2 := filepath.Join(dir, "ollama-"+newTs+".log")
	os.WriteFile(f1, []byte("aaa"), 0o600)
	os.WriteFile(f2, []byte("bbb"), 0o600)

	result, err := cleanupLogs(nil, dir, 0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Removed != 2 {
		t.Errorf("Removed = %d, want 2", result.Removed)
	}
}

func TestCleanupLogs_SkipsNonLogFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	txtFile := filepath.Join(dir, "notes.txt")
	os.WriteFile(txtFile, []byte("not a log"), 0o600)

	logFile := filepath.Join(dir, "llamacpp-20200101-000000.log")
	os.WriteFile(logFile, []byte("old"), 0o600)

	result, err := cleanupLogs(nil, dir, time.Hour, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Removed != 1 {
		t.Errorf("Removed = %d, want 1", result.Removed)
	}
	if _, err := os.Stat(txtFile); err != nil {
		t.Error("non-log file should still exist")
	}
}

// TestCleanupLogs_PreservesActiveLogFile guards the TDD §9.1 invariant:
// cleanup must always skip the log file of a running server, even in the
// worst case of a delete-all pass. The running server is a real httptest
// fake that passes the llamacpp health check, so the active log is resolved
// through the full discovery path (DiscoverRunningInstances +
// fillRuntimeDetails), exactly as in production.
func TestCleanupLogs_PreservesActiveLogFile(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	staleFile := filepath.Join(dir, "llamacpp-20200101-000000.log")
	os.WriteFile(staleFile, []byte("stale"), 0o600)

	// The active log must carry the newest timestamp so findManagedLogFile
	// resolves it as the running instance's log.
	activeTs := time.Now().Format(logTimestampFormat)
	activeFile := filepath.Join(dir, "llamacpp-"+activeTs+".log")
	os.WriteFile(activeFile, []byte("active"), 0o600)

	host, port := hostPort(t, srv.URL)
	backend := "llamacpp"
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:   dir,
		Profiles: map[string]Profile{},
	}
	cfg.Defaults = ProfileParams{Server: &backend, Host: &host, Port: &port}

	result, err := cleanupLogs(cfg, dir, 0, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Removed != 1 {
		t.Errorf("Removed = %d, want 1", result.Removed)
	}
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Error("stale file should have been deleted")
	}
	if _, err := os.Stat(activeFile); err != nil {
		t.Error("active file of the running server must be preserved")
	}
}

// TestAutoCleanupLogs_DisabledRetention pins the log_retention semantics:
// unset and 0 both mean cleanup disabled — nothing is deleted, no matter how
// old the files are.
func TestAutoCleanupLogs_DisabledRetention(t *testing.T) {
	t.Parallel()

	zero := 0
	tests := []struct {
		name      string
		retention *int
	}{
		{name: "unset retention", retention: nil},
		{name: "zero retention", retention: &zero},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			oldFile := filepath.Join(dir, "llamacpp-20200101-000000.log")
			os.WriteFile(oldFile, []byte("old"), 0o600)

			autoCleanupLogs(&Config{LogDir: dir, LogRetention: tt.retention})

			if _, err := os.Stat(oldFile); err != nil {
				t.Error("disabled retention must not delete any log file")
			}
		})
	}
}

// TestAutoCleanupLogs_PositiveRetention confirms the guard for disabled
// cleanup does not break the enabled path: a positive retention still
// removes stale files and keeps fresh ones.
func TestAutoCleanupLogs_PositiveRetention(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	staleFile := filepath.Join(dir, "llamacpp-20200101-000000.log")
	os.WriteFile(staleFile, []byte("stale"), 0o600)

	freshTs := time.Now().Format(logTimestampFormat)
	freshFile := filepath.Join(dir, "llamacpp-"+freshTs+".log")
	os.WriteFile(freshFile, []byte("fresh"), 0o600)

	seven := 7
	autoCleanupLogs(&Config{LogDir: dir, LogRetention: &seven})

	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Error("stale file should have been deleted")
	}
	if _, err := os.Stat(freshFile); err != nil {
		t.Error("fresh file should still exist")
	}
}

func TestCleanupLogs_SkipsDirectories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	subdir := filepath.Join(dir, "subdir.log")
	os.Mkdir(subdir, 0o700)

	result, err := cleanupLogs(nil, dir, time.Hour, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Removed != 0 {
		t.Errorf("Removed = %d, want 0", result.Removed)
	}
}
