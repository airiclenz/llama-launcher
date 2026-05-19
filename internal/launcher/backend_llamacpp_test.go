package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLlamaCppBuildServerArgs(t *testing.T) {
	t.Parallel()

	b := &LlamaCpp{}

	intPtr := func(v int) *int { return &v }
	strPtr := func(v string) *string { return &v }
	boolPtr := func(v bool) *bool { return &v }

	t.Run("full parameter set", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{}
		profile := &ResolvedProfile{
			ModelPath: "/models/test.gguf",
			ProfileParams: ProfileParams{
				Host:      strPtr("0.0.0.0"),
				Port:      intPtr(9090),
				GPULayers: intPtr(99),
				Threads:   intPtr(8),
				FlashAttn: boolPtr(true),
				Mlock:     boolPtr(true),
				Jinja:     boolPtr(true),
			},
			ExtraArgs: []string{"--no-warmup"},
		}

		args := b.BuildServerArgs(cfg, profile)
		argSet := toArgMap(args)

		assertArg(t, argSet, "--model", "/models/test.gguf")
		assertArg(t, argSet, "--host", "0.0.0.0")
		assertArg(t, argSet, "--port", "9090")
		assertArg(t, argSet, "-ngl", "99")
		assertArg(t, argSet, "-t", "8")
		assertArg(t, argSet, "-fa", "on")
		assertFlagPresent(t, args, "--mlock")
		assertFlagPresent(t, args, "--jinja")
		assertFlagPresent(t, args, "--no-warmup")
	})

	t.Run("nil params produce empty args", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{}
		profile := &ResolvedProfile{}

		args := b.BuildServerArgs(cfg, profile)

		if len(args) != 0 {
			t.Errorf("len(args) = %d, want 0, got %v", len(args), args)
		}
	})

	t.Run("false booleans are not included", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{}
		profile := &ResolvedProfile{
			ProfileParams: ProfileParams{
				FlashAttn: boolPtr(false),
				Mlock:     boolPtr(false),
				NoMmap:    boolPtr(false),
				Jinja:     boolPtr(false),
			},
		}

		args := b.BuildServerArgs(cfg, profile)
		assertArg(t, toArgMap(args), "-fa", "off")
		for _, arg := range args {
			if arg == "--mlock" || arg == "--no-mmap" || arg == "--jinja" {
				t.Errorf("unexpected boolean flag: %s", arg)
			}
		}
	})

	t.Run("no model path omits --model", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{}
		profile := &ResolvedProfile{
			ProfileParams: ProfileParams{Host: strPtr("localhost")},
		}

		args := b.BuildServerArgs(cfg, profile)
		for _, arg := range args {
			if arg == "--model" {
				t.Error("--model should not appear with empty ModelPath")
			}
		}
	})

	t.Run("extra args are appended", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{}
		profile := &ResolvedProfile{
			ExtraArgs: []string{"--cache-type-k", "q8_0"},
		}

		args := b.BuildServerArgs(cfg, profile)
		if len(args) < 2 || args[len(args)-2] != "--cache-type-k" || args[len(args)-1] != "q8_0" {
			t.Errorf("extra args not appended correctly: %v", args)
		}
	})
}

func TestLlamaCppResolveModel(t *testing.T) {
	t.Parallel()

	b := &LlamaCpp{}

	t.Run("empty model ref returns empty path", func(t *testing.T) {
		t.Parallel()
		path, err := b.ResolveModel(&Config{}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if path != "" {
			t.Errorf("path = %q, want empty", path)
		}
	})

	t.Run("nonexistent model returns error", func(t *testing.T) {
		t.Parallel()
		_, err := b.ResolveModel(&Config{ModelsDir: "/nonexistent"}, "fake.gguf")
		if err == nil {
			t.Fatal("expected error for missing model")
		}
	})

	t.Run("existing file resolves successfully", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		modelFile := "test-model.gguf"
		if err := writeTestFile(t, dir, modelFile); err != nil {
			t.Fatal(err)
		}

		cfg := &Config{ModelsDir: dir}
		path, err := b.ResolveModel(cfg, modelFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if path == "" {
			t.Error("expected non-empty path")
		}
	})

	t.Run("directory path returns error", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		cfg := &Config{ModelsDir: dir}
		_, err := b.ResolveModel(cfg, ".")
		if err == nil {
			t.Fatal("expected error for directory path")
		}
	})
}

func TestLlamaCppName(t *testing.T) {
	t.Parallel()

	b := &LlamaCpp{}
	if b.Name() != "llamacpp" {
		t.Errorf("Name() = %q, want llamacpp", b.Name())
	}
}

func TestLlamaCppServerBinary(t *testing.T) {
	t.Parallel()

	t.Run("uses config path", func(t *testing.T) {
		t.Parallel()
		b := &LlamaCpp{}
		cfg := &Config{Servers: map[string]string{"llamacpp": "/opt/bin/llama-server"}}
		if got := b.ServerBinary(cfg); got != "/opt/bin/llama-server" {
			t.Errorf("ServerBinary = %q, want /opt/bin/llama-server", got)
		}
	})

	t.Run("falls back to llama-server", func(t *testing.T) {
		t.Parallel()
		b := &LlamaCpp{}
		cfg := &Config{Servers: map[string]string{}}
		if got := b.ServerBinary(cfg); got != "llama-server" {
			t.Errorf("ServerBinary = %q, want llama-server", got)
		}
	})
}

// --- test helpers ---

func toArgMap(args []string) map[string]string {
	m := make(map[string]string, len(args)/2)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			m[args[i]] = args[i+1]
		}
	}
	return m
}

func assertArg(t *testing.T, m map[string]string, key, want string) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("missing arg %s", key)
		return
	}
	if got != want {
		t.Errorf("arg %s = %q, want %q", key, got, want)
	}
}

func assertFlagPresent(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, a := range args {
		if a == flag {
			return
		}
	}
	t.Errorf("missing flag %s in args: %v", flag, args)
}

func writeTestFile(t *testing.T, dir, name string) error {
	t.Helper()
	return os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o644)
}
