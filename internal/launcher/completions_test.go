package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// runComplete drives the real Run dispatcher and returns what it wrote to
// stdout and stderr and its exit code. Callers must not use t.Parallel(): the
// standard streams are process-global.
func runComplete(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = Run(args)
		})
	})
	return stdout, stderr, code
}

// completionLines splits __complete output into its candidates.
func completionLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestComplete_Candidates(t *testing.T) {
	cfgPath := writeRunConfig(t)

	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"no words lists the subcommands", nil, completionSubcommands},
		{"an empty first word lists the subcommands", []string{""}, completionSubcommands},
		{"a first-word prefix filters the subcommands", []string{"lo"}, []string{"load", "logs"}},
		{"a dash on the first word lists the global flag", []string{"-"}, []string{"--config"}},
		{"load offers profile names", []string{"load", ""}, []string{"chat"}},
		{"load offers its flags after a dash", []string{"load", "-"}, []string{"--restart", "-r", "--force"}},
		{"load offers nothing after its profile", []string{"load", "chat", ""}, nil},
		{"unload offers profile names", []string{"unload", "c"}, []string{"chat"}},
		{"start offers profiles after --profile", []string{"start", "--profile", ""}, []string{"chat"}},
		{"start offers profiles after -p", []string{"start", "-p", ""}, []string{"chat"}},
		{"start offers its flags after a dash", []string{"start", "--"}, []string{"--profile"}},
		{"stop offers enabled backends", []string{"stop", ""}, []string{"llamacpp", "ollama"}},
		{"status offers --json", []string{"status", "-"}, []string{"--json"}},
		{"list offers --json", []string{"list", "--j"}, []string{"--json"}},
		{"logs offers clean and backends", []string{"logs", ""}, []string{"clean", "llamacpp", "ollama"}},
		{"logs offers its flags after a dash", []string{"logs", "-"}, []string{"--follow", "-f"}},
		{"logs clean offers its flags", []string{"logs", "clean", "-"}, []string{"--days", "--all"}},
		{"logs clean offers nothing after --days", []string{"logs", "clean", "--days", ""}, nil},
		{"config offers its subcommands", []string{"config", ""}, []string{"validate", "init", "reset"}},
		{"config init offers --force", []string{"config", "init", "-"}, []string{"--force", "-f"}},
		{"completions offers the shells", []string{"completions", ""}, completionShells},
		{"an unknown command offers nothing", []string{"frobnicate", ""}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			words := append([]string{completeCommand, "--config", cfgPath}, c.words...)
			if len(c.words) == 0 {
				// Keep the bare "no words" case free of the --config pair.
				t.Setenv("LLAMA_LAUNCHER_CONFIG", cfgPath)
				words = []string{completeCommand}
			}
			out, stderr, code := runComplete(t, words...)
			if code != 0 || stderr != "" {
				t.Fatalf("Run(%v) = exit %d, stderr %q; want exit 0, no stderr", words, code, stderr)
			}
			if got := completionLines(out); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Run(%v) candidates = %q, want %q", words, got, c.want)
			}
		})
	}
}

// A tab press right after a bare --config is completing the path: it must
// print nothing, write nothing to stderr and exit 0, never take the flag as a
// usage error.
func TestComplete_TrailingConfigFlag(t *testing.T) {
	t.Setenv("LLAMA_LAUNCHER_CONFIG", "")
	t.Setenv("HOME", t.TempDir())

	for _, words := range [][]string{
		{completeCommand, "--config"},
		{completeCommand, "load", "--config"},
	} {
		if _, stderr, code := runComplete(t, words...); code != 0 || stderr != "" {
			t.Errorf("Run(%v) = exit %d, stderr %q; want exit 0, no stderr", words, code, stderr)
		}
	}

	words := []string{completeCommand, "load", "--config", ""}
	out, stderr, code := runComplete(t, words...)
	if code != 0 || stderr != "" || out != "" {
		t.Errorf("Run(%v) = exit %d, stdout %q, stderr %q; want exit 0 and nothing printed for the path", words, code, out, stderr)
	}
}

// Neither completion command may create a config file: the Homebrew install
// runs `completions` under a throwaway HOME, and a tab press with no config
// must only offer subcommands and flags.
func TestComplete_MissingConfigCreatesNoFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LLAMA_LAUNCHER_CONFIG", "")

	if _, stderr, code := runComplete(t, "completions", "bash"); code != 0 || stderr != "" {
		t.Fatalf("completions bash = exit %d, stderr %q", code, stderr)
	}

	cases := []struct {
		words []string
		want  []string
	}{
		{[]string{completeCommand, ""}, completionSubcommands},
		{[]string{completeCommand, "load", ""}, nil},
		{[]string{completeCommand, "load", "-"}, []string{"--restart", "-r", "--force"}},
		{[]string{completeCommand, "stop", ""}, nil},
		{[]string{completeCommand, "--config", filepath.Join(home, "absent.yaml"), "logs", ""}, []string{"clean"}},
	}
	for _, c := range cases {
		out, stderr, code := runComplete(t, c.words...)
		if code != 0 || stderr != "" {
			t.Errorf("Run(%v) = exit %d, stderr %q; want exit 0, no stderr", c.words, code, stderr)
		}
		if got := completionLines(out); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Run(%v) candidates = %q, want %q", c.words, got, c.want)
		}
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("reading temp HOME: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("temp HOME holds %d entries after completion, want none (first: %s)", len(entries), entries[0].Name())
	}
}

// A tab press must never run an api_key_cmd: __complete parses and validates
// the config only.
func TestComplete_RunsNoKeyCommand(t *testing.T) {
	requirePOSIXShell(t)

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	path := filepath.Join(dir, "config.yaml")
	writeKeyCommandConfig(t, path, "touch '"+marker+"'; printf sk-test")

	out, stderr, code := runComplete(t, completeCommand, "--config", path, "load", "")
	if code != 0 || stderr != "" {
		t.Fatalf("__complete = exit %d, stderr %q", code, stderr)
	}
	if got := completionLines(out); !reflect.DeepEqual(got, []string{"test"}) {
		t.Errorf("candidates = %q, want [test]: the config was not loaded", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("__complete ran the api_key_cmd: its marker file exists")
	}
}

func TestCompletions_UsageErrors(t *testing.T) {
	for _, args := range [][]string{{"completions"}, {"completions", "tcsh"}, {"completions", "bash", "zsh"}} {
		out, _, code := runComplete(t, args...)
		if code != 2 || out != "" {
			t.Errorf("Run(%v) = exit %d, stdout %q; want exit 2, no script", args, code, out)
		}
	}
}

// Each script must call __complete, parse in its shell, and — for bash — use
// only bash-3.2 builtins, since macOS ships /bin/bash 3.2.
func TestCompletions_ScriptsParse(t *testing.T) {
	syntaxCheck := map[string][]string{
		"bash": {"bash", "-n"},
		"zsh":  {"zsh", "-n"},
		"fish": {"fish", "--no-execute"},
	}
	for _, shell := range completionShells {
		t.Run(shell, func(t *testing.T) {
			script, stderr, code := runComplete(t, "completions", shell)
			if code != 0 || stderr != "" {
				t.Fatalf("completions %s = exit %d, stderr %q", shell, code, stderr)
			}
			if !strings.Contains(script, completeCommand) {
				t.Errorf("%s script does not call %s", shell, completeCommand)
			}
			if shell == "bash" {
				for _, banned := range []string{"mapfile", "readarray"} {
					if strings.Contains(script, banned) {
						t.Errorf("bash script uses %s, which bash 3.2 lacks", banned)
					}
				}
			}

			argv := syntaxCheck[shell]
			if _, err := exec.LookPath(argv[0]); err != nil {
				t.Skipf("%s not on PATH", argv[0])
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdin = strings.NewReader(script)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s rejects the script: %v\n%s", strings.Join(argv, " "), err, out)
			}
		})
	}
}
