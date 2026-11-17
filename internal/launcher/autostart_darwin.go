//go:build darwin

// The launchd half of `autostart`: installing and removing the agent that
// autostart.go renders, in the logged-in user's GUI domain. Only darwin has
// launchd; autostart_other.go refuses both verbs elsewhere.

package launcher

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// launchAgentsDirMode and launchAgentPlistMode are the permissions of a
// LaunchAgents directory the launcher creates and of the plist it writes —
// launchd's own conventions; neither holds a secret.
const (
	launchAgentsDirMode  = 0o755
	launchAgentPlistMode = 0o644
)

// autostartLogDirMode matches the log directory createLogPath creates.
const autostartLogDirMode = 0o700

// launchctlNotLoadedExitCodes are the exit codes `launchctl bootout` gives for
// a service that is not loaded: ESRCH (3) and launchd's "service not found"
// (113).
var launchctlNotLoadedExitCodes = map[int]bool{3: true, 113: true}

// launchctlNotLoadedMessages are the phrases `launchctl bootout` prints for a
// service that is not loaded, whatever exit code a launchctl release pairs
// them with.
var launchctlNotLoadedMessages = []string{"No such process", "Could not find specified service"}

// installLaunchAgent writes spec's plist to ~/Library/LaunchAgents and
// bootstraps it in the user's GUI domain, returning the plist's path. It
// creates the LaunchAgents directory and the log directory when missing — the
// job writes its log before any `load` could create one — and boots out an
// agent already loaded under the label first, so a second install replaces
// the first. RunAtLoad makes the bootstrap run `load` at once: the Profile
// activates now, replacing whatever holds its address.
func installLaunchAgent(spec launchAgentSpec) (string, error) {
	path, err := launchAgentPath(spec.Label)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), launchAgentsDirMode); err != nil {
		return "", fmt.Errorf("creating the LaunchAgents directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), autostartLogDirMode); err != nil {
		return "", fmt.Errorf("creating log directory: %w", err)
	}

	if err := bootoutLaunchAgent(spec.Label); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(renderLaunchAgentPlist(spec)), launchAgentPlistMode); err != nil {
		return "", fmt.Errorf("writing the launch agent: %w", err)
	}
	if err := launchctl("bootstrap", launchdGUIDomain(), path); err != nil {
		return "", err
	}
	return path, nil
}

// removeLaunchAgent boots out the agent labelled label and deletes its plist,
// returning the plist's path and whether a plist was there. An agent that is
// not loaded, or has no plist, is not an error. The server its `load` started
// is not stopped.
func removeLaunchAgent(label string) (path string, wasInstalled bool, err error) {
	path, err = launchAgentPath(label)
	if err != nil {
		return "", false, err
	}
	if err := bootoutLaunchAgent(label); err != nil {
		return path, false, err
	}

	err = os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, false, nil
	}
	if err != nil {
		return path, false, fmt.Errorf("deleting the launch agent: %w", err)
	}
	return path, true, nil
}

// launchAgentPath returns where the agent labelled label lives:
// ~/Library/LaunchAgents/<label>.plist, the directory launchd reads at login.
func launchAgentPath(label string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the LaunchAgents directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

// launchdGUIDomain returns the launchd domain of the logged-in user's
// session, the one agents in ~/Library/LaunchAgents load into.
func launchdGUIDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

// bootoutLaunchAgent unloads the agent labelled label, treating an agent that
// is not loaded as already done.
func bootoutLaunchAgent(label string) error {
	output, exitCode, err := runLaunchctl("bootout", launchdGUIDomain()+"/"+label)
	if err != nil {
		return err
	}
	if exitCode == 0 || isLaunchctlNotLoaded(output, exitCode) {
		return nil
	}
	return launchctlFailure("bootout", output, exitCode)
}

// isLaunchctlNotLoaded reports whether a failed bootout said the service was
// not loaded.
func isLaunchctlNotLoaded(output string, exitCode int) bool {
	if launchctlNotLoadedExitCodes[exitCode] {
		return true
	}
	for _, message := range launchctlNotLoadedMessages {
		if strings.Contains(output, message) {
			return true
		}
	}
	return false
}

// launchctl runs `launchctl <args>` and returns an error carrying its output
// when it exits non-zero.
func launchctl(args ...string) error {
	output, exitCode, err := runLaunchctl(args...)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return launchctlFailure(args[0], output, exitCode)
	}
	return nil
}

// launchctlFailure describes a launchctl subcommand that exited non-zero.
func launchctlFailure(subcommand, output string, exitCode int) error {
	return fmt.Errorf("launchctl %s exited %d: %s", subcommand, exitCode, strings.TrimSpace(output))
}
