package launcher

import (
	"strings"
	"testing"
)

func TestGetLLMServer_Known(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"llamacpp", "lmstudio", "ollama"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b, err := GetLLMServer(name)
			if err != nil {
				t.Fatalf("GetLLMServer(%q) error: %v", name, err)
			}
			if b.Name() != name {
				t.Errorf("Name() = %q, want %q", b.Name(), name)
			}
		})
	}
}

func TestGetLLMServer_Unknown(t *testing.T) {
	t.Parallel()

	_, err := GetLLMServer("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown LLM server")
	}
	if !strings.Contains(err.Error(), "unknown LLM server") {
		t.Errorf("error = %q, want it to contain 'unknown LLM server'", err)
	}
}
