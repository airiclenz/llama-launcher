//go:build !darwin

package launcher

import (
	"errors"
	"testing"
)

func TestLaunchAgent_RefusedWithoutLaunchd(t *testing.T) {
	_, installErr := installLaunchAgent(launchAgentSpec{Label: launchAgentLabelPrefix + "chat"})
	_, _, removeErr := removeLaunchAgent(launchAgentLabelPrefix + "chat")

	if !errors.Is(installErr, ErrUnsupported) {
		t.Errorf("installLaunchAgent = %v, want it to wrap ErrUnsupported", installErr)
	}
	if !errors.Is(removeErr, ErrUnsupported) {
		t.Errorf("removeLaunchAgent = %v, want it to wrap ErrUnsupported", removeErr)
	}
}

// TestCmdAutostart_InstallRemoveExit3WithoutLaunchd: both verbs exit 3 for an
// existing Profile and never run launchctl. Not parallel: isolateAutostart
// swaps HOME and runLaunchctl.
func TestCmdAutostart_InstallRemoveExit3WithoutLaunchd(t *testing.T) {
	_, calls := isolateAutostart(t, launchctlSucceeds)
	cfgPath := writeRunConfig(t)

	for _, subcommand := range []string{"install", "remove"} {
		_, code := runCLI(t, cfgPath, "autostart", subcommand, "chat")

		if code != 3 {
			t.Errorf("autostart %s = exit %d, want 3", subcommand, code)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("launchctl calls = %v, want none", *calls)
	}
}
