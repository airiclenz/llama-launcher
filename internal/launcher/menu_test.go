package launcher

import (
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

func TestFormatProfileParams_GPULayers_LMStudio(t *testing.T) {
	t.Parallel()

	// LM Studio's REST load endpoint has no GPU-offload field, so the popup
	// must not present gpu_layers as active for lmstudio profiles.
	layers := 99
	profile := &ResolvedProfile{
		Backend:       "lmstudio",
		ModelPath:     "test-model",
		ProfileParams: ProfileParams{GPULayers: &layers},
	}
	for _, line := range formatProfileParams(profile) {
		if contains(line, "GPU") {
			t.Errorf("expected no GPU line for lmstudio profile, got: %q", line)
		}
	}
}

func TestFormatProfileParams_GPULayers_LlamaCpp(t *testing.T) {
	t.Parallel()

	layers := 50
	profile := &ResolvedProfile{
		Backend:       "llamacpp",
		ModelPath:     "test-model",
		ProfileParams: ProfileParams{GPULayers: &layers},
	}
	lines := formatProfileParams(profile)
	for _, line := range lines {
		if contains(line, "GPU layers") && contains(line, "50") {
			return
		}
	}
	t.Errorf("expected GPU layers line with value 50, got lines: %v", lines)
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
