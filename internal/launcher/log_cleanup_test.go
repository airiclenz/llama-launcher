package launcher

import (
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
