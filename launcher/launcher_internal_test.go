// This is the facade's one same-package test file. It holds the re-exported
// sentinels against the core values they alias — the one check no external
// test can make, because a client module cannot import internal/. Everything
// a client can actually reach is tested from the outside, in launcher_test.go.
package launcher

import (
	"errors"
	"fmt"
	"testing"

	core "github.com/airiclenz/llama-launcher/internal/launcher"
)

// TestNewSentinels_AliasTheCoreValues pins the re-export-by-value discipline
// for the two sentinels ADR-0012 adds. Neither is reachable through a facade
// verb in a test — ErrStartupTimeout needs a managed server to be forked and
// then miss its health window (driven against the real wait loop in
// internal/launcher's TestLoadProfile_StartupTimeoutIsErrStartupTimeout), and
// ErrUnsupported only ever comes back from a windows build — so the boundary
// proof is identity: aliasing the core value is what lets errors.Is find the
// sentinel through an error the core wrapped. A sentinel re-declared here
// with errors.New would read the same in godoc and match nothing.
func TestNewSentinels_AliasTheCoreValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		facade error
		want   error
	}{
		{"ErrStartupTimeout", ErrStartupTimeout, core.ErrStartupTimeout},
		{"ErrUnsupported", ErrUnsupported, core.ErrUnsupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.facade != tc.want {
				t.Fatalf("launcher.%s = %v, want the core sentinel %v", tc.name, tc.facade, tc.want)
			}
			if err := fmt.Errorf("activating profile: %w", tc.want); !errors.Is(err, tc.facade) {
				t.Errorf("errors.Is does not find launcher.%s through a core error that wraps it", tc.name)
			}
		})
	}
}
