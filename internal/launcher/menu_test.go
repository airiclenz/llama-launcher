package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		max   int
		want  int
	}{
		{"valid first", "1", 5, 0},
		{"valid last", "5", 5, 4},
		{"zero", "0", 5, -1},
		{"negative", "-1", 5, -1},
		{"exceeds max", "6", 5, -1},
		{"non-numeric", "abc", 5, -1},
		{"empty", "", 5, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseChoice(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("parseChoice(%q, %d) = %d, want %d", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

func TestFormatUptime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"seconds only", 45 * time.Second, "45s"},
		{"minutes and seconds", 3*time.Minute + 15*time.Second, "3m 15s"},
		{"hours minutes seconds", 2*time.Hour + 5*time.Minute + 30*time.Second, "2h 05m 30s"},
		{"zero", 0, "0s"},
		{"exactly one hour", 1 * time.Hour, "1h 00m 00s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatUptime(tt.duration)
			if got != tt.want {
				t.Errorf("formatUptime(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestPrimaryInstance(t *testing.T) {
	t.Parallel()

	idleFirst := &RunningInstance{Backend: "lmstudio", Host: "127.0.0.1", Port: 1234}
	idleSecond := &RunningInstance{Backend: "ollama", Host: "127.0.0.1", Port: 11434}
	loaded := &RunningInstance{Backend: "ollama", Host: "127.0.0.1", Port: 11434, ActiveModel: "llama3"}
	loadedSecond := &RunningInstance{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, ActiveModel: "qwen3"}

	tests := []struct {
		name      string
		instances []*RunningInstance
		want      *RunningInstance
	}{
		{"idle first, loaded second", []*RunningInstance{idleFirst, loaded}, loaded},
		{"loaded first, idle second", []*RunningInstance{loaded, idleSecond}, loaded},
		{"two loaded, first wins", []*RunningInstance{loaded, loadedSecond}, loaded},
		{"all idle, sort-first wins", []*RunningInstance{idleFirst, idleSecond}, idleFirst},
		{"single idle", []*RunningInstance{idleFirst}, idleFirst},
		{"empty", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := primaryInstance(tt.instances); got != tt.want {
				t.Errorf("primaryInstance() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestProfileDisplayName(t *testing.T) {
	t.Parallel()

	t.Run("with title", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Profiles: map[string]Profile{
				"test": {Title: "My Test Profile"},
			},
		}
		got := profileDisplayName(cfg, "test")
		if got != "My Test Profile" {
			t.Errorf("got %q, want %q", got, "My Test Profile")
		}
	})

	t.Run("without title falls back to profile name", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Profiles: map[string]Profile{
				"test": {Description: "Only shown in the config popup"},
			},
		}
		got := profileDisplayName(cfg, "test")
		if got != "test" {
			t.Errorf("got %q, want %q", got, "test")
		}
	})

	t.Run("unknown profile", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Profiles: map[string]Profile{},
		}
		got := profileDisplayName(cfg, "unknown")
		if got != "unknown" {
			t.Errorf("got %q, want %q", got, "unknown")
		}
	})
}

func TestFormatProfileParams_LMStudio(t *testing.T) {
	t.Parallel()

	findLine := func(lines []string, substr string) bool {
		for _, line := range lines {
			if contains(line, substr) {
				return true
			}
		}
		return false
	}

	t.Run("omits GPU offload — not part of the load request", func(t *testing.T) {
		t.Parallel()
		layers := 99
		profile := &ResolvedProfile{
			Backend:       "lmstudio",
			ModelPath:     "test-model",
			ProfileParams: ProfileParams{GPULayers: &layers},
		}
		lines := formatProfileParams(profile)
		if findLine(lines, "GPU offload") || findLine(lines, "GPU layers") {
			t.Errorf("expected no GPU line for lmstudio profile, got lines: %v", lines)
		}
	})

	t.Run("shows the params the load request sends", func(t *testing.T) {
		t.Parallel()
		batchSize := 512
		flashAttn := true
		parallel := 2
		profile := &ResolvedProfile{
			Backend:   "lmstudio",
			ModelPath: "test-model",
			ProfileParams: ProfileParams{
				BatchSize: &batchSize,
				FlashAttn: &flashAttn,
				Parallel:  &parallel,
			},
		}
		lines := formatProfileParams(profile)
		for _, want := range []string{"Batch size", "Flash attention", "Parallel"} {
			if !findLine(lines, want) {
				t.Errorf("expected %q line for lmstudio profile, got lines: %v", want, lines)
			}
		}
	})

	t.Run("omits llamacpp-only params", func(t *testing.T) {
		t.Parallel()
		threads := 8
		mlock := true
		profile := &ResolvedProfile{
			Backend:   "lmstudio",
			ModelPath: "test-model",
			ProfileParams: ProfileParams{
				Threads: &threads,
				Mlock:   &mlock,
			},
		}
		lines := formatProfileParams(profile)
		if findLine(lines, "Threads") || findLine(lines, "Mlock") {
			t.Errorf("expected no llamacpp-only lines for lmstudio profile, got lines: %v", lines)
		}
	})
}

func TestFormatProfileParams_OllamaShowsNoParams(t *testing.T) {
	t.Parallel()

	ctx := 4096
	profile := &ResolvedProfile{
		Backend:       "ollama",
		ModelPath:     "llama3",
		ProfileParams: ProfileParams{ContextSize: &ctx},
	}
	lines := formatProfileParams(profile)
	for _, line := range lines {
		if contains(line, "Context size") {
			t.Errorf("expected no Context size line for ollama profile (its load request never carries it), got lines: %v", lines)
		}
	}
}

// specStubServer is a minimal LLMServer whose only purpose is to carry a
// param spec of its own, proving the menu renders profile parameters purely
// from the backend-owned spec.
type specStubServer struct {
	name  string
	specs []ProfileParamSpec
}

func (s *specStubServer) Name() string                                       { return s.name }
func (s *specStubServer) DisplayName() string                                { return s.name }
func (s *specStubServer) DefaultAddr() string                                { return "localhost:0" }
func (s *specStubServer) HealthCheck(string) error                           { return nil }
func (s *specStubServer) ResolveModel(_ *Config, ref string) (string, error) { return ref, nil }
func (s *specStubServer) LoadModel(string, *ResolvedProfile) error           { return nil }
func (s *specStubServer) UnloadModel(string, string) error                   { return nil }
func (s *specStubServer) TryStart(*Config, string) error                     { return nil }
func (s *specStubServer) TryStop(string) error                               { return nil }
func (s *specStubServer) ParamSpecs() []ProfileParamSpec                     { return s.specs }

// TestFormatProfileParams_RendersBackendOwnedSpec registers a brand-new
// backend and asserts its profile pop-up renders exactly that backend's
// spec, in spec order — i.e. adding a backend requires no edit in menu.go.
// Not parallel: it mutates the global llmServers registry, which is safe
// only while no parallel test is running (sequential tests never overlap
// with parallel ones).
func TestFormatProfileParams_RendersBackendOwnedSpec(t *testing.T) {
	stub := &specStubServer{
		name: "specstub",
		specs: []ProfileParamSpec{
			intParamSpec("Stub knob", func(p *ProfileParams) *int { return p.Threads }),
			specContextSize,
		},
	}
	RegisterLLMServer(stub)
	t.Cleanup(func() { delete(llmServers, stub.name) })

	threads := 8
	ctx := 4096
	mlock := true
	profile := &ResolvedProfile{
		Backend:   stub.name,
		ModelPath: "stub-model",
		ProfileParams: ProfileParams{
			Threads:     &threads,
			ContextSize: &ctx,
			Mlock:       &mlock, // not in the stub's spec — must not render
		},
	}
	lines := formatProfileParams(profile)

	knobIdx, ctxIdx := -1, -1
	for i, line := range lines {
		switch {
		case contains(line, "Stub knob"):
			knobIdx = i
			if !contains(line, "8") {
				t.Errorf("Stub knob line missing value 8: %q", line)
			}
		case contains(line, "Context size"):
			ctxIdx = i
			if !contains(line, "4096") {
				t.Errorf("Context size line missing value 4096: %q", line)
			}
		case contains(line, "Mlock"):
			t.Errorf("Mlock rendered although absent from the backend's spec: %q", line)
		}
	}
	if knobIdx == -1 || ctxIdx == -1 {
		t.Fatalf("expected both spec'd params rendered, got lines: %v", lines)
	}
	if knobIdx > ctxIdx {
		t.Errorf("params rendered out of spec order (Stub knob at %d after Context size at %d)", knobIdx, ctxIdx)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestDoLoadProfile_RefusesStartingOccupant verifies the menu's load and
// model-swap actions run through the same ADR-0010 refusal as the CLI:
// doLoadProfile (the funnel behind both) calls LoadProfile with
// restart=false, so a menu load onto a Starting address refuses with the
// stop/--restart guidance instead of displacing the in-flight model load.
// Not parallel: captureStdout swaps os.Stdout.
func TestDoLoadProfile_RefusesStartingOccupant(t *testing.T) {
	srv := newFakeStartingLlamaCppServer(t)
	cfg := startingCfg(t, "llamacpp", addrFromURL(t, srv.URL))
	modelPath := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(modelPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["big"] = Profile{Model: modelPath}

	var err error
	_ = captureStdout(t, func() { err = doLoadProfile(cfg, "big") })

	if err == nil {
		t.Fatal("doLoadProfile succeeded, want the Starting-occupant refusal")
	}
	if !strings.Contains(err.Error(), "--restart") || !strings.Contains(err.Error(), "llama-launcher stop") {
		t.Errorf("refusal lacks the stop/--restart guidance: %v", err)
	}
}

// TestServerStatusLines_StartingInstance pins the menu header rendering of
// a Starting instance (ADR-0010): the instance appears with the starting…
// label instead of being invisible, while a healthy instance keeps its
// model detail and gains no label.
func TestServerStatusLines_StartingInstance(t *testing.T) {
	t.Parallel()

	noMem := false
	cfg := &Config{
		Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true},
			"ollama":   {Enabled: true},
		},
		ShowMemoryStatus: &noMem,
	}
	instances := []*RunningInstance{
		{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, Starting: true},
		{Backend: "ollama", Host: "127.0.0.1", Port: 11434, ActiveModel: "llama3"},
	}

	lines := serverStatusLines(cfg, instances)

	var startingLine, healthyLine string
	for _, line := range lines {
		if contains(line, "127.0.0.1:8080") {
			startingLine = line
		}
		if contains(line, "127.0.0.1:11434") {
			healthyLine = line
		}
	}
	if startingLine == "" {
		t.Fatalf("Starting instance missing from header lines: %v", lines)
	}
	if !contains(startingLine, startingLabel) {
		t.Errorf("Starting instance line lacks %q: %q", startingLabel, startingLine)
	}
	if healthyLine == "" {
		t.Fatalf("healthy instance missing from header lines: %v", lines)
	}
	if contains(healthyLine, startingLabel) {
		t.Errorf("healthy instance line wrongly labelled %q: %q", startingLabel, healthyLine)
	}
	if !contains(healthyLine, "llama3") {
		t.Errorf("healthy instance line lost its model detail: %q", healthyLine)
	}
}

// TestStopTargetItems_LabelsStartingInstance pins the stop sub-menu listing
// (ADR-0010): a Starting instance is offered as a stop target and labelled,
// so the user knows the stop kills an in-flight model load.
func TestStopTargetItems_LabelsStartingInstance(t *testing.T) {
	t.Parallel()

	instances := []*RunningInstance{
		{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, Starting: true},
		{Backend: "ollama", Host: "127.0.0.1", Port: 11434, ActiveModel: "llama3"},
	}

	items := stopTargetItems(instances)

	if len(items) != len(instances) {
		t.Fatalf("got %d items, want %d", len(items), len(instances))
	}
	if !contains(items[0].Description, "127.0.0.1:8080") || !contains(items[0].Description, startingLabel) {
		t.Errorf("Starting target not labelled: %+v", items[0])
	}
	if contains(items[1].Description, startingLabel) {
		t.Errorf("healthy target wrongly labelled: %+v", items[1])
	}
}

func TestFormatProfileParams_RedactsAPIKey(t *testing.T) {
	t.Parallel()

	profile := &ResolvedProfile{
		Backend:   "llamacpp",
		ModelPath: "test-model",
		ExtraArgs: []string{"--api-key", "secret", "--no-warmup"},
	}
	lines := formatProfileParams(profile)
	for _, line := range lines {
		if contains(line, "secret") {
			t.Errorf("api key leaked into popup line: %q", line)
		}
	}
	found := false
	for _, line := range lines {
		if contains(line, "--api-key") && contains(line, "***") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected redacted --api-key line, got: %v", lines)
	}
}
