//go:build windows

// The interactive menu on windows, which has no select(2) over a console
// handle and so has never run the menu — before ADR-0012 that showed up as a
// build failure. Here it is a refused verb instead: the menu declines with a
// sentence wrapping ErrUnsupported while every CLI subcommand, and everything
// the library drives over HTTP, keeps working.

package launcher

import (
	"fmt"
	"time"
)

// pollStdin reports that stdin is never ready. Nothing calls it here:
// requireInteractiveMenu turns the menu away before a poll can happen.
func pollStdin(_ time.Duration) bool {
	return false
}

// requireInteractiveMenu refuses the interactive menu on windows. The error
// wraps ErrUnsupported, so a caller can tell "this platform cannot" from a
// menu that ran and failed.
func requireInteractiveMenu() error {
	return fmt.Errorf("the interactive menu is not supported on windows: %w", ErrUnsupported)
}
