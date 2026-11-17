package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeAutostartConfig writes a config with a managed vLLM Profile "chat" and
// an Ollama Profile "remote" into dir and loads it.
func writeAutostartConfig(t *testing.T, dir string) *Config {
	t.Helper()

	path := filepath.Join(dir, "config.yaml")
	content := "servers:\n" +
		"  vllm: true\n" +
		"  ollama: true\n" +
		"log_dir: " + dir + "\n" +
		"defaults:\n" +
		"  server: vllm\n" +
		"profiles:\n" +
		"  chat:\n" +
		"    model: owner/repo\n" +
		"  remote:\n" +
		"    server: ollama\n" +
		"    model: llama3\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	cfg, err := LoadConfigNotify(path, func(string) {})
	if err != nil {
		t.Fatalf("loading temp config: %v", err)
	}
	return cfg
}

// fakeServerBinaryDir puts an executable named name into a fresh directory,
// makes that directory the process's whole PATH and returns it.
func fakeServerBinaryDir(t *testing.T, name string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("writing fake %s: %v", name, err)
	}
	t.Setenv("PATH", dir)
	return dir
}

// envLookup returns a getenv over values, reporting "" for any other key.
func envLookup(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func joinPathList(dirs ...string) string {
	return strings.Join(dirs, string(os.PathListSeparator))
}

func TestBuildLaunchAgentSpec_ProgramAndLabel(t *testing.T) {
	dir := t.TempDir()
	cfg := writeAutostartConfig(t, dir)

	spec, err := buildLaunchAgentSpec(cfg, cfg.ConfigPath, "chat", envLookup(nil))

	if err != nil {
		t.Fatalf("buildLaunchAgentSpec: %v", err)
	}
	if spec.Label != "com.github.airiclenz.llama-launcher.chat" {
		t.Errorf("Label = %q", spec.Label)
	}
	args := spec.ProgramArguments
	if len(args) != 5 || !filepath.IsAbs(args[0]) ||
		args[1] != "--config" || args[2] != cfg.ConfigPath || args[3] != "load" || args[4] != "chat" {
		t.Errorf("ProgramArguments = %q, want [<abs launcher> --config %s load chat]", args, cfg.ConfigPath)
	}
	if want := filepath.Join(dir, "autostart-chat.log"); spec.LogPath != want {
		t.Errorf("LogPath = %q, want %q", spec.LogPath, want)
	}
}

func TestBuildLaunchAgentSpec_SlugsTheProfileName(t *testing.T) {
	dir := t.TempDir()
	cfg := writeAutostartConfig(t, dir)
	cfg.Profiles["my chat"] = cfg.Profiles["chat"]

	spec, err := buildLaunchAgentSpec(cfg, cfg.ConfigPath, "my chat", envLookup(nil))

	if err != nil {
		t.Fatalf("buildLaunchAgentSpec: %v", err)
	}
	if spec.Label != "com.github.airiclenz.llama-launcher.my_chat" {
		t.Errorf("Label = %q", spec.Label)
	}
	if want := filepath.Join(dir, "autostart-my_chat.log"); spec.LogPath != want {
		t.Errorf("LogPath = %q, want %q", spec.LogPath, want)
	}
	if last := spec.ProgramArguments[len(spec.ProgramArguments)-1]; last != "my chat" {
		t.Errorf("profile argument = %q, want the unslugged name", last)
	}
}

func TestBuildLaunchAgentSpec_RelativePathsMadeAbsolute(t *testing.T) {
	dir := t.TempDir()
	cfg := writeAutostartConfig(t, dir)
	t.Chdir(dir)
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cfg.LogDir = "logs"

	spec, err := buildLaunchAgentSpec(cfg, "config.yaml", "chat", envLookup(nil))

	if err != nil {
		t.Fatalf("buildLaunchAgentSpec: %v", err)
	}
	if want := filepath.Join(workingDir, "config.yaml"); spec.ProgramArguments[2] != want {
		t.Errorf("config argument = %q, want %q", spec.ProgramArguments[2], want)
	}
	if want := filepath.Join(workingDir, "logs", "autostart-chat.log"); spec.LogPath != want {
		t.Errorf("LogPath = %q, want %q", spec.LogPath, want)
	}
}

func TestBuildLaunchAgentSpec_UnknownProfile(t *testing.T) {
	cfg := writeAutostartConfig(t, t.TempDir())

	_, err := buildLaunchAgentSpec(cfg, cfg.ConfigPath, "missing", envLookup(nil))

	if err == nil || !strings.Contains(err.Error(), `unknown profile "missing"`) {
		t.Errorf("err = %v, want an unknown profile error", err)
	}
}

func TestBuildLaunchAgentSpec_Path(t *testing.T) {
	cfg := writeAutostartConfig(t, t.TempDir())
	serverDir := fakeServerBinaryDir(t, "vllm")
	running, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	launcherDir := filepath.Dir(running)

	cases := []struct {
		name    string
		profile string
		path    string
		want    string
	}{
		{
			"a managed server's directory follows the launcher's",
			"chat", joinPathList("/usr/bin", "/bin"),
			joinPathList(launcherDir, serverDir, "/usr/bin", "/bin"),
		},
		{
			"a directory already on PATH is not added again",
			"chat", joinPathList("/usr/bin", serverDir),
			joinPathList(launcherDir, "/usr/bin", serverDir),
		},
		{
			"an unmanaged server adds only the launcher's directory",
			"remote", joinPathList("/usr/bin"),
			joinPathList(launcherDir, "/usr/bin"),
		},
		{
			"an empty PATH holds just the prepended directories",
			"chat", "",
			joinPathList(launcherDir, serverDir),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, err := buildLaunchAgentSpec(cfg, cfg.ConfigPath, c.profile, envLookup(map[string]string{"PATH": c.path}))

			if err != nil {
				t.Fatalf("buildLaunchAgentSpec: %v", err)
			}
			if got := spec.Environment["PATH"]; got != c.want {
				t.Errorf("PATH = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildLaunchAgentSpec_ServerBinaryMissing(t *testing.T) {
	cfg := writeAutostartConfig(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	running, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	spec, err := buildLaunchAgentSpec(cfg, cfg.ConfigPath, "chat", envLookup(map[string]string{"PATH": "/usr/bin"}))

	if err != nil {
		t.Fatalf("buildLaunchAgentSpec: %v", err)
	}
	if want := joinPathList(filepath.Dir(running), "/usr/bin"); spec.Environment["PATH"] != want {
		t.Errorf("PATH = %q, want %q", spec.Environment["PATH"], want)
	}
}

func TestBuildLaunchAgentSpec_ForwardsHubVariablesOnlyWhenSet(t *testing.T) {
	cfg := writeAutostartConfig(t, t.TempDir())

	cases := []struct {
		name string
		env  map[string]string
	}{
		{"none set", map[string]string{}},
		{"HF_HOME only", map[string]string{"HF_HOME": "/hf"}},
		{"HF_HUB_CACHE only", map[string]string{"HF_HUB_CACHE": "/hub"}},
		{"XDG_CACHE_HOME only", map[string]string{"XDG_CACHE_HOME": "/cache"}},
		{"all set", map[string]string{"HF_HOME": "/hf", "HF_HUB_CACHE": "/hub", "XDG_CACHE_HOME": "/cache"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, err := buildLaunchAgentSpec(cfg, cfg.ConfigPath, "chat", envLookup(c.env))

			if err != nil {
				t.Fatalf("buildLaunchAgentSpec: %v", err)
			}
			for _, key := range launchAgentForwardedEnv {
				got, present := spec.Environment[key]
				want, set := c.env[key]
				if present != set || got != want {
					t.Errorf("%s = %q (present %v), want %q (present %v)", key, got, present, want, set)
				}
			}
		})
	}
}

func TestRenderLaunchAgentPlist(t *testing.T) {
	spec := launchAgentSpec{
		Label:            "com.github.airiclenz.llama-launcher.chat",
		ProgramArguments: []string{"/opt/homebrew/bin/llama-launcher", "--config", "/Users/me/config.yaml", "load", "chat"},
		LogPath:          "/Users/me/logs/autostart-chat.log",
		Environment:      map[string]string{"PATH": "/opt/homebrew/bin:/usr/bin", "HF_HOME": "/hf"},
	}

	plist := renderLaunchAgentPlist(spec)

	wantFragments := []string{
		"<key>Label</key>\n\t<string>com.github.airiclenz.llama-launcher.chat</string>\n",
		"<key>ProgramArguments</key>\n\t<array>\n" +
			"\t\t<string>/opt/homebrew/bin/llama-launcher</string>\n" +
			"\t\t<string>--config</string>\n" +
			"\t\t<string>/Users/me/config.yaml</string>\n" +
			"\t\t<string>load</string>\n" +
			"\t\t<string>chat</string>\n" +
			"\t</array>\n",
		"<key>RunAtLoad</key>\n\t<true/>\n",
		"<key>AbandonProcessGroup</key>\n\t<true/>\n",
		"<key>StandardOutPath</key>\n\t<string>/Users/me/logs/autostart-chat.log</string>\n",
		"<key>StandardErrorPath</key>\n\t<string>/Users/me/logs/autostart-chat.log</string>\n",
		"<key>EnvironmentVariables</key>\n\t<dict>\n" +
			"\t\t<key>HF_HOME</key>\n\t\t<string>/hf</string>\n" +
			"\t\t<key>PATH</key>\n\t\t<string>/opt/homebrew/bin:/usr/bin</string>\n" +
			"\t</dict>\n",
	}
	for _, fragment := range wantFragments {
		if !strings.Contains(plist, fragment) {
			t.Errorf("plist lacks %q:\n%s", fragment, plist)
		}
	}
	if strings.Contains(plist, "KeepAlive") {
		t.Errorf("plist sets KeepAlive:\n%s", plist)
	}
	lintPlist(t, plist)
}

func TestRenderLaunchAgentPlist_EscapesXML(t *testing.T) {
	spec := launchAgentSpec{
		Label:            "com.github.airiclenz.llama-launcher.a__b",
		ProgramArguments: []string{"/bin/llama-launcher", "load", "a&<b"},
		LogPath:          "/logs/autostart-a__b.log",
	}

	plist := renderLaunchAgentPlist(spec)

	if !strings.Contains(plist, "<string>a&amp;&lt;b</string>") {
		t.Errorf("plist does not escape the Profile name:\n%s", plist)
	}
	if strings.Contains(plist, "a&<b") {
		t.Errorf("plist carries the raw Profile name:\n%s", plist)
	}
	if strings.Contains(plist, "EnvironmentVariables") {
		t.Errorf("plist names an empty environment:\n%s", plist)
	}
	lintPlist(t, plist)
}

// lintPlist checks plist with plutil on darwin; elsewhere it does nothing.
func lintPlist(t *testing.T, plist string) {
	t.Helper()

	if runtime.GOOS != "darwin" {
		return
	}
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Logf("plutil not found, skipping the lint: %v", err)
		return
	}
	path := filepath.Join(t.TempDir(), "agent.plist")
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		t.Fatalf("writing plist: %v", err)
	}
	if out, err := exec.Command(plutil, "-lint", path).CombinedOutput(); err != nil {
		t.Errorf("plutil -lint: %v\n%s\n%s", err, out, plist)
	}
}
