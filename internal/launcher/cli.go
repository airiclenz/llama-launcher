package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

var Version = "dev"

// Run is the CLI entry point. It parses args and dispatches subcommands.
func Run(args []string) int {
	configPath := ""

	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --config requires a path")
				return 2
			}
			configPath = args[i+1]
			i++
			continue
		}
		filtered = append(filtered, args[i])
	}
	args = filtered

	if len(args) == 1 && args[0] == "version" {
		fmt.Println(Version)
		return 0
	}

	if configPath == "" {
		configPath = os.Getenv("LLAMA_LAUNCHER_CONFIG")
	}
	if configPath == "" {
		configPath = DefaultConfigPath()
	}

	if len(args) >= 1 && args[0] == "config" {
		return cmdConfig(configPath, args[1:])
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			if err := GenerateExampleConfig(configPath); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				return 2
			}
			fmt.Printf("Created example config at: %s\n", configPath)
			cfg, err = LoadConfig(configPath)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 2
		}
	}

	CleanupLegacyStateFiles()

	if len(args) == 0 {
		if err := RunInteractiveMenu(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 3
		}
		return 0
	}

	switch args[0] {
	case "load":
		return cmdLoad(cfg, args[1:])
	case "unload":
		return cmdUnload(cfg, args[1:])
	case "start":
		return cmdStart(cfg, args[1:])
	case "stop":
		return cmdStop(cfg, args[1:])
	case "status":
		return cmdStatus(cfg, args[1:])
	case "list":
		return cmdList(cfg, args[1:])
	case "logs":
		return cmdLogs(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n", args[0])
		printUsage()
		return 2
	}
}

func cmdLoad(cfg *Config, args []string) int {
	restart := false
	var profileName string
	for _, a := range args {
		switch a {
		case "--restart", "-r", "--force":
			restart = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n", a)
				return 2
			}
			if profileName != "" {
				fmt.Fprintln(os.Stderr, "Usage: llama-launcher load <profile> [--restart]")
				return 2
			}
			profileName = a
		}
	}
	if profileName == "" {
		fmt.Fprintln(os.Stderr, "Usage: llama-launcher load <profile> [--restart]")
		return 2
	}

	profile, err := cfg.ResolveProfile(profileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}

	displayName := profile.DisplayName()
	progress := newCLIProgress(fmt.Sprintf("Loading %s", displayName))
	inst, started, err := LoadProfile(cfg, profile, restart, progress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}

	if started && inst.PID > 0 {
		fmt.Printf("Server started (PID %d)\n", inst.PID)
	} else if started {
		fmt.Printf("Connected to %s\n", backendDisplayName(inst.Backend))
	}
	if inst.ActiveProfile == profile.Name {
		fmt.Printf("Loaded %s on %s:%d\n", displayName, inst.Host, inst.Port)
	}
	return 0
}

func cmdUnload(cfg *Config, args []string) int {
	var target *RunningInstance
	if len(args) > 0 {
		profile, err := cfg.ResolveProfile(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 2
		}
		addr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)
		instances := DiscoverRunningInstances(cfg)
		target = findInstance(instances, addr)
		// A Starting instance has no active model yet but is a valid unload
		// target: unload on a managed backend reduces to stop (ADR-0010).
		if target == nil || target.Backend != profile.Backend || (target.ActiveModel == "" && !target.Starting) {
			fmt.Println("No model loaded for that profile.")
			return 1
		}
	} else {
		instances := DiscoverRunningInstances(cfg)
		var loaded []*RunningInstance
		for _, inst := range instances {
			if inst.ActiveModel != "" || inst.Starting {
				loaded = append(loaded, inst)
			}
		}
		if len(loaded) == 0 {
			fmt.Println("No model loaded.")
			return 1
		}
		if len(loaded) > 1 {
			fmt.Fprintln(os.Stderr, "Multiple models loaded — specify which to unload:")
			for _, inst := range loaded {
				fmt.Fprintf(os.Stderr, "  %s at %s: %s\n", backendDisplayName(inst.Backend), inst.Addr(), unloadTargetLabel(inst))
			}
			return 2
		}
		target = loaded[0]
	}

	res, err := Unload(target.Backend, target.Addr())
	printStopSteps("Unloading model", res.Steps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if res.ServerStopped {
		fmt.Printf("Model unloaded, server stopped at %s\n", res.Instance.Addr())
	} else {
		fmt.Printf("Model unloaded (server still running at %s:%d)\n", res.Instance.Host, res.Instance.Port)
	}
	return 0
}

// unloadTargetLabel describes one candidate in the ambiguous-unload listing:
// the loaded model with its matched profile, or "(starting…)" for an
// instance still loading its model (a Starting instance names no model,
// ADR-0010).
func unloadTargetLabel(inst *RunningInstance) string {
	if inst.Starting {
		return "(starting…)"
	}
	profileLabel := inst.ActiveProfile
	if profileLabel == "" {
		profileLabel = "(no matching profile)"
	}
	return fmt.Sprintf("%s (%s)", inst.ActiveModel, profileLabel)
}

func cmdStart(cfg *Config, args []string) int {
	var profileName string
	for i := 0; i < len(args); i++ {
		if args[i] == "--profile" || args[i] == "-p" {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: %s requires a profile name\n", args[i])
				return 2
			}
			profileName = args[i+1]
			i++
			continue
		}
		fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n", args[i])
		return 2
	}

	if profileName != "" {
		return cmdLoad(cfg, []string{profileName})
	}

	if cfg.Defaults.Server == nil {
		fmt.Fprintln(os.Stderr, "Error: multiple servers enabled and no default — specify --profile <name>")
		return 2
	}
	serverName := *cfg.Defaults.Server
	b, err := GetLLMServer(serverName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}

	// A managed server bakes the Model into its start arguments (ADR-0003),
	// so there is nothing to start without a profile — forking anyway would
	// only die with "--model is required". Fail fast instead.
	if _, isManaged := b.(ManagedLLMServer); isManaged {
		fmt.Fprintf(
			os.Stderr,
			"Error: %s requires a profile: llama-launcher start --profile <name>\n",
			serverName,
		)
		return 2
	}

	fmt.Printf("Starting %s...\n", b.DisplayName())
	defaults := cfg.Defaults
	applyBackendFallbacks(&defaults, cfg, serverName, b)
	profile := &ResolvedProfile{
		Backend:       serverName,
		ProfileParams: defaults,
	}
	inst, started, err := EnsureServer(cfg, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if !started {
		if inst.PID > 0 {
			fmt.Printf("Server already running on %s:%d (PID %d)\n", inst.Host, inst.Port, inst.PID)
		} else {
			fmt.Printf("Already connected to %s at %s:%d\n", b.DisplayName(), inst.Host, inst.Port)
		}
		return 0
	}
	if inst.PID > 0 {
		fmt.Printf("Server started on %s:%d (PID %d)\n", inst.Host, inst.Port, inst.PID)
		if inst.LogFile != "" {
			fmt.Printf("Log: %s\n", inst.LogFile)
		}
	} else {
		fmt.Printf("Connected to %s at %s:%d\n", b.DisplayName(), inst.Host, inst.Port)
	}
	return 0
}

// resolveTargetInstance interprets the optional [target] argument for
// stop/logs. The target may be an explicit host:port or a backend name; if
// no target is given and exactly one instance is reachable, it is selected
// automatically. On ambiguity (multiple matches), the candidates are
// printed to stderr and the returned error is non-nil.
func resolveTargetInstance(cfg *Config, target string) (*RunningInstance, error) {
	running := DiscoverRunningInstances(cfg)
	if len(running) == 0 {
		return nil, ErrNotRunning
	}

	if target == "" {
		if len(running) == 1 {
			return running[0], nil
		}
		fmt.Fprintln(os.Stderr, "Multiple servers running — specify which to stop:")
		for _, inst := range running {
			fmt.Fprintf(os.Stderr, "  %s at %s\n", backendDisplayName(inst.Backend), inst.Addr())
		}
		return nil, fmt.Errorf("ambiguous target")
	}

	if strings.Contains(target, ":") {
		for _, inst := range running {
			if inst.Addr() == target {
				return inst, nil
			}
		}
		return nil, fmt.Errorf("no running instance at %s", target)
	}

	var matches []*RunningInstance
	for _, inst := range running {
		if inst.Backend == target {
			matches = append(matches, inst)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no running %s instance", target)
	}
	if len(matches) > 1 {
		fmt.Fprintf(os.Stderr, "Multiple %s instances running — specify host:port:\n", target)
		for _, inst := range matches {
			fmt.Fprintf(os.Stderr, "  %s\n", inst.Addr())
		}
		return nil, fmt.Errorf("ambiguous target")
	}
	return matches[0], nil
}

func cmdStop(cfg *Config, args []string) int {
	target := ""
	if len(args) > 0 {
		target = args[0]
	}

	inst, err := resolveTargetInstance(cfg, target)
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			fmt.Println("No server running.")
			return 1
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}

	res, serr := Stop(inst.Addr())
	printStopSteps("Stopping server", res.Steps)
	if serr != nil {
		if errors.Is(serr, ErrNotRunning) {
			fmt.Println("No server running.")
			return 1
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", serr)
		return 3
	}
	stopped := res.Instance
	if stopped.PID > 0 {
		fmt.Printf("Stopped %s at %s (PID %d)\n", backendDisplayName(stopped.Backend), stopped.Addr(), stopped.PID)
	} else {
		fmt.Printf("Stopped %s at %s\n", backendDisplayName(stopped.Backend), stopped.Addr())
	}
	return 0
}

func cmdStatus(cfg *Config, args []string) int {
	jsonOutput := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOutput = true
		default:
			fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n", a)
			return 2
		}
	}
	if jsonOutput {
		return cmdStatusJSON(cfg)
	}

	instances := DiscoverRunningInstances(cfg)
	if len(instances) == 0 {
		fmt.Println("No servers running.")
		return 1
	}

	maxLen := 0
	for _, inst := range instances {
		if n := len(backendDisplayName(inst.Backend)); n > maxLen {
			maxLen = n
		}
	}

	for _, inst := range instances {
		b, err := GetLLMServer(inst.Backend)
		if err != nil {
			continue
		}
		state := statusStateLabel(inst)
		modelStr := inst.ActiveModel
		if modelStr != "" {
			fmt.Printf("  ● %-*s  %s  %-22s %s\n", maxLen, b.DisplayName(), state, inst.Addr(), modelStr)
		} else {
			fmt.Printf("  ● %-*s  %s  %s\n", maxLen, b.DisplayName(), state, inst.Addr())
		}
	}

	for _, inst := range instances {
		// A Starting instance gets a details line too: its PID and log are
		// exactly what a user watching a long model load needs (ADR-0010).
		if inst.ActiveModel == "" && !inst.Starting {
			continue
		}
		fillRuntimeDetails(cfg, inst)
		parts := []string{statusDetailsLead(cfg, inst)}
		if inst.PID > 0 {
			parts = append(parts, fmt.Sprintf("PID %d", inst.PID))
			if uptime := inst.Uptime(); uptime > 0 {
				parts = append(parts, fmt.Sprintf("Uptime %s", formatUptime(uptime)))
			}
		}
		if inst.LogFile != "" {
			parts = append(parts, fmt.Sprintf("Log %s", inst.LogFile))
		}
		fmt.Println()
		fmt.Println(strings.Join(parts, " · "))
	}

	return 0
}

// statusStateLabel renders the state column of a status row: a Starting
// instance (ADR-0010) shows "starting…" instead of "running". The label is
// padded to a fixed visible width so the address column stays aligned —
// byte-counting %-*s padding would drift on the multibyte ellipsis.
func statusStateLabel(inst *RunningInstance) string {
	const columnWidth = 9 // visible width of the widest label, "starting…"
	label := "running"
	if inst.Starting {
		label = "starting…"
	}
	return label + strings.Repeat(" ", columnWidth-visibleWidth(label))
}

// statusDetailsLead is the first segment of an instance's details line: the
// active profile (or model) for a healthy instance, or a "Starting" marker
// for one still loading — a Starting instance has no model to name
// (ADR-0010).
func statusDetailsLead(cfg *Config, inst *RunningInstance) string {
	if inst.Starting {
		return fmt.Sprintf("Starting: %s", backendDisplayName(inst.Backend))
	}
	profileLabel := profileDisplayName(cfg, inst.ActiveProfile)
	if inst.ActiveProfile == "" {
		profileLabel = inst.ActiveModel
	}
	return fmt.Sprintf("Active: %s", profileLabel)
}

func cmdList(cfg *Config, args []string) int {
	jsonOutput := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOutput = true
		default:
			fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n", a)
			return 2
		}
	}
	if jsonOutput {
		return cmdListJSON(cfg)
	}

	names := cfg.ProfileNames()
	anyFav := anyProfileFavourite(cfg, names)

	maxNameLen := 0
	maxTagLen := 0
	maxDescLen := 0
	descs := make(map[string]string, len(names))
	for _, name := range names {
		if w := visibleWidth(name); w > maxNameLen {
			maxNameLen = w
		}
		p := cfg.Profiles[name]
		server := resolveProfileServer(cfg, &p)
		tag := backendDisplayName(server)
		if w := visibleWidth(tag); w > maxTagLen {
			maxTagLen = w
		}
		desc := p.Description
		if desc == "" {
			desc = "-"
		}
		descs[name] = desc
		if w := visibleWidth(desc); w > maxDescLen {
			maxDescLen = w
		}
	}

	fmt.Println("Profiles:")
	fmt.Println()
	for _, name := range names {
		p := cfg.Profiles[name]
		server := resolveProfileServer(cfg, &p)
		desc := descs[name]
		suffix := ""
		if anyFav {
			pad := strings.Repeat(" ", maxDescLen-visibleWidth(desc))
			if p.IsFavourite {
				suffix = pad + " ★"
			}
		}
		fmt.Printf("  %-*s  [%-*s] %s%s\n", maxNameLen, name, maxTagLen, backendDisplayName(server), desc, suffix)
	}
	return 0
}

// cmdStatusJSON prints one JSON entry per running instance (keyed by
// address) plus one running=false entry per enabled backend that has no
// running instance, so multiple concurrent instances of one backend all
// appear (ADR-0006). A Starting instance (ADR-0010) reports running=false
// with starting=true — running keeps meaning healthy. Entries are grouped
// by backend name in sorted order; within a backend, instances are ordered
// by address. Exit code matches the human path: 0 if any instance was
// discovered (healthy or Starting), 1 if all are stopped.
func cmdStatusJSON(cfg *Config) int {
	type entry struct {
		Backend       string `json:"backend"`
		Running       bool   `json:"running"`
		Starting      bool   `json:"starting"`
		Address       string `json:"address"`
		ActiveProfile string `json:"active_profile"`
		ActiveModel   string `json:"active_model"`
		PID           int    `json:"pid"`
		UptimeSeconds int64  `json:"uptime_seconds"`
	}

	var backends []string
	for name := range cfg.Servers {
		if cfg.IsServerEnabled(name) {
			backends = append(backends, name)
		}
	}
	sort.Strings(backends)

	instances := DiscoverRunningInstances(cfg)

	output := make([]entry, 0, len(backends)+len(instances))
	anyRunning := false
	for _, name := range backends {
		matched := false
		for _, inst := range instances {
			if inst.Backend != name {
				continue
			}
			matched = true
			anyRunning = true
			fillRuntimeDetails(cfg, inst)
			output = append(output, entry{
				Backend:       name,
				Running:       !inst.Starting,
				Starting:      inst.Starting,
				Address:       inst.Addr(),
				ActiveProfile: inst.ActiveProfile,
				ActiveModel:   inst.ActiveModel,
				PID:           inst.PID,
				UptimeSeconds: int64(inst.Uptime().Seconds()),
			})
		}
		if !matched {
			output = append(output, entry{Backend: name})
		}
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	fmt.Println(string(data))

	if !anyRunning {
		return 1
	}
	return 0
}

// cmdListJSON prints a JSON array of configured profiles. Pointer fields on
// ProfileParams are dereferenced into local fields with omitempty so unset
// parameters are absent rather than serialised as null.
func cmdListJSON(cfg *Config) int {
	type entry struct {
		Name        string `json:"name"`
		Title       string `json:"title,omitempty"`
		Description string `json:"description,omitempty"`
		Backend     string `json:"backend"`
		Model       string `json:"model"`
		GPULayers   *int   `json:"gpu_layers,omitempty"`
		ContextSize *int   `json:"context_size,omitempty"`
	}

	names := cfg.ProfileNames()
	output := make([]entry, 0, len(names))
	for _, name := range names {
		p := cfg.Profiles[name]
		server := resolveProfileServer(cfg, &p)
		merged := mergeParams(cfg.Defaults, p.ProfileParams)
		output = append(output, entry{
			Name:        name,
			Title:       p.Title,
			Description: p.Description,
			Backend:     server,
			Model:       p.Model,
			GPULayers:   merged.GPULayers,
			ContextSize: merged.ContextSize,
		})
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	fmt.Println(string(data))
	return 0
}

func cmdLogs(cfg *Config, args []string) int {
	if len(args) > 0 && args[0] == "clean" {
		return cmdLogsClean(cfg, args[1:])
	}

	follow := false
	var target string
	for _, arg := range args {
		if arg == "--follow" || arg == "-f" {
			follow = true
		} else {
			target = arg
		}
	}

	var inst *RunningInstance
	if target != "" {
		t, err := resolveTargetInstance(cfg, target)
		if err != nil {
			if errors.Is(err, ErrNotRunning) {
				fmt.Println("No server running.")
				return 1
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 2
		}
		inst = t
	} else {
		instances := DiscoverRunningInstances(cfg)
		if len(instances) == 1 {
			inst = instances[0]
		} else if len(instances) > 1 {
			fmt.Fprintln(os.Stderr, "Multiple servers running — specify which to show logs for:")
			for _, c := range instances {
				fmt.Fprintf(os.Stderr, "  %s at %s\n", backendDisplayName(c.Backend), c.Addr())
			}
			return 2
		}
	}

	if inst == nil {
		fmt.Println("No server running.")
		return 1
	}

	fillRuntimeDetails(cfg, inst)
	if inst.LogFile == "" {
		fmt.Fprintf(os.Stderr, "No launcher-managed log found for %s under %s.\n", backendDisplayName(inst.Backend), cfg.LogDir)
		fmt.Fprintln(os.Stderr, "External servers log to wherever they were started; check their own log location.")
		return 1
	}

	if err := TailLog(inst.LogFile, follow); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	return 0
}

func cmdLogsClean(cfg *Config, args []string) int {
	days := 7
	daysSet := false
	deleteAll := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--days":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --days requires a value")
				return 2
			}
			n := 0
			for _, c := range args[i+1] {
				if c < '0' || c > '9' {
					fmt.Fprintln(os.Stderr, "Error: --days value must be a positive integer")
					return 2
				}
				n = n*10 + int(c-'0')
			}
			days = n
			daysSet = true
			i++
		case "--all":
			deleteAll = true
		default:
			fmt.Fprintf(os.Stderr, "Error: unknown flag %q\n", args[i])
			fmt.Fprintln(os.Stderr, "Usage: llama-launcher logs clean [--days N|--all]")
			return 2
		}
	}

	if daysSet && deleteAll {
		fmt.Fprintln(os.Stderr, "Error: --days and --all are mutually exclusive")
		return 2
	}

	maxAge := time.Duration(days) * 24 * time.Hour
	result, err := cleanupLogs(cfg, cfg.LogDir, maxAge, deleteAll)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}

	if result.Removed == 0 {
		fmt.Println("No log files to clean.")
		return 0
	}

	fmt.Printf("Removed %d file(s), freed %s\n", result.Removed, formatBytes(result.Freed))
	return 0
}

func cmdConfig(configPath string, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: llama-launcher config <subcommand>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  validate         Check config file for errors")
		fmt.Fprintln(os.Stderr, "  init [--force]   Generate example config (overwrite if --force)")
		fmt.Fprintln(os.Stderr, "  reset            Reset config to example (overwrite)")
		return 2
	}

	switch args[0] {
	case "validate":
		return cmdConfigValidate(configPath)
	case "init":
		return cmdConfigInit(configPath, args[1:])
	case "reset":
		return cmdConfigReset(configPath)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown config subcommand %q\n", args[0])
		return 2
	}
}

func cmdConfigValidate(configPath string) int {
	cfg, err := parseConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}

	problems := cfg.validateAll()
	if len(problems) == 0 {
		fmt.Printf("Config OK: %s\n", configPath)
		return 0
	}

	fmt.Fprintf(os.Stderr, "Found %d problem(s) in %s:\n\n", len(problems), configPath)
	for i, p := range problems {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, p)
	}
	return 2
}

func cmdConfigInit(configPath string, args []string) int {
	force := false
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
		}
	}

	// If file exists and not forced, error.
	if !force {
		if _, err := os.Stat(configPath); err == nil {
			fmt.Fprintf(os.Stderr, "Error: config already exists. Use --force to overwrite.\n")
			return 2
		}
	}

	if err := GenerateExampleConfig(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	fmt.Printf("Generated example config at: %s\n", configPath)
	return 0
}

func cmdConfigReset(configPath string) int {
	if err := GenerateExampleConfig(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	fmt.Printf("Reset config to example at: %s\n", configPath)
	return 0
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `
Usage: llama-launcher [--config path] [command] [args]

Commands:
  load <profile> [--restart]
                        Activate a profile (no-op if already active; --restart forces)
  unload [profile]      Unload model (for managed backends: stops server)
  start [--profile p]   Start server (optionally with a profile)
  stop [target]         Stop a server (target = host:port or backend name)
  status [--json]       Show server and model status
  list [--json]         List available profiles
  logs [target] [-f]           Tail an instance's log
  logs clean [--days N|--all]  Remove old log files
  config validate       Check config file for errors
  config init [--force]   Generate example config (overwrite if --force)
  config reset           Reset config to example (overwrite)
  version               Print version and exit

Run without arguments for interactive mode.
`)
}
