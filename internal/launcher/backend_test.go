package launcher

import (
	"strings"
	"testing"
)

func TestGetBackend_Known(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"llamacpp", "lmstudio", "ollama"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b, err := GetBackend(name)
			if err != nil {
				t.Fatalf("GetBackend(%q) error: %v", name, err)
			}
			if b.Name() != name {
				t.Errorf("Name() = %q, want %q", b.Name(), name)
			}
		})
	}
}

func TestGetBackend_Unknown(t *testing.T) {
	t.Parallel()

	_, err := GetBackend("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("error = %q, want it to contain 'unknown backend'", err)
	}
}
