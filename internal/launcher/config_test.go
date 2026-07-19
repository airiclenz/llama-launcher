package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
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

func TestProfileNames_FavouritesFirst(t *testing.T) {
	t.Parallel()

	defaultServer := "llamacpp"
	otherServer := "ollama"
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}, "ollama": {Enabled: true}},
		Defaults: ProfileParams{Server: &defaultServer},
		Profiles: map[string]Profile{
			"zeta-default":  {Description: "Z default"},
			"alpha-default": {Description: "A default"},
			"bravo-fav":     {Description: "B fav", IsFavourite: true},
			"alpha-other":   {Description: "A ollama", ProfileParams: ProfileParams{Server: &otherServer}},
			"zeta-other-fav": {
				Description:   "Z ollama fav",
				IsFavourite:   true,
				ProfileParams: ProfileParams{Server: &otherServer},
			},
		},
	}

	names := cfg.ProfileNames()
	want := []string{
		// favourites first, then alphabetical-by-server, then by name within group
		"bravo-fav",
		"zeta-other-fav",
		// non-favourites, alphabetical-by-server (llamacpp < ollama), then by name
		"alpha-default",
		"zeta-default",
		"alpha-other",
	}

	if len(names) != len(want) {
		t.Fatalf("len = %d, want %d: got %v", len(names), len(want), names)
	}
	for i, name := range names {
		if name != want[i] {
			t.Errorf("names[%d] = %q, want %q (got order: %v)", i, name, want[i], names)
		}
	}
}

func TestProfileNames_ConfigOrder(t *testing.T) {
	t.Parallel()

	server := "llamacpp"
	sortFalse := false
	cfg := &Config{
		Servers:            map[string]ServerConfig{"llamacpp": {Enabled: true}},
		Defaults:           ProfileParams{Server: &server},
		SortAlphabetically: &sortFalse,
		Profiles: map[string]Profile{
			"charlie": {Description: "C"},
			"alpha":   {Description: "A", IsFavourite: true},
			"bravo":   {Description: "B"},
		},
		profileOrder: []string{"charlie", "alpha", "bravo"},
	}

	names := cfg.ProfileNames()
	want := []string{"charlie", "alpha", "bravo"}

	if len(names) != len(want) {
		t.Fatalf("len = %d, want %d: got %v", len(names), len(want), names)
	}
	for i, name := range names {
		if name != want[i] {
			t.Errorf("names[%d] = %q, want %q (got %v)", i, name, want[i], names)
		}
	}
}

func TestProfileNames_ConfigOrder_FiltersDisabledServers(t *testing.T) {
	t.Parallel()

	defaultServer := "llamacpp"
	otherServer := "ollama"
	sortFalse := false
	cfg := &Config{
		Servers:            map[string]ServerConfig{"llamacpp": {Enabled: true}, "ollama": {Enabled: false}},
		Defaults:           ProfileParams{Server: &defaultServer},
		SortAlphabetically: &sortFalse,
		Profiles: map[string]Profile{
			"first":  {Description: "first"},
			"second": {Description: "second", ProfileParams: ProfileParams{Server: &otherServer}},
			"third":  {Description: "third"},
		},
		profileOrder: []string{"first", "second", "third"},
	}

	names := cfg.ProfileNames()
	want := []string{"first", "third"}

	if len(names) != len(want) {
		t.Fatalf("len = %d, want %d: got %v", len(names), len(want), names)
	}
	for i, name := range names {
		if name != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, name, want[i])
		}
	}
}

func TestProfileNames_DefaultsToAlphabetic(t *testing.T) {
	t.Parallel()

	server := "llamacpp"
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		Defaults: ProfileParams{Server: &server},
		Profiles: map[string]Profile{
			"charlie": {Description: "C"},
			"alpha":   {Description: "A"},
			"bravo":   {Description: "B"},
		},
		profileOrder: []string{"charlie", "alpha", "bravo"},
	}

	names := cfg.ProfileNames()
	want := []string{"alpha", "bravo", "charlie"}

	if len(names) != len(want) {
		t.Fatalf("len = %d, want %d: got %v", len(names), len(want), names)
	}
	for i, name := range names {
		if name != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, name, want[i])
		}
	}
}

func TestParseConfig_CapturesProfileOrder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	yaml := `
servers:
  llamacpp: true
profiles:
  zeta:
    description: "Z"
    model: z.gguf
  alpha:
    description: "A"
    model: a.gguf
  mike:
    description: "M"
    model: m.gguf
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := parseConfig(cfgPath)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}

	want := []string{"zeta", "alpha", "mike"}
	if len(cfg.profileOrder) != len(want) {
		t.Fatalf("profileOrder = %v, want %v", cfg.profileOrder, want)
	}
	for i, name := range cfg.profileOrder {
		if name != want[i] {
			t.Errorf("profileOrder[%d] = %q, want %q", i, name, want[i])
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
			Servers:   map[string]ServerConfig{"llamacpp": {Enabled: true}},
			ModelsDir: dir,
			Defaults:  ProfileParams{Server: strPtr("llamacpp")},
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
			Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
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
	if !strings.Contains(err.Error(), "'host'/'port'") || !strings.Contains(err.Error(), "defaults") {
		t.Errorf("error = %q, want it to name the host/port-in-defaults migration path", err)
	}
	if strings.Contains(err.Error(), "merged into") {
		t.Errorf("error = %q, must not instruct moving entries into the servers section", err)
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

func TestValidate_DefaultsServerFallbackWarning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  llamacpp: true
  ollama: true
defaults:
  server: llamacpp
profiles:
  no-server:
    model: test.gguf
  has-server:
    server: ollama
    model: llama3.1:8b
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one", cfg.Warnings)
	}
	if !strings.Contains(cfg.Warnings[0], `"no-server"`) {
		t.Errorf("warning = %q, want it to name profile 'no-server'", cfg.Warnings[0])
	}
	if !strings.Contains(cfg.Warnings[0], "defaults.server") {
		t.Errorf("warning = %q, want it to mention defaults.server", cfg.Warnings[0])
	}
}

func TestValidate_NoFallbackWarning_SingleServer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  llamacpp: true
profiles:
  no-server:
    model: test.gguf
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("expected no warnings with single enabled server, got: %v", cfg.Warnings)
	}
}

func TestValidateAll_DefaultsServerFallbackWarning(t *testing.T) {
	t.Parallel()

	server := "llamacpp"
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}, "ollama": {Enabled: true}},
		Defaults: ProfileParams{Server: &server},
		Profiles: map[string]Profile{
			"no-server": {Model: "test.gguf"},
		},
	}

	problems := cfg.validateAll()
	found := false
	for _, p := range problems {
		if strings.Contains(p, "defaults.server") && strings.Contains(p, `"no-server"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected defaults.server deprecation warning in validateAll problems, got: %v", problems)
	}
}

func TestConfiguredBackendAddr(t *testing.T) {
	t.Parallel()

	server := "llamacpp"
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
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

func TestParseConfig(t *testing.T) {
	t.Parallel()

	t.Run("returns config even with validation issues", func(t *testing.T) {
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
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
			t.Fatalf("writing config: %v", err)
		}

		cfg, err := parseConfig(cfgPath)
		if err != nil {
			t.Fatalf("parseConfig should succeed: %v", err)
		}
		if len(cfg.Profiles) != 1 {
			t.Errorf("Profiles count = %d, want 1", len(cfg.Profiles))
		}
	})

	t.Run("returns error for invalid YAML", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(cfgPath, []byte("not: valid: yaml: ["), 0o644); err != nil {
			t.Fatalf("writing config: %v", err)
		}

		_, err := parseConfig(cfgPath)
		if err == nil {
			t.Fatal("expected parse error, got nil")
		}
	})

	t.Run("returns ErrConfigNotFound for missing file", func(t *testing.T) {
		t.Parallel()
		_, err := parseConfig("/nonexistent/config.yaml")
		if !isConfigNotFoundError(err) {
			t.Errorf("expected ErrConfigNotFound, got: %v", err)
		}
	})
}

func TestValidateAll(t *testing.T) {
	t.Parallel()

	t.Run("valid config returns no problems", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		modelPath := filepath.Join(dir, "test.gguf")
		if err := os.WriteFile(modelPath, []byte("fake"), 0o644); err != nil {
			t.Fatalf("writing model: %v", err)
		}

		server := "llamacpp"
		cfg := &Config{
			Servers:   map[string]ServerConfig{"llamacpp": {Enabled: true}},
			ModelsDir: dir,
			Defaults:  ProfileParams{Server: &server},
			Profiles: map[string]Profile{
				"test": {Description: "Test", Model: "test.gguf"},
			},
		}
		problems := cfg.validateAll()
		if len(problems) != 0 {
			t.Errorf("expected no problems, got: %v", problems)
		}
	})

	t.Run("collects multiple errors", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			DefaultBackend: "llamacpp",
			Endpoints:      map[string]string{"llamacpp": "localhost:8080"},
			Servers:        map[string]ServerConfig{},
			Profiles:       map[string]Profile{},
		}
		problems := cfg.validateAll()
		if len(problems) < 3 {
			t.Errorf("expected at least 3 problems, got %d: %v", len(problems), problems)
		}
	})

	t.Run("reports deprecated backend in profiles", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{"llamacpp": {Enabled: true}},
			Profiles: map[string]Profile{
				"test": {Backend: "llamacpp", Model: "test.gguf"},
			},
		}
		problems := cfg.validateAll()
		found := false
		for _, p := range problems {
			if strings.Contains(p, "backend") && strings.Contains(p, "renamed") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected deprecated backend warning, got: %v", problems)
		}
	})

	t.Run("reports missing model file", func(t *testing.T) {
		t.Parallel()
		server := "llamacpp"
		cfg := &Config{
			Servers:   map[string]ServerConfig{"llamacpp": {Enabled: true}},
			ModelsDir: "/nonexistent/models",
			Defaults:  ProfileParams{Server: &server},
			Profiles: map[string]Profile{
				"test": {Model: "missing.gguf"},
			},
		}
		problems := cfg.validateAll()
		found := false
		for _, p := range problems {
			if strings.Contains(p, "model not found") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected model-not-found problem, got: %v", problems)
		}
	})

	t.Run("reports unknown server", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{"nonexistent_backend": {Enabled: true}},
			Profiles: map[string]Profile{
				"test": {Model: "test.gguf"},
			},
		}
		problems := cfg.validateAll()
		found := false
		for _, p := range problems {
			if strings.Contains(p, "unknown server") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected unknown server problem, got: %v", problems)
		}
	})

	t.Run("reports no servers enabled", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{"llamacpp": {Enabled: false}},
			Profiles: map[string]Profile{
				"test": {Model: "test.gguf"},
			},
		}
		problems := cfg.validateAll()
		found := false
		for _, p := range problems {
			if strings.Contains(p, "no servers enabled") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected no-servers-enabled problem, got: %v", problems)
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

func TestMenuRefreshInterval(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		val  *int
		want time.Duration
	}{
		{name: "nil defaults to 10s", val: nil, want: 10 * time.Second},
		{name: "explicit 5", val: ptrInt(5), want: 5 * time.Second},
		{name: "explicit 1", val: ptrInt(1), want: 1 * time.Second},
		{name: "zero clamps to 1s", val: ptrInt(0), want: 1 * time.Second},
		{name: "negative clamps to 1s", val: ptrInt(-7), want: 1 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{RefreshDuration: tc.val}
			if got := cfg.MenuRefreshInterval(); got != tc.want {
				t.Errorf("MenuRefreshInterval() = %v, want %v", got, tc.want)
			}
		})
	}
}

func ptrInt(v int) *int { return &v }

func ptrStr(v string) *string { return &v }

func TestMemoryBarDefaults(t *testing.T) {
	t.Parallel()

	t.Run("absent block uses built-in defaults", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{}
		if got, want := cfg.memoryBarDefaults(), builtinBarDefaults(); got != want {
			t.Errorf("memoryBarDefaults = %+v, want %+v", got, want)
		}
	})

	t.Run("partial block overrides only the set keys", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{MemoryStatusBar: &MemoryStatusBar{Width: ptrInt(6)}}
		got := cfg.memoryBarDefaults()
		if got.Width != 6 {
			t.Errorf("Width = %d, want 6", got.Width)
		}
		if want := builtinBarDefaults(); got.Fg != want.Fg || got.Bg != want.Bg {
			t.Errorf("colors = %q/%q, want built-in %q/%q", got.Fg, got.Bg, want.Fg, want.Bg)
		}
	})

	t.Run("full block resolves colors and clamps width", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{MemoryStatusBar: &MemoryStatusBar{
			Width:      ptrInt(99),
			Color:      ptrStr("yellow"),
			Background: ptrStr("blue"),
		}}
		got := cfg.memoryBarDefaults()
		if got.Width != maxBarWidth {
			t.Errorf("Width = %d, want clamped %d", got.Width, maxBarWidth)
		}
		if got.Fg != "\033[33m" || got.Bg != "\033[44m" {
			t.Errorf("colors = %q/%q, want yellow foreground / blue background escapes", got.Fg, got.Bg)
		}
	})

	t.Run("256-color and hex values resolve", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{MemoryStatusBar: &MemoryStatusBar{
			Color:      ptrStr("#7aa2f7"),
			Background: ptrStr("240"),
		}}
		got := cfg.memoryBarDefaults()
		if got.Fg != "\033[38;2;122;162;247m" || got.Bg != "\033[48;5;240m" {
			t.Errorf("colors = %q/%q, want hex foreground / 256-palette background escapes", got.Fg, got.Bg)
		}
	})

	t.Run("unknown colors fall back and warn", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{MemoryStatusBar: &MemoryStatusBar{
			Color:      ptrStr("sparkle"),
			Background: ptrStr("glitter"),
		}}
		if got, want := cfg.memoryBarDefaults(), builtinBarDefaults(); got != want {
			t.Errorf("memoryBarDefaults = %+v, want built-in %+v", got, want)
		}
		warnings := cfg.memoryBarWarnings()
		if len(warnings) != 2 {
			t.Fatalf("warnings = %v, want 2 entries", warnings)
		}
		if !strings.Contains(warnings[0], "sparkle") || !strings.Contains(warnings[1], "glitter") {
			t.Errorf("warnings = %v, want them to name the bad colors", warnings)
		}
	})
}

func TestLoadConfig_MemoryBarWarning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  llamacpp: true
models_dir: /tmp/models
memory_status_bar:
  color: sparkle
profiles:
  test-profile:
    model: test.gguf
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	found := false
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "memory_status_bar") && strings.Contains(w, "sparkle") {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %v, want a memory_status_bar warning naming \"sparkle\"", cfg.Warnings)
	}
}

func TestCompiledMemoryTemplate_Memoization(t *testing.T) {
	t.Parallel()

	cfg := &Config{MemoryStatusFormat: ptrStr("{free_ram} free")}

	first := cfg.CompiledMemoryTemplate()
	if second := cfg.CompiledMemoryTemplate(); second != first {
		t.Error("unchanged config should return the cached template")
	}

	cfg.MemoryStatusFormat = ptrStr("{used_ram_pct:bar}")
	changed := cfg.CompiledMemoryTemplate()
	if changed == first {
		t.Error("changed format string should recompile")
	}
	if !changed.Styled() {
		t.Error("bar template should report Styled")
	}

	cfg.MemoryStatusBar = &MemoryStatusBar{Width: ptrInt(6)}
	if rebar := cfg.CompiledMemoryTemplate(); rebar == changed {
		t.Error("changed bar defaults should recompile")
	}

	// Reload replaces the struct wholesale, clearing the unexported cache.
	fresh := &Config{MemoryStatusFormat: ptrStr("{used_ram_pct:bar}")}
	*cfg = *fresh
	if cfg.memTpl != nil {
		t.Error("struct replacement should clear the compiled-template cache")
	}
	if cfg.CompiledMemoryTemplate() == nil {
		t.Error("recompile after replacement should succeed")
	}
}

func TestServerConfigUnmarshal(t *testing.T) {
	t.Parallel()

	writeAndParse := func(t *testing.T, serversYAML string) *Config {
		t.Helper()
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		yaml := "servers:\n" + serversYAML + "profiles:\n  test:\n    model: test.gguf\n"
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
			t.Fatalf("writing config: %v", err)
		}
		cfg, err := parseConfig(cfgPath)
		if err != nil {
			t.Fatalf("parseConfig: %v", err)
		}
		return cfg
	}

	t.Run("scalar bool form", func(t *testing.T) {
		t.Parallel()
		cfg := writeAndParse(t, "  llamacpp: true\n  ollama: false\n")
		if !cfg.Servers["llamacpp"].Enabled {
			t.Error("llamacpp should be enabled")
		}
		if cfg.Servers["ollama"].Enabled {
			t.Error("ollama should be disabled")
		}
		if cfg.APIKeyFor("llamacpp") != "" {
			t.Error("scalar form must not carry an api key")
		}
	})

	t.Run("mapping form with api_key", func(t *testing.T) {
		t.Parallel()
		cfg := writeAndParse(t, "  llamacpp:\n    enabled: true\n    api_key: secret\n")
		if !cfg.Servers["llamacpp"].Enabled {
			t.Error("llamacpp should be enabled")
		}
		if got := cfg.APIKeyFor("llamacpp"); got != "secret" {
			t.Errorf("APIKeyFor = %q, want %q", got, "secret")
		}
	})

	t.Run("mapping form defaults enabled to true", func(t *testing.T) {
		t.Parallel()
		cfg := writeAndParse(t, "  llamacpp:\n    api_key: secret\n")
		if !cfg.Servers["llamacpp"].Enabled {
			t.Error("mapping form without enabled should default to enabled")
		}
	})

	t.Run("mapping form with enabled false keeps the key", func(t *testing.T) {
		t.Parallel()
		cfg := writeAndParse(t, "  llamacpp:\n    enabled: false\n    api_key: secret\n")
		if cfg.Servers["llamacpp"].Enabled {
			t.Error("llamacpp should be disabled")
		}
		if got := cfg.APIKeyFor("llamacpp"); got != "secret" {
			t.Errorf("APIKeyFor = %q, want %q", got, "secret")
		}
	})

	t.Run("invalid scalar returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		yaml := "servers:\n  llamacpp: maybe\nprofiles:\n  test:\n    model: m\n"
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
			t.Fatalf("writing config: %v", err)
		}
		if _, err := parseConfig(cfgPath); err == nil {
			t.Fatal("expected error for non-bool scalar server entry")
		}
	})
}

func TestAPIKeyWhitespaceWarning(t *testing.T) {
	t.Parallel()

	cfg := &Config{Servers: map[string]ServerConfig{
		"llamacpp": {Enabled: true, APIKey: " secret \n"},
		"ollama":   {Enabled: true, APIKey: "clean"},
	}}

	if got := cfg.APIKeyFor("llamacpp"); got != "secret" {
		t.Errorf("APIKeyFor = %q, want trimmed %q", got, "secret")
	}

	warnings := cfg.apiKeyWarnings()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	if !strings.Contains(warnings[0], "servers.llamacpp") {
		t.Errorf("warning = %q, want it to name servers.llamacpp", warnings[0])
	}
}
