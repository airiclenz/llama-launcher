//go:build !darwin

// The launchd half of `autostart` on every platform without launchd: both
// verbs refuse with an error wrapping ErrUnsupported (ADR-0012), while
// `autostart print`, which needs no launchd, keeps working.

package launcher

import "fmt"

// installLaunchAgent refuses: there is no launchd to install an agent into.
func installLaunchAgent(_ launchAgentSpec) (string, error) {
	return "", fmt.Errorf("installing a launchd agent needs macOS: %w", ErrUnsupported)
}

// removeLaunchAgent refuses: there is no launchd to remove an agent from.
func removeLaunchAgent(_ string) (string, bool, error) {
	return "", false, fmt.Errorf("removing a launchd agent needs macOS: %w", ErrUnsupported)
}
