//go:build darwin

package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testAutostartLabel is the launchd label the darwin autostart tests install.
const testAutostartLabel = launchAgentLabelPrefix + "chat"

// launchctlReply is what a fake launchctl answers one subcommand with.
type launchctlReply struct {
	output   string
	exitCode int
}

// launchctlReplying returns a fake launchctl that answers each subcommand
// with its reply in replies, and exits 0 for any other.
func launchctlReplying(replies map[string]launchctlReply) launchctlRunner {
	return func(args ...string) (string, int, error) {
		reply := replies[args[0]]
		return reply.output, reply.exitCode, nil
	}
}

// testLaunchAgentSpec returns an agent whose log sits in a log directory
// under home that does not exist yet.
func testLaunchAgentSpec(home string) launchAgentSpec {
	return launchAgentSpec{
		Label:            testAutostartLabel,
		ProgramArguments: []string{"/usr/local/bin/llama-launcher", "load", "chat"},
		LogPath:          filepath.Join(home, "logs", "autostart-chat.log"),
	}
}

// wantPlistPath is where the agent labelled testAutostartLabel lives under home.
func wantPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", testAutostartLabel+".plist")
}

// wantBootout is the bootout call for testAutostartLabel.
func wantBootout() []string {
	return []string{"bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), testAutostartLabel)}
}

func TestInstallLaunchAgent_CreatesDirectoriesWritesPlistAndBootstraps(t *testing.T) {
	home, calls := isolateAutostart(t, launchctlSucceeds)
	spec := testLaunchAgentSpec(home)

	path, err := installLaunchAgent(spec)

	if err != nil {
		t.Fatalf("installLaunchAgent = %v, want nil", err)
	}
	if path != wantPlistPath(home) {
		t.Errorf("path = %q, want %q", path, wantPlistPath(home))
	}
	written, err := os.ReadFile(wantPlistPath(home))
	if err != nil || string(written) != renderLaunchAgentPlist(spec) {
		t.Errorf("plist = %q (err %v), want the rendered spec", written, err)
	}
	if info, err := os.Stat(filepath.Dir(spec.LogPath)); err != nil || !info.IsDir() {
		t.Errorf("log directory %s not created (err %v)", filepath.Dir(spec.LogPath), err)
	}
	want := [][]string{wantBootout(), {"bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), wantPlistPath(home)}}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("launchctl calls = %v, want %v", *calls, want)
	}
}

func TestInstallLaunchAgent_ReplacesAnExistingPlist(t *testing.T) {
	home, _ := isolateAutostart(t, launchctlSucceeds)
	spec := testLaunchAgentSpec(home)
	if err := os.MkdirAll(filepath.Dir(wantPlistPath(home)), 0o755); err != nil {
		t.Fatalf("creating LaunchAgents: %v", err)
	}
	if err := os.WriteFile(wantPlistPath(home), []byte("stale"), 0o644); err != nil {
		t.Fatalf("writing stale plist: %v", err)
	}

	_, err := installLaunchAgent(spec)

	written, readErr := os.ReadFile(wantPlistPath(home))
	if err != nil || readErr != nil || string(written) != renderLaunchAgentPlist(spec) {
		t.Errorf("installLaunchAgent = %v; plist = %q (err %v), want the rendered spec", err, written, readErr)
	}
}

func TestInstallLaunchAgent_ToleratesANotLoadedBootout(t *testing.T) {
	cases := []struct {
		name  string
		reply launchctlReply
	}{
		{"exit 3, No such process", launchctlReply{"Boot-out failed: 3: No such process", 3}},
		{"exit 113", launchctlReply{"Boot-out failed: 113: Could not find specified service", 113}},
		{"the message alone", launchctlReply{"Could not find specified service", 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, calls := isolateAutostart(t, launchctlReplying(map[string]launchctlReply{"bootout": c.reply}))

			_, err := installLaunchAgent(testLaunchAgentSpec(home))

			if err != nil {
				t.Fatalf("installLaunchAgent = %v, want nil", err)
			}
			if len(*calls) != 2 || (*calls)[1][0] != "bootstrap" {
				t.Errorf("launchctl calls = %v, want bootout then bootstrap", *calls)
			}
		})
	}
}

func TestInstallLaunchAgent_FailingBootoutStopsBeforeWriting(t *testing.T) {
	home, calls := isolateAutostart(t, launchctlReplying(map[string]launchctlReply{
		"bootout": {"Boot-out failed: 5: Input/output error", 5},
	}))

	_, err := installLaunchAgent(testLaunchAgentSpec(home))

	if err == nil || !strings.Contains(err.Error(), "launchctl bootout exited 5: Boot-out failed: 5: Input/output error") {
		t.Errorf("installLaunchAgent = %v, want the bootout failure", err)
	}
	if _, statErr := os.Stat(wantPlistPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("plist stat = %v, want no plist written", statErr)
	}
	if len(*calls) != 1 {
		t.Errorf("launchctl calls = %v, want only the bootout", *calls)
	}
}

func TestInstallLaunchAgent_FailingBootstrapIsReported(t *testing.T) {
	home, _ := isolateAutostart(t, launchctlReplying(map[string]launchctlReply{
		"bootstrap": {"Bootstrap failed: 5: Input/output error", 5},
	}))

	_, err := installLaunchAgent(testLaunchAgentSpec(home))

	if err == nil || !strings.Contains(err.Error(), "launchctl bootstrap exited 5") {
		t.Errorf("installLaunchAgent = %v, want the bootstrap failure", err)
	}
}

func TestRemoveLaunchAgent_BootsOutAndDeletes(t *testing.T) {
	home, calls := isolateAutostart(t, launchctlSucceeds)
	if _, err := installLaunchAgent(testLaunchAgentSpec(home)); err != nil {
		t.Fatalf("installLaunchAgent: %v", err)
	}
	*calls = nil

	path, wasInstalled, err := removeLaunchAgent(testAutostartLabel)

	if err != nil || !wasInstalled || path != wantPlistPath(home) {
		t.Errorf("removeLaunchAgent = (%q, %v, %v), want (%q, true, nil)", path, wasInstalled, err, wantPlistPath(home))
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("plist stat = %v, want it deleted", statErr)
	}
	if want := [][]string{wantBootout()}; !reflect.DeepEqual(*calls, want) {
		t.Errorf("launchctl calls = %v, want %v", *calls, want)
	}
}

func TestRemoveLaunchAgent_NothingInstalledIsNotAnError(t *testing.T) {
	isolateAutostart(t, launchctlReplying(map[string]launchctlReply{
		"bootout": {"Boot-out failed: 3: No such process", 3},
	}))

	_, wasInstalled, err := removeLaunchAgent(testAutostartLabel)

	if err != nil || wasInstalled {
		t.Errorf("removeLaunchAgent = (_, %v, %v), want (_, false, nil)", wasInstalled, err)
	}
}

// writeHomeLogDirConfig writes a config whose log_dir sits under the test's
// HOME and does not exist yet, so install must create it.
func writeHomeLogDirConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "servers:\n" +
		"  ollama: true\n" +
		"log_dir: ~/autostart-logs\n" +
		"profiles:\n" +
		"  chat:\n" +
		"    server: ollama\n" +
		"    model: llama3\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

func TestCmdAutostart_InstallThenRemove(t *testing.T) {
	home, _ := isolateAutostart(t, launchctlSucceeds)
	cfgPath := writeHomeLogDirConfig(t)

	installOut, installCode := runCLI(t, cfgPath, "autostart", "install", "chat")
	_, installStatErr := os.Stat(wantPlistPath(home))
	removeOut, removeCode := runCLI(t, cfgPath, "autostart", "remove", "chat")

	if installCode != 0 || installStatErr != nil {
		t.Errorf("install = exit %d, plist stat %v; want exit 0 and the plist written", installCode, installStatErr)
	}
	for _, fragment := range []string{wantPlistPath(home), "Loading chat now", "every login"} {
		if !strings.Contains(installOut, fragment) {
			t.Errorf("install output lacks %q:\n%s", fragment, installOut)
		}
	}
	if removeCode != 0 {
		t.Errorf("remove = exit %d, want 0", removeCode)
	}
	for _, fragment := range []string{"Removed launch agent: " + wantPlistPath(home), "keeps running", "llama-launcher stop"} {
		if !strings.Contains(removeOut, fragment) {
			t.Errorf("remove output lacks %q:\n%s", fragment, removeOut)
		}
	}
}

func TestCmdAutostart_LaunchctlFailureExits3(t *testing.T) {
	isolateAutostart(t, launchctlReplying(map[string]launchctlReply{
		"bootout": {"Boot-out failed: 5: Input/output error", 5},
	}))
	cfgPath := writeHomeLogDirConfig(t)

	for _, subcommand := range []string{"install", "remove"} {
		_, code := runCLI(t, cfgPath, "autostart", subcommand, "chat")

		if code != 3 {
			t.Errorf("autostart %s = exit %d, want 3", subcommand, code)
		}
	}
}
