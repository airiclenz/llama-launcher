package launcher

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
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
		return cmdStart(cfg)
	case "stop":
		return cmdStop(cfg, args[1:])
	case "status":
		return cmdStatus(cfg)
	case "list":
		return cmdList(cfg)
	case "logs":
		return cmdLogs(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n", args[0])
		printUsage()
		return 2
	}
}


func cmdLoad(cfg *Config, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: llama-launcher load <profile>")
		return 2
	}

	profile, err := cfg.ResolveProfile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}

	displayName := profile.Description
	if displayName == "" {
		displayName = profile.Name
	}
	fmt.Printf("Loading %s...\n", displayName)
	state, started, err := LoadProfile(cfg, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}

	if started && state.Managed {
		fmt.Printf("Server started (PID %d)\n", state.PID)
	} else if started && !state.Managed {
		fmt.Printf("Connected to %s\n", backendDisplayName(state.Backend))
	}
	if state.ActiveProfile == profile.Name {
		fmt.Printf("Loaded %s on %s:%d\n", displayName, state.Host, state.Port)
	}
	return 0
}

func cmdUnload(cfg *Config, args []string) int {
	var backend string
	if len(args) > 0 {
		profile, err := cfg.ResolveProfile(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 2
		}
		backend = profile.Backend
	} else {
		states, _ := ReadAllStates()
		var loaded []*ServerState
		for _, s := range states {
			if IsServerAlive(s) && s.ActiveModel != "" {
				loaded = append(loaded, s)
			}
		}
		if len(loaded) == 0 {
			fmt.Println("No model loaded.")
			return 1
		}
		if len(loaded) > 1 {
			fmt.Fprintln(os.Stderr, "Multiple models loaded — specify which to unload:")
			for _, s := range loaded {
				fmt.Fprintf(os.Stderr, "  %s: %s (%s)\n", backendDisplayName(s.Backend), s.ActiveModel, s.ActiveProfile)
			}
			return 2
		}
		backend = loaded[0].Backend
	}

	b, err := GetBackend(backend)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}

	if _, ok := b.(ManagedBackend); ok {
		state, err := StopBackendServer(backend)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 3
		}
		fmt.Printf("Model unloaded, server stopped (PID %d)\n", state.PID)
	} else {
		state, err := UnloadBackendModel(backend)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 3
		}
		fmt.Printf("Model unloaded (server still running at %s:%d)\n", state.Host, state.Port)
	}
	return 0
}

func cmdStart(cfg *Config) int {
	if cfg.Defaults.Server == nil {
		fmt.Fprintln(os.Stderr, "Error: no default server configured in defaults section")
		return 2
	}
	serverName := *cfg.Defaults.Server
	b, err := GetBackend(serverName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}

	fmt.Printf("Starting %s...\n", b.DisplayName())
	defaults := cfg.Defaults
	applyBackendFallbacks(&defaults, cfg, serverName, b)
	profile := &ResolvedProfile{
		Backend:       serverName,
		ProfileParams: defaults,
	}
	state, started, err := EnsureServer(cfg, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if !started {
		if state.Managed {
			fmt.Printf("Server already running on %s:%d (PID %d)\n", state.Host, state.Port, state.PID)
		} else {
			fmt.Printf("Already connected to %s at %s:%d\n", b.DisplayName(), state.Host, state.Port)
		}
		return 0
	}
	if state.Managed {
		fmt.Printf("Server started on %s:%d (PID %d)\n", state.Host, state.Port, state.PID)
		fmt.Printf("Log: %s\n", state.LogFile)
	} else {
		fmt.Printf("Connected to %s at %s:%d\n", b.DisplayName(), state.Host, state.Port)
	}
	return 0
}

func cmdStop(cfg *Config, args []string) int {
	var backend string
	if len(args) > 0 {
		backend = args[0]
		if _, err := GetBackend(backend); err != nil {
			fmt.Fprintf(os.Stderr, "Error: unknown backend %q\n", backend)
			return 2
		}
	} else {
		states, _ := ReadAllStates()
		var running []*ServerState
		for _, s := range states {
			if IsServerAlive(s) {
				running = append(running, s)
			}
		}
		if len(running) == 0 {
			fmt.Println("No server running.")
			return 1
		}
		if len(running) > 1 {
			fmt.Fprintln(os.Stderr, "Multiple servers running — specify which to stop:")
			for _, s := range running {
				fmt.Fprintf(os.Stderr, "  %s at %s:%d\n", backendDisplayName(s.Backend), s.Host, s.Port)
			}
			return 2
		}
		backend = running[0].Backend
	}

	state, err := StopBackendServer(backend)
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			fmt.Println("No server running.")
			return 1
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if state.Managed {
		fmt.Printf("Stopped server (PID %d)\n", state.PID)
	} else {
		fmt.Printf("Disconnected from %s (server still running at %s:%d)\n",
			backendDisplayName(state.Backend), state.Host, state.Port)
	}
	return 0
}

func cmdStatus(cfg *Config) int {
	states, _ := ReadAllStates()
	stateMap := make(map[string]*ServerState)
	for _, s := range states {
		stateMap[s.Backend] = s
	}

	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		if cfg.IsServerEnabled(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	type probeResult struct {
		name    string
		healthy bool
		models  []RunningModelInfo
	}
	results := make(chan probeResult, len(names))
	for _, name := range names {
		go func(name string) {
			b, err := GetBackend(name)
			if err != nil {
				results <- probeResult{name: name}
				return
			}
			addr := cfg.ConfiguredBackendAddr(name)
			if s, ok := stateMap[name]; ok {
				addr = s.Addr()
			}
			healthy := b.HealthCheck(addr) == nil
			var models []RunningModelInfo
			if healthy {
				if ml, ok := b.(ModelLister); ok {
					models, _ = ml.ListRunningModels(addr)
				}
			}
			results <- probeResult{name: name, healthy: healthy, models: models}
		}(name)
	}
	healthMap := make(map[string]probeResult)
	for range names {
		r := <-results
		healthMap[r.name] = r
	}

	anyRunning := false

	maxLen := 0
	for _, name := range names {
		if n := len(backendDisplayName(name)); n > maxLen {
			maxLen = n
		}
	}

	for _, name := range names {
		b, err := GetBackend(name)
		if err != nil {
			continue
		}

		addr := cfg.ConfiguredBackendAddr(name)
		if s, ok := stateMap[name]; ok {
			addr = s.Addr()
		}

		r := healthMap[name]
		if r.healthy {
			anyRunning = true
			modelStr := ""
			if len(r.models) > 0 {
				modelNames := make([]string, len(r.models))
				for i, m := range r.models {
					modelNames[i] = m.Name
				}
				modelStr = strings.Join(modelNames, ", ")
			}
			if modelStr != "" {
				fmt.Printf("  ● %-*s  running    %-22s %s\n", maxLen, b.DisplayName(), addr, modelStr)
			} else {
				fmt.Printf("  ● %-*s  running    %s\n", maxLen, b.DisplayName(), addr)
			}
		} else {
			fmt.Printf("  ○ %-*s  stopped\n", maxLen, b.DisplayName())
			if _, ok := stateMap[name]; ok {
				removeBackendState(name)
			}
		}
	}

	for _, name := range names {
		s, ok := stateMap[name]
		if !ok || s.ActiveProfile == "" {
			continue
		}
		if !healthMap[name].healthy {
			continue
		}
		parts := []string{fmt.Sprintf("Active: %s", profileDisplayName(cfg, s.ActiveProfile))}
		if s.Managed {
			parts = append(parts, fmt.Sprintf("PID %d", s.PID))
			parts = append(parts, fmt.Sprintf("Uptime %s", formatUptime(s.Uptime())))
		}
		if s.LogFile != "" {
			parts = append(parts, fmt.Sprintf("Log %s", s.LogFile))
		}
		fmt.Println()
		fmt.Println(strings.Join(parts, " · "))
	}

	if !anyRunning {
		return 1
	}
	return 0
}

func cmdList(cfg *Config) int {
	names := cfg.ProfileNames()

	maxNameLen := 0
	maxTagLen := 0
	for _, name := range names {
		if len(name) > maxNameLen {
			maxNameLen = len(name)
		}
		p := cfg.Profiles[name]
		server := resolveProfileServer(cfg, &p)
		tag := backendDisplayName(server)
		if len(tag) > maxTagLen {
			maxTagLen = len(tag)
		}
	}

	fmt.Println("Profiles:")
	fmt.Println()
	for _, name := range names {
		p := cfg.Profiles[name]
		desc := p.Description
		if desc == "" {
			desc = "-"
		}
		server := resolveProfileServer(cfg, &p)
		fmt.Printf("  %-*s  [%-*s] %s\n", maxNameLen, name, maxTagLen, backendDisplayName(server), desc)
	}
	return 0
}

func cmdLogs(cfg *Config, args []string) int {
	follow := false
	var backend string
	for _, arg := range args {
		if arg == "--follow" || arg == "-f" {
			follow = true
		} else {
			backend = arg
		}
	}

	var state *ServerState
	if backend != "" {
		s, err := ReadBackendState(backend)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 3
		}
		state = s
	} else {
		states, _ := ReadAllStates()
		var withLogs []*ServerState
		for _, s := range states {
			if s.LogFile != "" {
				withLogs = append(withLogs, s)
			}
		}
		if len(withLogs) == 1 {
			state = withLogs[0]
		} else if len(withLogs) > 1 {
			for _, s := range withLogs {
				if IsServerAlive(s) {
					state = s
					break
				}
			}
			if state == nil {
				state = withLogs[0]
			}
		}
	}

	if state == nil {
		fmt.Println("No server running.")
		return 1
	}

	if !IsServerAlive(state) {
		if state.Managed {
			fmt.Fprintf(os.Stderr, "Notice: server exited unexpectedly (PID %d)\n", state.PID)
		}
		removeBackendState(state.Backend)
	}

	if state.LogFile == "" {
		fmt.Println("No log file available for external server.")
		return 1
	}

	if err := TailLog(state.LogFile, follow); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	return 0
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `
Usage: llama-launcher [--config path] [command] [args]

Commands:
  load <profile>        Start server with model (stops existing if different)
  unload [profile]      Unload model (for managed backends: stops server)
  start                 Start server without a model
  stop [backend]        Stop the server
  status                Show server and model status
  list                  List available profiles
  logs [backend] [-f]   Tail the server log
  version               Print version and exit

Run without arguments for interactive mode.
`)
}
