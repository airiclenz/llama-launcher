package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandTilde(t *testing.T) {
	t.Parallel()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "tilde prefix", path: "~/Models", want: filepath.Join(home, "Models")},
		{name: "absolute path", path: "/usr/local/bin", want: "/usr/local/bin"},
		{name: "relative path", path: "relative/path", want: "relative/path"},
		{name: "empty string", path: "", want: ""},
		{name: "tilde only", path: "~", want: home},
		{name: "tilde username unchanged", path: "~bob/data", want: "~bob/data"},
		{name: "tilde in middle unchanged", path: "/path/~/file", want: "/path/~/file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExpandTilde(tt.path)
			if got != tt.want {
				t.Errorf("ExpandTilde(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestMergeParams(t *testing.T) {
	t.Parallel()

	intPtr := func(v int) *int { return &v }
	strPtr := func(v string) *string { return &v }
	boolPtr := func(v bool) *bool { return &v }
	floatPtr := func(v float64) *float64 { return &v }

	defaults := ProfileParams{
		GPULayers:   intPtr(99),
		Threads:     intPtr(8),
		ContextSize: intPtr(4096),
		Host:        strPtr("127.0.0.1"),
		Port:        intPtr(8080),
		FlashAttn:   boolPtr(true),
		Temperature: floatPtr(0.7),
	}

	t.Run("profile overrides specific fields", func(t *testing.T) {
		t.Parallel()
		profile := ProfileParams{
			ContextSize: intPtr(8192),
			Temperature: floatPtr(0.3),
		}

		merged := mergeParams(defaults, profile)

		if *merged.ContextSize != 8192 {
			t.Errorf("ContextSize = %d, want 8192", *merged.ContextSize)
		}
		if *merged.Temperature != 0.3 {
			t.Errorf("Temperature = %f, want 0.3", *merged.Temperature)
		}
		if *merged.GPULayers != 99 {
			t.Errorf("GPULayers = %d, want 99 (from defaults)", *merged.GPULayers)
		}
		if *merged.Threads != 8 {
			t.Errorf("Threads = %d, want 8 (from defaults)", *merged.Threads)
		}
	})

	t.Run("empty profile inherits all defaults", func(t *testing.T) {
		t.Parallel()
		merged := mergeParams(defaults, ProfileParams{})

		if *merged.GPULayers != 99 {
			t.Errorf("GPULayers = %d, want 99", *merged.GPULayers)
		}
		if *merged.Host != "127.0.0.1" {
			t.Errorf("Host = %q, want 127.0.0.1", *merged.Host)
		}
	})

	t.Run("nil defaults with profile values", func(t *testing.T) {
		t.Parallel()
		profile := ProfileParams{
			GPULayers: intPtr(50),
		}

		merged := mergeParams(ProfileParams{}, profile)

		if *merged.GPULayers != 50 {
			t.Errorf("GPULayers = %d, want 50", *merged.GPULayers)
		}
		if merged.Threads != nil {
			t.Errorf("Threads = %v, want nil", merged.Threads)
		}
	})
}

func TestApplyFallbacks(t *testing.T) {
	t.Parallel()

	t.Run("fills host and port when nil", func(t *testing.T) {
		t.Parallel()
		p := ProfileParams{}
		applyFallbacks(&p)

		if p.Host == nil || *p.Host != defaultHost {
			t.Errorf("Host = %v, want %q", p.Host, defaultHost)
		}
		if p.Port == nil || *p.Port != defaultPort {
			t.Errorf("Port = %v, want %d", p.Port, defaultPort)
		}
	})

	t.Run("preserves existing host and port", func(t *testing.T) {
		t.Parallel()
		host := "0.0.0.0"
		port := 9090
		p := ProfileParams{Host: &host, Port: &port}
		applyFallbacks(&p)

		if *p.Host != "0.0.0.0" {
			t.Errorf("Host = %q, want 0.0.0.0", *p.Host)
		}
		if *p.Port != 9090 {
			t.Errorf("Port = %d, want 9090", *p.Port)
		}
	})
}

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	t.Run("missing file returns ErrConfigNotFound", func(t *testing.T) {
		t.Parallel()
		_, err := LoadConfig("/nonexistent/path/config.yaml")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !isConfigNotFoundError(err) {
			t.Errorf("expected ErrConfigNotFound, got: %v", err)
		}
	})

	t.Run("valid config loads successfully", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")

		yaml := `
servers:
  llamacpp: true
models_dir: /tmp/models
profiles:
  test-profile:
    description: "Test"
    model: test.gguf
`
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
			t.Fatalf("writing config: %v", err)
		}

		cfg, err := LoadConfig(cfgPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		if len(cfg.Profiles) != 1 {
			t.Errorf("Profiles count = %d, want 1", len(cfg.Profiles))
		}
		if cfg.Defaults.Server == nil || *cfg.Defaults.Server != "llamacpp" {
			t.Errorf("Defaults.Server = %v, want llamacpp", cfg.Defaults.Server)
		}
	})

	t.Run("config with no profiles fails validation", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")

		yaml := `
servers:
  llamacpp: true
profiles: {}
`
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
			t.Fatalf("writing config: %v", err)
		}

		_, err := LoadConfig(cfgPath)
		if err == nil {
			t.Fatal("expected validation error, got nil")
		}
	})
}

func TestProfileNames(t *testing.T) {
	t.Parallel()

	server := "llamacpp"
	cfg := &Config{
		Servers:  map[string]bool{"llamacpp": true},
		Defaults: ProfileParams{Server: &server},
		Profiles: map[string]Profile{
			"charlie": {Description: "C"},
			"alpha":   {Description: "A"},
			"bravo":   {Description: "B"},
		},
	}

	names := cfg.ProfileNames()
	want := []string{"alpha", "bravo", "charlie"}

	if len(names) != len(want) {
		t.Fatalf("len = %d, want %d", len(names), len(want))
	}
	for i, name := range names {
		if name != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, name, want[i])
		}
	}
}

func TestResolveProfile(t *testing.T) {
	t.Parallel()

	t.Run("sets ModelRef to original model string", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		modelRef := "subdir/model.gguf"
		subdir := filepath.Join(dir, "subdir")
		if err := os.MkdirAll(subdir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, modelRef), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}

		strPtr := func(v string) *string { return &v }
		cfg := &Config{
			Servers:  map[string]bool{"llamacpp": true},
			ModelsDir: dir,
			Defaults: ProfileParams{Server: strPtr("llamacpp")},
			Profiles: map[string]Profile{
				"test": {Description: "test", Model: modelRef},
			},
		}

		rp, err := cfg.ResolveProfile("test")
		if err != nil {
			t.Fatalf("ResolveProfile: %v", err)
		}

		if rp.ModelRef != modelRef {
			t.Errorf("ModelRef = %q, want %q", rp.ModelRef, modelRef)
		}
		if rp.ModelPath != filepath.Join(dir, modelRef) {
			t.Errorf("ModelPath = %q, want %q", rp.ModelPath, filepath.Join(dir, modelRef))
		}
	})

	t.Run("unknown profile returns error", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{
			Servers:  map[string]bool{"llamacpp": true},
			Defaults: ProfileParams{Server: func() *string { s := "llamacpp"; return &s }()},
			Profiles: map[string]Profile{},
		}

		_, err := cfg.ResolveProfile("nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown profile")
		}
	})
}

func TestGenerateExampleConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.yaml")

	if err := GenerateExampleConfig(path); err != nil {
		t.Fatalf("GenerateExampleConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading generated config: %v", err)
	}

	if len(data) == 0 {
		t.Error("generated config is empty")
	}
}

func TestValidate_DeprecatedDefaultBackend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
default_backend: llamacpp
servers:
  llamacpp: true
profiles:
  test:
    model: test.gguf
`
	os.WriteFile(cfgPath, []byte(yaml), 0o644)

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for deprecated default_backend")
	}
	if !strings.Contains(err.Error(), "default_backend") {
		t.Errorf("error = %q, want it to mention default_backend", err)
	}
}

func TestValidate_DeprecatedEndpoints(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  llamacpp: true
endpoints:
  llamacpp: "localhost:8080"
profiles:
  test:
    model: test.gguf
`
	os.WriteFile(cfgPath, []byte(yaml), 0o644)

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for deprecated endpoints")
	}
	if !strings.Contains(err.Error(), "endpoints") {
		t.Errorf("error = %q, want it to mention endpoints", err)
	}
}

func TestValidate_NoServersEnabled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  llamacpp: false
profiles:
  test:
    model: test.gguf
`
	os.WriteFile(cfgPath, []byte(yaml), 0o644)

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected validation error for no servers enabled")
	}
	if !strings.Contains(err.Error(), "no servers enabled") {
		t.Errorf("error = %q, want it to mention 'no servers enabled'", err)
	}
}

func TestValidate_AutoAssignDefaultServer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  llamacpp: true
profiles:
  test:
    model: test.gguf
`
	os.WriteFile(cfgPath, []byte(yaml), 0o644)

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Defaults.Server == nil || *cfg.Defaults.Server != "llamacpp" {
		t.Errorf("Defaults.Server = %v, want llamacpp", cfg.Defaults.Server)
	}
}

func TestConfiguredBackendAddr(t *testing.T) {
	t.Parallel()

	server := "llamacpp"
	cfg := &Config{
		Servers:  map[string]bool{"llamacpp": true},
		Defaults: ProfileParams{Server: &server},
	}

	addr := cfg.ConfiguredBackendAddr("llamacpp")
	if addr == "" {
		t.Fatal("expected non-empty address")
	}
	if !strings.Contains(addr, ":") {
		t.Errorf("address %q should contain a colon", addr)
	}
}

func TestShouldAutoClose(t *testing.T) {
	t.Parallel()

	t.Run("nil defaults to true", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		if !cfg.ShouldAutoClose() {
			t.Error("ShouldAutoClose() = false, want true when nil")
		}
	})

	t.Run("explicit false", func(t *testing.T) {
		t.Parallel()
		f := false
		cfg := &Config{AutoClose: &f}
		if cfg.ShouldAutoClose() {
			t.Error("ShouldAutoClose() = true, want false")
		}
	})
}

func TestShouldDisplayCentered(t *testing.T) {
	t.Parallel()

	t.Run("nil defaults to false", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		if cfg.ShouldDisplayCentered() {
			t.Error("ShouldDisplayCentered() = true, want false when nil")
		}
	})

	t.Run("explicit true", func(t *testing.T) {
		t.Parallel()
		tr := true
		cfg := &Config{DisplayCentered: &tr}
		if !cfg.ShouldDisplayCentered() {
			t.Error("ShouldDisplayCentered() = false, want true")
		}
	})
}

func isConfigNotFoundError(err error) bool {
	for err != nil {
		if err.Error() == "config file not found" {
			return true
		}
		unwrapped := errors.Unwrap(err)
		if unwrapped == err {
			break
		}
		err = unwrapped
	}
	return false
}
