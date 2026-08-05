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

func TestFormatContextSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int
		want string
	}{
		{"zero", 0, "0"},
		{"below one thousand", 512, "512"},
		{"four thousand", 4096, "4K"},
		{"sixteen thousand", 16384, "16K"},
		{"thirty-two thousand", 32768, "32K"},
		{"sixty-five thousand", 65536, "65K"},
		{"ninety-eight thousand", 98304, "98K"},
		{"one hundred thirty-one thousand", 131072, "131K"},
		{"one million", 1048576, "1M"},
		{"thousand boundary", 1000, "1K"},
		{"just below the thousand boundary", 999, "999"},
		{"just below the million boundary", 999999, "999K"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatContextSize(tt.size)
			if got != tt.want {
				t.Errorf("formatContextSize(%d) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

// visibleColumnOf returns the visible column at which sub starts inside s, so
// alignment assertions compare rendered columns rather than byte offsets (the
// ★ marker and any multibyte title would drift under len()).
func visibleColumnOf(t *testing.T, s, sub string) int {
	t.Helper()
	i := strings.Index(s, sub)
	if i < 0 {
		t.Fatalf("substring %q not found in %q", sub, s)
	}
	return visibleWidth(s[:i])
}

// TestBuildProfileItems_ContextColumn pins the context-size column of the TUI
// selection menu: llama.cpp rows show their compact merged value, the Ollama
// row stays blank because its LLM Server never receives the parameter, and
// both the [server] tags and the ★ marker keep their columns.
func TestBuildProfileItems_ContextColumn(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Servers: map[string]ServerConfig{
			"llamacpp": {Enabled: true},
			"ollama":   {Enabled: true},
		},
		Profiles: map[string]Profile{
			"big":   {Title: "Big", ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(131072)}},
			"small": {Title: "Small", IsFavourite: true, ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(65536)}},
			"olla":  {Title: "Olla", IsFavourite: true, ProfileParams: ProfileParams{Server: strPtrLocal("ollama"), ContextSize: ptrInt(32768)}},
		},
	}
	names := []string{"big", "small", "olla"}

	items := buildProfileItems(cfg, names)

	if len(items) != len(names) {
		t.Fatalf("got %d items, want %d", len(items), len(names))
	}
	big, small, olla := items[0].Description, items[1].Description, items[2].Description

	// Cells hold the merged value, right-aligned to the widest one; the Ollama
	// row is blank spaces of that same width (its ParamSpecs omit the param).
	wantCells := map[string]string{big: "131K", small: " 65K", olla: "    "}
	for desc, want := range wantCells {
		if got := desc[:visibleColumnOf(t, desc, "[")-len(contextColumnGap)]; got != want {
			t.Errorf("context cell of %q = %q, want %q", desc, got, want)
		}
	}

	// Both tag delimiters keep one column across every row.
	for _, delim := range []string{"[", "]"} {
		want := visibleColumnOf(t, big, delim)
		for _, desc := range []string{small, olla} {
			if got := visibleColumnOf(t, desc, delim); got != want {
				t.Errorf("%q column of %q = %d, want %d", delim, desc, got, want)
			}
		}
	}

	// The ★ marker stays rightmost and aligned across the favourite rows.
	if !strings.HasSuffix(small, "★") || !strings.HasSuffix(olla, "★") {
		t.Errorf("★ is not the rightmost element of the favourite rows: %q / %q", small, olla)
	}
	if got, want := visibleColumnOf(t, olla, "★"), visibleColumnOf(t, small, "★"); got != want {
		t.Errorf("★ column of %q = %d, want %d", olla, got, want)
	}
	if got, want := visibleColumnOf(t, small, "★"), visibleWidth(big)+1; got != want {
		t.Errorf("★ column = %d, want %d (one space past the widest plain row)", got, want)
	}
}

// TestBuildProfileItems_ContextFromDefaults pins Decision D5: the column shows
// the effective value, so a profile without its own context_size inherits the
// one from defaults and an explicit profile value still wins.
func TestBuildProfileItems_ContextFromDefaults(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		Defaults: ProfileParams{ContextSize: ptrInt(32768)},
		Profiles: map[string]Profile{
			"inherits":  {Title: "Inherits", ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp")}},
			"overrides": {Title: "Overrides", ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(131072)}},
		},
	}

	items := buildProfileItems(cfg, []string{"inherits", "overrides"})

	if got, want := items[0].Description, " 32K"; got != want {
		t.Errorf("inherited context cell = %q, want %q", got, want)
	}
	if got, want := items[1].Description, "131K"; got != want {
		t.Errorf("overriding context cell = %q, want %q", got, want)
	}
}

// TestBuildProfileItems_NoContextColumn pins Decision D6's presence gate: with
// no displayable profile the descriptions keep their exact pre-column shape.
func TestBuildProfileItems_NoContextColumn(t *testing.T) {
	t.Parallel()

	t.Run("mixed servers without any context size keep the bare tag", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"llamacpp": {Enabled: true},
				"ollama":   {Enabled: true},
			},
			Profiles: map[string]Profile{
				"cpp":  {ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp")}},
				"olla": {ProfileParams: ProfileParams{Server: strPtrLocal("ollama")}},
			},
		}

		items := buildProfileItems(cfg, []string{"cpp", "olla"})

		if got, want := items[0].Description, "[LLaMA.cpp]"; got != want {
			t.Errorf("description = %q, want %q", got, want)
		}
		if got, want := items[1].Description, "[Ollama   ]"; got != want {
			t.Errorf("description = %q, want %q", got, want)
		}
	})

	t.Run("a context size only Ollama carries does not open the column", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"llamacpp": {Enabled: true},
				"ollama":   {Enabled: true},
			},
			Profiles: map[string]Profile{
				"cpp":  {ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp")}},
				"olla": {ProfileParams: ProfileParams{Server: strPtrLocal("ollama"), ContextSize: ptrInt(65536)}},
			},
		}

		items := buildProfileItems(cfg, []string{"cpp", "olla"})

		if got, want := items[1].Description, "[Ollama   ]"; got != want {
			t.Errorf("description = %q, want %q", got, want)
		}
	})

	t.Run("single server without context size keeps empty descriptions", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{"llamacpp": {Enabled: true}},
			Profiles: map[string]Profile{
				"cpp": {ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp")}},
			},
		}

		items := buildProfileItems(cfg, []string{"cpp"})

		if got := items[0].Description; got != "" {
			t.Errorf("description = %q, want empty", got)
		}
	})
}

// TestBuildProfileItems_SingleServerContextOnly pins the single-enabled-server
// case: with no [server] tag column the context cell is the whole description.
func TestBuildProfileItems_SingleServerContextOnly(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Servers: map[string]ServerConfig{"lmstudio": {Enabled: true}},
		Profiles: map[string]Profile{
			"big":   {ProfileParams: ProfileParams{Server: strPtrLocal("lmstudio"), ContextSize: ptrInt(131072)}},
			"small": {ProfileParams: ProfileParams{Server: strPtrLocal("lmstudio"), ContextSize: ptrInt(4096)}},
		},
	}

	items := buildProfileItems(cfg, []string{"big", "small"})

	for i, want := range []string{"131K", "  4K"} {
		if got := items[i].Description; got != want {
			t.Errorf("description = %q, want %q", got, want)
		}
	}
}

// TestBuildSimpleProfileLines_ContextColumn pins the context-size column of the
// non-TTY numbered list: same merged values, right-alignment and ParamSpecs
// gate as the TUI menu, with the [server] tag and ★ columns still aligned.
func TestBuildSimpleProfileLines_ContextColumn(t *testing.T) {
	t.Parallel()

	t.Run("mixed servers keep every column aligned", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"llamacpp": {Enabled: true},
				"ollama":   {Enabled: true},
			},
			Profiles: map[string]Profile{
				"big":   {Title: "Big", ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(131072)}},
				"small": {Title: "Small", IsFavourite: true, ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(65536)}},
				"olla":  {Title: "Olla", IsFavourite: true, ProfileParams: ProfileParams{Server: strPtrLocal("ollama"), ContextSize: ptrInt(32768)}},
			},
		}
		names := []string{"big", "small", "olla"}

		lines := buildSimpleProfileLines(cfg, names)

		// Cells hold the merged value right-aligned to the widest one; the
		// Ollama row is blank because its ParamSpecs omit the parameter.
		want := []string{
			"Big    131K  [LLaMA.cpp]",
			"Small   65K  [LLaMA.cpp] ★",
			"Olla         [Ollama   ] ★",
		}
		if len(lines) != len(want) {
			t.Fatalf("got %d lines, want %d", len(lines), len(want))
		}
		for i := range want {
			if lines[i] != want[i] {
				t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
			}
		}

		// The tag delimiters and the ★ marker each keep one column.
		for _, marker := range []string{"[", "]"} {
			for _, line := range lines[1:] {
				if got, want := visibleColumnOf(t, line, marker), visibleColumnOf(t, lines[0], marker); got != want {
					t.Errorf("%q column of %q = %d, want %d", marker, line, got, want)
				}
			}
		}
		if got, want := visibleColumnOf(t, lines[2], "★"), visibleColumnOf(t, lines[1], "★"); got != want {
			t.Errorf("★ column of %q = %d, want %d", lines[2], got, want)
		}
	})

	t.Run("no displayable profile keeps the pre-column shape", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"llamacpp": {Enabled: true},
				"ollama":   {Enabled: true},
			},
			Profiles: map[string]Profile{
				"cpp":  {ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp")}},
				"olla": {ProfileParams: ProfileParams{Server: strPtrLocal("ollama"), ContextSize: ptrInt(65536)}},
			},
		}

		lines := buildSimpleProfileLines(cfg, []string{"cpp", "olla"})

		for i, want := range []string{"cpp   [LLaMA.cpp]", "olla  [Ollama   ]"} {
			if lines[i] != want {
				t.Errorf("line %d = %q, want %q", i, lines[i], want)
			}
		}
	})

	t.Run("single enabled server shows the cell without a tag", func(t *testing.T) {
		t.Parallel()
		cfg := &Config{
			Servers: map[string]ServerConfig{"lmstudio": {Enabled: true}},
			Profiles: map[string]Profile{
				"big":   {ProfileParams: ProfileParams{Server: strPtrLocal("lmstudio"), ContextSize: ptrInt(131072)}},
				"small": {ProfileParams: ProfileParams{Server: strPtrLocal("lmstudio"), ContextSize: ptrInt(4096)}},
			},
		}

		lines := buildSimpleProfileLines(cfg, []string{"big", "small"})

		for i, want := range []string{"big    131K", "small    4K"} {
			if lines[i] != want {
				t.Errorf("line %d = %q, want %q", i, lines[i], want)
			}
		}
	})
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

// TestModelDisplayName pins the render-time shortening rule: path-shaped
// server ids collapse to their base name, while ids that are names rather
// than paths (LM Studio, Ollama) survive verbatim.
func TestModelDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want string
	}{
		{"absolute gguf path", "/Users/airic/LL-Models/Qwen/qwen3.6-35B-A3B-Q4_K_M.gguf", "qwen3.6-35B-A3B-Q4_K_M.gguf"},
		{"relative gguf path", "Qwen/qwen3.6-35B-A3B-Q4_K_M.gguf", "qwen3.6-35B-A3B-Q4_K_M.gguf"},
		{"upper-case extension", "/models/Qwen/Model.GGUF", "Model.GGUF"},
		{"lm studio style id", "qwen/qwen3-8b", "qwen/qwen3-8b"},
		{"ollama style id", "llama3:8b", "llama3:8b"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := modelDisplayName(tt.id); got != tt.want {
				t.Errorf("modelDisplayName(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

// TestServerStatusLines_ShortensModelPath pins the menu header: an instance
// with no matching profile falls back to its model id, and that fallback is
// rendered as a file name rather than the absolute path llama-server reports.
func TestServerStatusLines_ShortensModelPath(t *testing.T) {
	t.Parallel()

	noMem := false
	cfg := &Config{
		Servers:          map[string]ServerConfig{"llamacpp": {Enabled: true}},
		ShowMemoryStatus: &noMem,
	}
	instances := []*RunningInstance{
		{Backend: "llamacpp", Host: "0.0.0.0", Port: 1111, ActiveModel: "/Users/airic/LL-Models/Qwen/qwen3.6-35B-A3B-Q4_K_M.gguf"},
	}

	lines := serverStatusLines(cfg, instances)

	var modelLine string
	for _, line := range lines {
		if contains(line, "0.0.0.0:1111") {
			modelLine = line
		}
	}
	if modelLine == "" {
		t.Fatalf("instance missing from header lines: %v", lines)
	}
	if !contains(modelLine, "qwen3.6-35B-A3B-Q4_K_M.gguf") {
		t.Errorf("header line lost the model file name: %q", modelLine)
	}
	if contains(modelLine, "/Users/airic/LL-Models/Qwen") {
		t.Errorf("header line still carries the directory portion: %q", modelLine)
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
