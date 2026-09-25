package launcher

import (
	"errors"
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
	authFailed := &RunningInstance{Host: "127.0.0.1", Port: 9000, AuthFailed: true}
	startingLlamaCpp := &RunningInstance{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, Starting: true}

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
		{"auth-failed row sorts first, Starting llamacpp wins", []*RunningInstance{authFailed, startingLlamaCpp}, startingLlamaCpp},
		{"auth-failed row alone", []*RunningInstance{authFailed}, authFailed},
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

// TestServerStatusLines_AuthFailedInstance pins the menu header rendering
// of a server refusing the configured api_key: its Backend "" matches no
// enabled backend's line, so it gets a line of its own carrying the shared
// auth-failed label, while the enabled backends keep their lines.
func TestServerStatusLines_AuthFailedInstance(t *testing.T) {
	t.Parallel()

	noMem := false
	cfg := &Config{
		Servers:          map[string]ServerConfig{"llamacpp": {Enabled: true}},
		ShowMemoryStatus: &noMem,
	}
	instances := []*RunningInstance{
		{Host: "127.0.0.1", Port: 9090, AuthFailed: true},
		{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, ActiveModel: "llama3"},
	}

	lines := serverStatusLines(cfg, instances)

	want := "auth failed at 127.0.0.1:9090 — check api_key in the servers section"
	var authLine string
	for _, line := range lines {
		if contains(line, "127.0.0.1:9090") {
			authLine = line
		}
	}
	if !contains(authLine, want) {
		t.Errorf("header lacks %q: %v", want, lines)
	}
	if contains(authLine, "llama3") || contains(authLine, "stopped") {
		t.Errorf("auth-failed line renders a model or a stopped state: %q", authLine)
	}
}

// TestStopTargetItems_LabelsAuthFailedInstance pins the stop sub-menu row
// of a server refusing the configured api_key: the shared auth-failed
// label, never an empty backend name beside the bare address.
func TestStopTargetItems_LabelsAuthFailedInstance(t *testing.T) {
	t.Parallel()

	instances := []*RunningInstance{
		{Host: "127.0.0.1", Port: 9090, AuthFailed: true},
		{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, ActiveModel: "llama3"},
	}

	items := stopTargetItems(instances)

	if len(items) != len(instances) {
		t.Fatalf("got %d items, want %d", len(items), len(instances))
	}
	want := "auth failed at 127.0.0.1:9090 — check api_key in the servers section"
	if items[0].Label != want {
		t.Errorf("auth-failed row label = %q, want %q", items[0].Label, want)
	}
	if items[1].Label == "" || contains(items[1].Label, "auth failed") {
		t.Errorf("identified row label = %q, want its backend name", items[1].Label)
	}
}

// setEditConfigGOOS points editConfigCommand at goos for the rest of the
// test. It swaps a package variable, so callers must not call t.Parallel().
func setEditConfigGOOS(t *testing.T, goos string) {
	t.Helper()

	original := editConfigGOOS
	editConfigGOOS = goos
	t.Cleanup(func() { editConfigGOOS = original })
}

// withStdin feeds input to os.Stdin for the rest of the test, so a numbered
// menu's readLine returns without a terminal. It swaps process state, so
// callers must not call t.Parallel().
func withStdin(t *testing.T, input string) {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	if _, err := writer.WriteString(input); err != nil {
		t.Fatalf("writing stdin: %v", err)
	}
	writer.Close()

	original := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = original
		reader.Close()
	})
}

// TestEditConfigCommand pins which command "Edit config" runs: `open` on
// darwin, and elsewhere $VISUAL before $EDITOR, split on whitespace with the
// config path appended and the terminal attached. Neither set (or both
// empty) means there is no command at all.
func TestEditConfigCommand(t *testing.T) {
	const path = "/home/user/.config/llama-launcher/config.yaml"

	tests := []struct {
		name     string
		goos     string
		visual   string
		editor   string
		wantArgs []string
	}{
		{"darwin opens the file whatever the editor", "darwin", "", "vim", []string{"open", path}},
		{"linux splits EDITOR on whitespace", "linux", "", "code -w", []string{"code", "-w", path}},
		{"linux prefers VISUAL over EDITOR", "linux", "nvim", "nano", []string{"nvim", path}},
		{"linux treats a blank VISUAL as unset", "linux", "   ", "nano", []string{"nano", path}},
		{"linux with neither set has no command", "linux", "", "", nil},
		{"windows with neither set has no command", "windows", "", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEditConfigGOOS(t, tc.goos)
			t.Setenv("VISUAL", tc.visual)
			t.Setenv("EDITOR", tc.editor)

			cmd, ok := editConfigCommand(path)

			if tc.wantArgs == nil {
				if ok || cmd != nil {
					t.Fatalf("editConfigCommand = (%v, %v), want (nil, false)", cmd, ok)
				}
				return
			}
			if !ok {
				t.Fatal("editConfigCommand reported no command")
			}
			if got, want := strings.Join(cmd.Args, " "), strings.Join(tc.wantArgs, " "); got != want {
				t.Errorf("args = %q, want %q", got, want)
			}
		})
	}
}

// TestEditConfigCommand_TerminalEditorOwnsTheTerminal pins that a terminal
// editor inherits stdin/stdout/stderr, so it can draw and the menu waits
// for it.
func TestEditConfigCommand_TerminalEditorOwnsTheTerminal(t *testing.T) {
	setEditConfigGOOS(t, "linux")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "vi")

	cmd, ok := editConfigCommand("/tmp/config.yaml")

	if !ok {
		t.Fatal("editConfigCommand reported no command")
	}
	if cmd.Stdin != os.Stdin || cmd.Stdout != os.Stdout || cmd.Stderr != os.Stderr {
		t.Error("the editor does not inherit the terminal")
	}
}

// TestEditConfig_DoEditConfigRefusesWithoutEditor pins the refusal when the verb is reached
// with no editor: an error naming the fix, never a failed exec.
func TestEditConfig_DoEditConfigRefusesWithoutEditor(t *testing.T) {
	setEditConfigGOOS(t, "linux")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")

	err := doEditConfig(&Config{ConfigPath: "/tmp/config.yaml"})

	if !errors.Is(err, errNoEditor) {
		t.Errorf("doEditConfig = %v, want errNoEditor", err)
	}
}

// editMenuConfig is a two-Profile config for the menu-variant tests.
func editMenuConfig() *Config {
	return &Config{
		ConfigPath: "/tmp/config.yaml",
		Servers:    map[string]ServerConfig{"llamacpp": {Enabled: true}},
		Profiles: map[string]Profile{
			"big":   {Title: "Big", ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp")}},
			"small": {Title: "Small", ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp")}},
		},
	}
}

// menuLabels lists the labels of items, separators left out.
func menuLabels(items []menuItem) []string {
	labels := make([]string, 0, len(items))
	for _, item := range items {
		if !item.Separator {
			labels = append(labels, item.Label)
		}
	}
	return labels
}

// TestMenuItems_EditConfigOnlyWithAnEditor pins that every TUI menu variant
// offers "Edit config" on darwin and with an editor set, and leaves it out
// where neither `open` nor $VISUAL/$EDITOR can open the file.
func TestMenuItems_EditConfigOnlyWithAnEditor(t *testing.T) {
	inst := &RunningInstance{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, LogFile: "/tmp/server.log"}
	builders := map[string]func(cfg *Config) []menuItem{
		"stopped": func(cfg *Config) []menuItem { return stoppedMenuItems(cfg, cfg.ProfileNames(), true) },
		"loaded":  func(cfg *Config) []menuItem { return loadedMenuItems(cfg, inst) },
		"idle":    func(cfg *Config) []menuItem { return idleMenuItems(cfg, inst, cfg.ProfileNames()) },
	}
	platforms := []struct {
		name     string
		goos     string
		editor   string
		wantEdit bool
	}{
		{"darwin", "darwin", "", true},
		{"linux with EDITOR", "linux", "vi", true},
		{"linux without an editor", "linux", "", false},
	}
	for _, platform := range platforms {
		for variant, build := range builders {
			t.Run(platform.name+"/"+variant, func(t *testing.T) {
				setEditConfigGOOS(t, platform.goos)
				t.Setenv("VISUAL", "")
				t.Setenv("EDITOR", platform.editor)

				labels := menuLabels(build(editMenuConfig()))

				hasEdit := strings.Contains(strings.Join(labels, "|"), "Edit config")
				if hasEdit != platform.wantEdit {
					t.Errorf("items %q: Edit config offered = %v, want %v", labels, hasEdit, platform.wantEdit)
				}
			})
		}
	}
}

// TestMenuSimple_EditConfigOnlyWithAnEditor pins the same rule for the
// numbered fallbacks: with no editor neither the "Edit config" line nor the
// `e` key in the Select prompt appears; with one, both do.
func TestMenuSimple_EditConfigOnlyWithAnEditor(t *testing.T) {
	inst := &RunningInstance{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080}
	variants := []struct {
		name       string
		run        func(cfg *Config) error
		withEdit   string
		withoutKey string
	}{
		{
			name:       "stopped",
			run:        func(cfg *Config) error { return runStoppedMenuSimple(cfg, cfg.ProfileNames()) },
			withEdit:   "Select [1-2, e, q]: ",
			withoutKey: "Select [1-2, q]: ",
		},
		{
			name:       "loaded",
			run:        func(cfg *Config) error { return runLoadedMenuSimple(cfg, inst) },
			withEdit:   "    5  Edit config\n",
			withoutKey: "Select [1-4, q]: ",
		},
		{
			name:       "idle",
			run:        func(cfg *Config) error { return runIdleMenuSimple(cfg, inst, cfg.ProfileNames()) },
			withEdit:   "Select [1-2, s, e, q]: ",
			withoutKey: "Select [1-2, s, q]: ",
		},
	}
	for _, variant := range variants {
		t.Run(variant.name+"/with EDITOR", func(t *testing.T) {
			setEditConfigGOOS(t, "linux")
			t.Setenv("VISUAL", "")
			t.Setenv("EDITOR", "vi")
			withStdin(t, "q\n")

			var err error
			out := captureStdout(t, func() { err = variant.run(editMenuConfig()) })

			if err != nil {
				t.Fatalf("menu returned %v", err)
			}
			if !strings.Contains(out, variant.withEdit) {
				t.Errorf("output lacks %q:\n%s", variant.withEdit, out)
			}
		})
		t.Run(variant.name+"/without an editor", func(t *testing.T) {
			setEditConfigGOOS(t, "linux")
			t.Setenv("VISUAL", "")
			t.Setenv("EDITOR", "")
			withStdin(t, "q\n")

			var err error
			out := captureStdout(t, func() { err = variant.run(editMenuConfig()) })

			if err != nil {
				t.Fatalf("menu returned %v", err)
			}
			if strings.Contains(out, "Edit config") {
				t.Errorf("output offers Edit config:\n%s", out)
			}
			if !strings.Contains(out, variant.withoutKey) {
				t.Errorf("output lacks %q:\n%s", variant.withoutKey, out)
			}
		})
	}
}

// TestMenuSimple_EKeyIgnoredWithoutAnEditor pins that typing `e` where the
// verb is not offered is an invalid selection, not an attempt to edit.
func TestMenuSimple_EKeyIgnoredWithoutAnEditor(t *testing.T) {
	setEditConfigGOOS(t, "linux")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	withStdin(t, "e\n")

	var err error
	_ = captureStdout(t, func() { err = runStoppedMenuSimple(editMenuConfig(), []string{"big", "small"}) })

	if err == nil || !strings.Contains(err.Error(), "invalid selection") {
		t.Errorf("runStoppedMenuSimple = %v, want an invalid-selection error", err)
	}
}

// scriptedWatch builds a stillLoadingWatch whose probes answer from the
// given per-poll scripts (the last answer repeats) and whose key reader
// returns keys in order, then keyNone. Raw mode is a no-op that records its
// restore.
func scriptedWatch(healthy, starting, alive []bool, keys []keyCode, restored *bool) stillLoadingWatch {
	at := func(script []bool, poll int) bool {
		return script[min(poll, len(script)-1)]
	}
	polls := 0
	return stillLoadingWatch{
		healthy:  func() bool { polls++; return at(healthy, polls-1) },
		starting: func() bool { return at(starting, polls-1) },
		alive:    func() bool { return at(alive, polls-1) },
		readKey: func(time.Duration) keyCode {
			if len(keys) == 0 {
				return keyNone
			}
			key := keys[0]
			keys = keys[1:]
			return key
		},
		draw:     func() {},
		enterRaw: func() (func(), error) { return func() { *restored = true }, nil },
	}
}

// TestWaitStillLoading_HealthyOnThirdPoll verifies the popup reports success
// once the server turns healthy, after two still-starting polls.
func TestWaitStillLoading_HealthyOnThirdPoll(t *testing.T) {
	t.Parallel()
	restored := false
	watch := scriptedWatch([]bool{false, false, true}, []bool{true}, []bool{true}, nil, &restored)

	healthy, err := waitStillLoading(watch, errors.New("timed out"))

	if !healthy || err != nil {
		t.Errorf("waitStillLoading = (%v, %v), want (true, nil)", healthy, err)
	}
	if !restored {
		t.Error("raw mode was not restored")
	}
}

// TestWaitStillLoading_GoneTwice verifies a server that is neither healthy,
// starting nor alive on two polls in a row ends the wait with the timeout
// text plus the no-longer-running line, while a single miss does not.
func TestWaitStillLoading_GoneTwice(t *testing.T) {
	t.Parallel()
	restored := false
	timeoutErr := errors.New("server startup timed out: left running")
	watch := scriptedWatch([]bool{false}, []bool{false, true, false}, []bool{false}, nil, &restored)

	healthy, err := waitStillLoading(watch, timeoutErr)

	if healthy {
		t.Fatal("waitStillLoading reported healthy for a gone server")
	}
	if err == nil || !strings.Contains(err.Error(), timeoutErr.Error()) || !strings.Contains(err.Error(), "no longer running") {
		t.Errorf("error = %v, want the timeout text and %q", err, stillLoadingGoneText)
	}
	if !restored {
		t.Error("raw mode was not restored")
	}
}

// TestWaitStillLoading_ReturnKeysLeaveServer verifies Esc, Ctrl+C and q each
// return to the menu with no error, while the server is still loading.
func TestWaitStillLoading_ReturnKeysLeaveServer(t *testing.T) {
	t.Parallel()
	for _, key := range []keyCode{keyEscape, keyCtrlC, keyQ} {
		restored := false
		watch := scriptedWatch([]bool{false}, []bool{true}, []bool{true}, []keyCode{keyNone, key}, &restored)

		healthy, err := waitStillLoading(watch, errors.New("timed out"))

		if healthy || err != nil {
			t.Errorf("key %d: waitStillLoading = (%v, %v), want (false, nil)", key, healthy, err)
		}
		if !restored {
			t.Errorf("key %d: raw mode was not restored", key)
		}
	}
}

// TestDoLoadProfile_RoutesStartupTimeout verifies the post-LoadProfile
// branching: only a managed startup timeout in terminal mode reaches the
// still-loading waiter, with its address and PID; the external timeout and
// every non-terminal error pass through unchanged.
func TestDoLoadProfile_RoutesStartupTimeout(t *testing.T) {
	t.Parallel()
	managed := startupTimeoutErr(errors.New("health wait timed out"), &RunningInstance{
		Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, PID: 4242, LogFile: "/logs/llamacpp.log",
	})
	external := externalStartupTimeoutErr(errors.New("health wait timed out"), &RunningInstance{
		Backend: "ollama", Host: "127.0.0.1", Port: 11434, PID: 4242,
	})
	tests := []struct {
		name       string
		err        error
		terminal   bool
		wantWaited bool
	}{
		{name: "non-terminal managed timeout", err: managed, terminal: false},
		{name: "terminal external timeout", err: external, terminal: true},
		{name: "terminal managed timeout", err: managed, terminal: true, wantWaited: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var waited *startupTimeout
			waiter := func(_ error, st startupTimeout) error {
				waited = &st
				return nil
			}

			got := routeLoadError(tc.err, tc.terminal, waiter)

			if !tc.wantWaited {
				if waited != nil || got != tc.err {
					t.Errorf("waiter called = %v, error = %v; want the error unchanged and no wait", waited != nil, got)
				}
				return
			}
			if waited == nil || waited.addr != "127.0.0.1:8080" || waited.pid != 4242 {
				t.Errorf("waiter got %+v, want addr 127.0.0.1:8080 and PID 4242", waited)
			}
		})
	}
}
