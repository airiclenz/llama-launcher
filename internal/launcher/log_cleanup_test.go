package launcher

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseLogName(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 5, 21, 15, 4, 5, 0, time.UTC)
	millis := time.Date(2026, 5, 21, 15, 4, 5, 123_000_000, time.UTC)
	tests := []struct {
		name     string
		filename string
		want     logName
		wantOK   bool
	}{
		{
			name:     "legacy second stamp",
			filename: "llamacpp-20260521-150405.log",
			want:     logName{backend: "llamacpp", started: stamp},
			wantOK:   true,
		},
		{
			name:     "legacy millisecond stamp",
			filename: "llamacpp-20260521-150405.123.log",
			want:     logName{backend: "llamacpp", started: millis},
			wantOK:   true,
		},
		{
			name:     "port, millisecond stamp",
			filename: "ollama-11434-20260521-150405.123.log",
			want:     logName{backend: "ollama", port: 11434, started: millis},
			wantOK:   true,
		},
		{
			name:     "port, second stamp",
			filename: "ollama-11434-20260521-150405.log",
			want:     logName{backend: "ollama", port: 11434, started: stamp},
			wantOK:   true,
		},
		{
			name:     "port and Profile, millisecond stamp",
			filename: "llamacpp-8080-qwen3-20260521-150405.123.log",
			want:     logName{backend: "llamacpp", port: 8080, profile: "qwen3", started: millis},
			wantOK:   true,
		},
		{
			name:     "port and Profile, second stamp",
			filename: "splash-8000-coder-20260521-150405.log",
			want:     logName{backend: "splash", port: 8000, profile: "coder", started: stamp},
			wantOK:   true,
		},
		{
			name:     "Profile with dashes",
			filename: "llamacpp-8080-qwen3-coder-30b-20260521-150405.123.log",
			want:     logName{backend: "llamacpp", port: 8080, profile: "qwen3-coder-30b", started: millis},
			wantOK:   true,
		},
		{name: "non-numeric port", filename: "llamacpp-main-20260521-150405.123.log"},
		{name: "zero port", filename: "llamacpp-0-20260521-150405.123.log"},
		{name: "malformed millisecond stamp", filename: "llamacpp-20260521-150405.12x.log"},
		{name: "no extension", filename: "llamacpp-20260521-150405"},
		{name: "too few parts", filename: "llamacpp.log"},
		{name: "no backend", filename: "-20260521-150405.log"},
		{name: "invalid timestamp", filename: "llamacpp-notadate-nottime.log"},
		{name: "not a log file", filename: "llamacpp-20260521-150405.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseLogName(tt.filename)

			if ok != tt.wantOK {
				t.Fatalf("parseLogName(%q) ok = %v, want %v", tt.filename, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("parseLogName(%q) = %+v, want %+v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestLogName_FileNameRoundTrips(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 5, 21, 15, 4, 5, 42_000_000, time.UTC)
	tests := []struct {
		name string
		log  logName
		want string
	}{
		{
			name: "port and Profile",
			log:  logName{backend: "llamacpp", port: 8080, profile: "qwen3-coder", started: started},
			want: "llamacpp-8080-qwen3-coder-20260521-150405.042.log",
		},
		{
			name: "port only",
			log:  logName{backend: "ollama", port: 11434, started: started},
			want: "ollama-11434-20260521-150405.042.log",
		},
		{
			name: "no port is the legacy shape",
			log:  logName{backend: "ollama", profile: "ignored", started: started},
			want: "ollama-20260521-150405.042.log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.log.fileName()
			parsed, ok := parseLogName(got)

			if got != tt.want {
				t.Errorf("fileName() = %q, want %q", got, tt.want)
			}
			if !ok || parsed.port != tt.log.port || !parsed.started.Equal(started) {
				t.Errorf("parseLogName(%q) = %+v, %v; want port %d, start %v", got, parsed, ok, tt.log.port, started)
			}
		})
	}
}

func TestLogProfileSlug(t *testing.T) {
	t.Parallel()

	tests := []struct {
		profileName string
		want        string
	}{
		{"qwen3", "qwen3"},
		{"Qwen3-Coder_30B.q4", "Qwen3-Coder_30B.q4"},
		{"my profile", "my_profile"},
		{"../etc/passwd", ".._etc_passwd"},
		{"a:b*c?", "a_b_c_"},
		{"café", "caf_"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.profileName, func(t *testing.T) {
			t.Parallel()

			if got := logProfileSlug(tt.profileName); got != tt.want {
				t.Errorf("logProfileSlug(%q) = %q, want %q", tt.profileName, got, tt.want)
			}
		})
	}
}

// TestCleanupLogs_AgesEveryShape pins that cleanup ages Profile-named,
// port-named and legacy logs alike: old ones go, fresh ones stay.
func TestCleanupLogs_AgesEveryShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Now()
	shapes := []logName{
		{backend: "llamacpp", port: 8080, profile: "qwen3-coder"},
		{backend: "ollama", port: 11434},
		{backend: "llamacpp"},
	}
	var oldPaths, freshPaths []string
	for _, shape := range shapes {
		for _, started := range []time.Time{old, fresh} {
			shape.started = started
			path := filepath.Join(dir, shape.fileName())
			if err := os.WriteFile(path, []byte("log"), 0o600); err != nil {
				t.Fatal(err)
			}
			if started.Equal(old) {
				oldPaths = append(oldPaths, path)
			} else {
				freshPaths = append(freshPaths, path)
			}
		}
	}

	result, err := cleanupLogs(nil, dir, 7*24*time.Hour, false)

	if err != nil {
		t.Fatalf("cleanupLogs: %v", err)
	}
	if result.Removed != len(oldPaths) {
		t.Errorf("Removed = %d, want %d", result.Removed, len(oldPaths))
	}
	for _, path := range oldPaths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("old log %q was kept", path)
		}
	}
	for _, path := range freshPaths {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("fresh log %q was removed: %v", path, err)
		}
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

func TestCleanupLogs_AgesMillisecondNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	oldMillis := filepath.Join(dir, "llamacpp-20200101-000000.250.log")
	oldLegacy := filepath.Join(dir, "ollama-20200101-000000.log")
	freshMillis := filepath.Join(dir, "llamacpp-"+time.Now().Format(logTimestampFormat)+".log")
	for _, p := range []string{oldMillis, oldLegacy, freshMillis} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	result, err := cleanupLogs(nil, dir, 24*time.Hour, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Removed != 2 {
		t.Errorf("Removed = %d, want 2", result.Removed)
	}
	for _, p := range []string{oldMillis, oldLegacy} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("old log %q should have been deleted", p)
		}
	}
	if _, err := os.Stat(freshMillis); err != nil {
		t.Errorf("fresh log %q should still exist", freshMillis)
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

// TestAutoCleanupLogs_ProfileRetention pins per-Profile log_retention: a log
// whose name carries a Profile's slug ages against that Profile's override, a
// Profile without one defers to the global value, every other log (another
// Profile's, ollama serve's, legacy) ages against the global value, and a
// retention that is not positive keeps the logs it governs.
func TestAutoCleanupLogs_ProfileRetention(t *testing.T) {
	t.Parallel()

	zero, one, seven, thirty := 0, 1, 7, 30
	tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour).Format(logTimestampFormat)
	const (
		coderLog  = "llamacpp-8080-coder-"
		chatLog   = "llamacpp-8080-chat-"
		serveLog  = "ollama-11434-"
		legacyLog = "llamacpp-"
	)

	tests := []struct {
		name        string
		global      *int
		profiles    map[string]Profile
		wantKept    []string
		wantRemoved []string
	}{
		{
			name:        "Profile override outlives the global value",
			global:      &seven,
			profiles:    map[string]Profile{"coder": {LogRetention: &thirty}, "chat": {}},
			wantKept:    []string{coderLog},
			wantRemoved: []string{chatLog, serveLog, legacyLog},
		},
		{
			name:        "Profile at 0 keeps its logs",
			global:      &seven,
			profiles:    map[string]Profile{"coder": {LogRetention: &zero}},
			wantKept:    []string{coderLog},
			wantRemoved: []string{chatLog, serveLog, legacyLog},
		},
		{
			name:        "Profile without an override defers to the global value",
			global:      &seven,
			profiles:    map[string]Profile{"coder": {}},
			wantRemoved: []string{coderLog, chatLog, serveLog, legacyLog},
		},
		{
			name:        "global unset with a Profile at 1 removes only that Profile's logs",
			global:      nil,
			profiles:    map[string]Profile{"coder": {LogRetention: &one}, "chat": {}},
			wantKept:    []string{chatLog, serveLog, legacyLog},
			wantRemoved: []string{coderLog},
		},
		{
			name:        "shared slug: the longest retention wins",
			global:      &seven,
			profiles:    map[string]Profile{"coder": {LogRetention: &one}, "coder ": {LogRetention: &thirty}, "coder_": {}},
			wantKept:    []string{"llamacpp-8080-coder_-"},
			wantRemoved: []string{coderLog},
		},
		{
			name:        "shared slug: a keep wins over every retention",
			global:      &seven,
			profiles:    map[string]Profile{"coder ": {LogRetention: &one}, "coder_": {LogRetention: &zero}},
			wantKept:    []string{"llamacpp-8080-coder_-"},
			wantRemoved: []string{coderLog},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := func(prefix string) string {
				return filepath.Join(dir, prefix+tenDaysAgo+logFileSuffix)
			}
			for _, prefix := range append(append([]string{}, tt.wantKept...), tt.wantRemoved...) {
				if err := os.WriteFile(path(prefix), []byte("log"), 0o600); err != nil {
					t.Fatalf("writing %s: %v", prefix, err)
				}
			}

			autoCleanupLogs(&Config{LogDir: dir, LogRetention: tt.global, Profiles: tt.profiles})

			for _, prefix := range tt.wantKept {
				if _, err := os.Stat(path(prefix)); err != nil {
					t.Errorf("%s log should have been kept", prefix)
				}
			}
			for _, prefix := range tt.wantRemoved {
				if _, err := os.Stat(path(prefix)); !os.IsNotExist(err) {
					t.Errorf("%s log should have been removed", prefix)
				}
			}
		})
	}
}

// TestAutoCleanupLogs_ProfileRetentionPreservesActiveLogFile guards the TDD
// §9.1 invariant on the per-Profile path: an expired log of a Profile with a
// positive override is still kept while its server runs.
func TestAutoCleanupLogs_ProfileRetentionPreservesActiveLogFile(t *testing.T) {
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

	host, port := hostPort(t, srv.URL)
	dir := t.TempDir()
	tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour).Format(logTimestampFormat)
	activeFile := filepath.Join(dir, fmt.Sprintf("llamacpp-%d-coder-%s.log", port, tenDaysAgo))
	staleFile := filepath.Join(dir, fmt.Sprintf("llamacpp-%d-coder-%s.log", port+1, tenDaysAgo))
	os.WriteFile(activeFile, []byte("active"), 0o600)
	os.WriteFile(staleFile, []byte("stale"), 0o600)

	backend := "llamacpp"
	one := 1
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:   dir,
		Defaults: ProfileParams{Server: &backend, Host: &host, Port: &port},
		Profiles: map[string]Profile{"coder": {Model: "coder.gguf", LogRetention: &one}},
	}

	autoCleanupLogs(cfg)

	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Error("stale log of the Profile should have been removed")
	}
	if _, err := os.Stat(activeFile); err != nil {
		t.Error("active log of the running server must be preserved")
	}
}
