package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// launchAgentLabelPrefix starts every launchd job label the launcher writes;
// the Profile's slug completes it.
const launchAgentLabelPrefix = "com.github.airiclenz.llama-launcher."

// autostartLogPrefix starts the name of the log file a launchd job writes its
// stdout and stderr to, inside log_dir.
const autostartLogPrefix = "autostart-"

// launcherBinaryName is the name the launcher is installed under on PATH.
const launcherBinaryName = "llama-launcher"

// launchAgentForwardedEnv are the variables a launchd job inherits from the
// shell that wrote it, when set: every one splashHubDir reads, so a `load` run
// by launchd resolves the same Hugging Face hub cache as one run by hand.
var launchAgentForwardedEnv = []string{"HF_HOME", "HF_HUB_CACHE", "XDG_CACHE_HOME"}

// plistEscaper escapes the characters XML text may not carry verbatim.
var plistEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// launchAgentSpec is the content of a launchd agent that loads one Profile at
// login. RunAtLoad and AbandonProcessGroup are always true and KeepAlive is
// never set, so they are not fields: the job runs `load` once, and the server
// it forks outlives it.
type launchAgentSpec struct {
	Label            string
	ProgramArguments []string
	// LogPath is the absolute file launchd sends the job's stdout and stderr to.
	LogPath     string
	Environment map[string]string
}

// buildLaunchAgentSpec returns the launchd agent that runs
// `llama-launcher --config <configPath> load <profileName>` at login.
// profileName must resolve through cfg.ResolveProfile, or the error says why
// it does not. configPath and cfg.LogDir are made absolute, with ~ expanded,
// because launchd runs the job from /. getenv supplies the PATH and the
// forwarded variables the job carries; PATH gets the launcher's directory and
// the Profile's managed server binary's directory (found on the process's
// PATH, skipped when absent) prepended when it lacks them.
func buildLaunchAgentSpec(
	cfg *Config,
	configPath string,
	profileName string,
	getenv func(key string) string,
) (launchAgentSpec, error) {
	profile, err := cfg.ResolveProfile(profileName)
	if err != nil {
		return launchAgentSpec{}, err
	}

	absConfigPath, err := filepath.Abs(ExpandTilde(configPath))
	if err != nil {
		return launchAgentSpec{}, fmt.Errorf("resolving config path: %w", err)
	}
	absLogDir, err := filepath.Abs(ExpandTilde(cfg.LogDir))
	if err != nil {
		return launchAgentSpec{}, fmt.Errorf("resolving log_dir: %w", err)
	}
	launcherPath, err := launcherExecutablePath()
	if err != nil {
		return launchAgentSpec{}, fmt.Errorf("locating the launcher: %w", err)
	}

	slug := logProfileSlug(profile.Name)
	pathDirs := []string{filepath.Dir(launcherPath)}
	if serverDir := managedServerBinaryDir(cfg, profile); serverDir != "" {
		pathDirs = append(pathDirs, serverDir)
	}

	return launchAgentSpec{
		Label:            launchAgentLabelPrefix + slug,
		ProgramArguments: []string{launcherPath, "--config", absConfigPath, "load", profile.Name},
		LogPath:          filepath.Join(absLogDir, autostartLogPrefix+slug+".log"),
		Environment:      launchAgentEnvironment(getenv, pathDirs),
	}, nil
}

// launcherExecutablePath returns the absolute path launchd should run the
// launcher by: the PATH entry when it is the running binary — Homebrew's
// stable symlink, which survives an upgrade — otherwise the running binary
// itself.
func launcherExecutablePath() (string, error) {
	running, err := os.Executable()
	if err != nil {
		return "", err
	}
	onPath, err := exec.LookPath(launcherBinaryName)
	if err != nil {
		return running, nil
	}
	if !sameFile(running, onPath) {
		return running, nil
	}
	return onPath, nil
}

// sameFile reports whether the two paths name the same file; a path that
// cannot be read names none.
func sameFile(first, second string) bool {
	firstInfo, err := os.Stat(first)
	if err != nil {
		return false
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		return false
	}
	return os.SameFile(firstInfo, secondInfo)
}

// managedServerBinaryDir returns the absolute directory of the server binary
// the launcher forks for profile, or "" when its LLM Server is not managed or
// the binary is not on PATH.
func managedServerBinaryDir(cfg *Config, profile *ResolvedProfile) string {
	server, err := GetLLMServer(profile.Backend)
	if err != nil {
		return ""
	}
	managed, isManaged := server.(ManagedLLMServer)
	if !isManaged {
		return ""
	}
	// LookPath refuses a match relative to the working directory (ErrDot), so
	// a found binary's directory is always absolute.
	binary, err := exec.LookPath(managed.ServerBinary(cfg))
	if err != nil {
		return ""
	}
	return filepath.Dir(binary)
}

// launchAgentEnvironment returns the variables a launchd job carries: PATH,
// with each of prependDirs it lacks placed before it in order, and every
// forwarded variable getenv reports non-empty. An empty PATH with nothing to
// prepend is left out, so launchd's default applies.
func launchAgentEnvironment(getenv func(key string) string, prependDirs []string) map[string]string {
	environment := map[string]string{}
	for _, key := range launchAgentForwardedEnv {
		if value := getenv(key); value != "" {
			environment[key] = value
		}
	}

	existing := filepath.SplitList(getenv("PATH"))
	present := make(map[string]bool, len(existing))
	for _, dir := range existing {
		present[dir] = true
	}
	var missing []string
	for _, dir := range prependDirs {
		if !present[dir] {
			missing = append(missing, dir)
			present[dir] = true
		}
	}
	if path := strings.Join(append(missing, existing...), string(os.PathListSeparator)); path != "" {
		environment["PATH"] = path
	}
	return environment
}

// renderLaunchAgentPlist renders spec as a launchd property list, its text
// XML-escaped and its environment keys sorted so the output is stable.
func renderLaunchAgentPlist(spec launchAgentSpec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n<dict>\n")

	writePlistString(&b, "\t", "Label", spec.Label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, argument := range spec.ProgramArguments {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", plistEscaper.Replace(argument))
	}
	b.WriteString("\t</array>\n")
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>AbandonProcessGroup</key>\n\t<true/>\n")
	writePlistString(&b, "\t", "StandardOutPath", spec.LogPath)
	writePlistString(&b, "\t", "StandardErrorPath", spec.LogPath)

	if len(spec.Environment) > 0 {
		keys := make([]string, 0, len(spec.Environment))
		for key := range spec.Environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, key := range keys {
			writePlistString(&b, "\t\t", key, spec.Environment[key])
		}
		b.WriteString("\t</dict>\n")
	}

	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// writePlistString writes one key and its string value at indent.
func writePlistString(b *strings.Builder, indent, key, value string) {
	fmt.Fprintf(
		b,
		"%s<key>%s</key>\n%s<string>%s</string>\n",
		indent, plistEscaper.Replace(key), indent, plistEscaper.Replace(value),
	)
}

// cmdAutostart is the autostart command. `autostart print <profile>` writes
// the launchd agent that loads the Profile at login to stdout and exits 0; a
// usage error, or a Profile that does not resolve, exits 2. It runs on every
// OS: printing a plist touches neither launchd nor the disk.
func cmdAutostart(cfg *Config, configPath string, args []string) int {
	if len(args) != 2 || args[0] != "print" {
		printAutostartUsage()
		return 2
	}

	spec, err := buildLaunchAgentSpec(cfg, configPath, args[1], os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	fmt.Print(renderLaunchAgentPlist(spec))
	return 0
}

func printAutostartUsage() {
	fmt.Fprintln(os.Stderr, "Usage: llama-launcher autostart print <profile>")
}
