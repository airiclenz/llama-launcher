package launcher

import (
	"errors"
	"fmt"
	"os"
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
		return cmdUnload()
	case "start":
		return cmdStart(cfg)
	case "stop":
		return cmdStop()
	case "status":
		return cmdStatus(cfg)
	case "list":
		return cmdList(cfg)
	case "logs":
		return cmdLogs(args[1:])
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

func cmdUnload() int {
	state, err := ReadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if state == nil {
		fmt.Println("No server running.")
		return 1
	}

	if state.Managed {
		state, err = StopServer()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 3
		}
		fmt.Printf("Model unloaded, server stopped (PID %d)\n", state.PID)
	} else {
		state, err = UnloadCurrentModel()
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

func cmdStop() int {
	state, err := StopServer()
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
	state, err := ReadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if state == nil {
		fmt.Println("Status: stopped")
		return 1
	}
	if !IsServerAlive(state) {
		if state.Managed {
			fmt.Printf("Status: stopped (server exited unexpectedly, PID %d)\n", state.PID)
		} else {
			fmt.Printf("Status: stopped (%s no longer reachable at %s:%d)\n",
				backendDisplayName(state.Backend), state.Host, state.Port)
		}
		RemoveState()
		return 1
	}

	if state.Managed {
		fmt.Println("Status:  running")
	} else {
		fmt.Println("Status:  connected")
	}
	if state.ActiveProfile != "" {
		fmt.Printf("Model:   %s\n", profileDisplayName(cfg, state.ActiveProfile))
	} else {
		fmt.Println("Model:   (none)")
	}
	fmt.Printf("Backend: %s\n", backendDisplayName(state.Backend))
	fmt.Printf("Server:  %s:%d\n", state.Host, state.Port)
	if state.Managed {
		fmt.Printf("PID:     %d\n", state.PID)
		fmt.Printf("Uptime:  %s\n", formatUptime(state.Uptime()))
	}
	if state.LogFile != "" {
		fmt.Printf("Log:     %s\n", state.LogFile)
	}
	return 0
}

func cmdList(cfg *Config) int {
	names := cfg.ProfileNames()
	fmt.Println("Profiles:")
	fmt.Println()
	for _, name := range names {
		p := cfg.Profiles[name]
		desc := p.Description
		if desc == "" {
			desc = "-"
		}
		server := resolveProfileServer(cfg, &p)
		fmt.Printf("  %-20s [%s] %s\n", name, backendDisplayName(server), desc)
	}
	return 0
}

func cmdLogs(args []string) int {
	follow := false
	for _, arg := range args {
		if arg == "--follow" || arg == "-f" {
			follow = true
		}
	}

	state, err := ReadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if state == nil {
		fmt.Println("No server running.")
		return 1
	}

	if !IsServerAlive(state) {
		if state.Managed {
			fmt.Fprintf(os.Stderr, "Notice: server exited unexpectedly (PID %d)\n", state.PID)
		}
		RemoveState()
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
  load <profile>   Start server with model (stops existing if different)
  unload           Stop server and unload model
  start            Start server without a model
  stop             Stop the server
  status           Show server and model status
  list             List available profiles
  logs [--follow]  Tail the server log
  version          Print version and exit

Run without arguments for interactive mode.
`)
}
