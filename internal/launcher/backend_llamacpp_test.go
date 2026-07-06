package launcher

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLlamaCppBuildServerArgs(t *testing.T) {
	t.Parallel()

	b := &LlamaCpp{}

	intPtr := func(v int) *int { return &v }
	strPtr := func(v string) *string { return &v }
	boolPtr := func(v bool) *bool { return &v }
	floatPtr := func(v float64) *float64 { return &v }

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

	t.Run("sampling params are emitted when set", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{}
		profile := &ResolvedProfile{
			ProfileParams: ProfileParams{
				Temperature:   floatPtr(0.7),
				RepeatPenalty: floatPtr(1.1),
				TopK:          intPtr(40),
				TopP:          floatPtr(0.95),
				MinP:          floatPtr(0.05),
			},
			ExtraArgs: []string{"--temp", "0.9"},
		}

		args := b.BuildServerArgs(cfg, profile)
		argSet := toArgMap(args)

		assertArg(t, argSet, "--repeat-penalty", "1.1")
		assertArg(t, argSet, "--top-k", "40")
		assertArg(t, argSet, "--top-p", "0.95")
		assertArg(t, argSet, "--min-p", "0.05")

		// Sampling flags must precede extra_args so a user override wins
		// (llama-server uses the last occurrence of a repeated flag).
		if len(args) < 2 || args[len(args)-2] != "--temp" || args[len(args)-1] != "0.9" {
			t.Errorf("extra_args must come after sampling flags, got: %v", args)
		}
		if !hasArgPair(args, "--temp", "0.7") {
			t.Errorf("missing configured --temp 0.7 before extra_args: %v", args)
		}
	})

	t.Run("nil sampling params omit the flags", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{}
		profile := &ResolvedProfile{
			ProfileParams: ProfileParams{Host: strPtr("localhost")},
		}

		args := b.BuildServerArgs(cfg, profile)
		for _, flag := range []string{"--temp", "--repeat-penalty", "--top-k", "--top-p", "--min-p"} {
			for _, arg := range args {
				if arg == flag {
					t.Errorf("unexpected sampling flag %s in args: %v", flag, args)
				}
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

	t.Run("api key from server config is passed before extra args", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true, APIKey: "secret"},
		}}
		profile := &ResolvedProfile{ExtraArgs: []string{"--no-warmup"}}

		args := b.BuildServerArgs(cfg, profile)
		assertArg(t, toArgMap(args), "--api-key", "secret")
		if args[len(args)-1] != "--no-warmup" {
			t.Errorf("extra args must come after --api-key, got: %v", args)
		}
	})

	t.Run("no api key omits the flag", func(t *testing.T) {
		t.Parallel()

		cfg := &Config{Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true},
		}}
		args := b.BuildServerArgs(cfg, &ResolvedProfile{})
		for _, arg := range args {
			if arg == "--api-key" {
				t.Errorf("unexpected --api-key in args: %v", args)
			}
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

	b := &LlamaCpp{}
	cfg := &Config{Servers: map[string]ServerConfig{"llamacpp": {Enabled: true}}}
	if got := b.ServerBinary(cfg); got != "llama-server" {
		t.Errorf("ServerBinary = %q, want llama-server", got)
	}
}

func TestLlamaCppHealthCheck(t *testing.T) {
	t.Parallel()

	b := &LlamaCpp{}

	t.Run("healthy with 200 and status field", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		}))
		defer srv.Close()

		if err := b.HealthCheck(addrFromURL(t, srv.URL)); err != nil {
			t.Errorf("expected healthy, got: %v", err)
		}
	})

	t.Run("rejects non-llamacpp /health body", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"Unexpected endpoint or method."}`))
		}))
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error for non-llamacpp /health body")
		}
		if !strings.Contains(err.Error(), "not llamacpp") {
			t.Errorf("error = %q, want it to contain 'not llamacpp'", err)
		}
	})

	t.Run("unhealthy with non-200", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error for unhealthy status")
		}
		if !strings.Contains(err.Error(), "unhealthy") {
			t.Errorf("error = %q, want it to contain 'unhealthy'", err)
		}
	})

	t.Run("unreachable server", func(t *testing.T) {
		t.Parallel()
		if err := b.HealthCheck("127.0.0.1:1"); err == nil {
			t.Fatal("expected error for unreachable server")
		}
	})

	t.Run("rejects a body larger than the read cap", func(t *testing.T) {
		t.Parallel()
		// Valid JSON overall, but the bounded read truncates it mid-body;
		// an unbounded read would accept this response as healthy.
		huge := `{"status":"ok","pad":"` + strings.Repeat("a", maxStatusBodyBytes) + `"}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(huge))
		}))
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error for a body larger than the read cap")
		}
		if !strings.Contains(err.Error(), "not llamacpp") {
			t.Errorf("error = %q, want it to contain 'not llamacpp'", err)
		}
	})
}

func TestLlamaCppAuthHeaders(t *testing.T) {
	t.Parallel()

	srv, authFor := recordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"m"}]}`))
		case "/props":
			w.Write([]byte(`{}`))
		}
	})
	addr := addrFromURL(t, srv.URL)

	b := &LlamaCpp{apiKey: "k"}
	if err := b.HealthCheck(addr); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if _, err := b.ListRunningModels(addr); err != nil {
		t.Fatalf("ListRunningModels: %v", err)
	}
	if _, err := b.QueryLiveParams(addr); err != nil {
		t.Fatalf("QueryLiveParams: %v", err)
	}
	for _, path := range []string{"/health", "/v1/models", "/props"} {
		if got := authFor(path); got != "Bearer k" {
			t.Errorf("Authorization on %s = %q, want %q", path, got, "Bearer k")
		}
	}

	noKey := &LlamaCpp{}
	if err := noKey.HealthCheck(addr); err != nil {
		t.Fatalf("HealthCheck without key: %v", err)
	}
	if got := authFor("/health"); got != "" {
		t.Errorf("Authorization without key = %q, want empty", got)
	}
}

func TestLlamaCppAuthFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := &LlamaCpp{apiKey: "wrong"}
	_, err := b.ListRunningModels(addrFromURL(t, srv.URL))
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error = %v, want actionable api_key message", err)
	}
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

func hasArgPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
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
