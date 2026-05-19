package launcher

import (
	"errors"
	"fmt"
	"os"
)

const Version = "1.0.0"

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

	if configPath == "" {
		configPath = os.Getenv("LLAMA_LAUNCHER_CONFIG")
	}
	if configPath == "" {
		configPath = DefaultConfigPath()
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		if errors.Is(err, ErrConfigNotFound) {
			return handleFirstRun(configPath)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
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
		return cmdStatus()
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

func handleFirstRun(configPath string) int {
	if err := GenerateExampleConfig(configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 2
	}
	fmt.Printf("Created example config at: %s\n", configPath)
	fmt.Println("Edit with your model paths and preferences, then run llama-launcher again.")
	return 2
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

	fmt.Printf("Loading %s...\n", profile.Name)
	state, started, err := LoadProfile(cfg, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}

	if started {
		fmt.Printf("Server started (PID %d)\n", state.PID)
	}
	if state.ActiveProfile == profile.Name {
		fmt.Printf("Loaded %s on %s:%d\n", profile.Name, state.Host, state.Port)
	}
	return 0
}

func cmdUnload() int {
	state, err := StopServer()
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			fmt.Println("No server running.")
			return 1
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	fmt.Printf("Model unloaded, server stopped (PID %d)\n", state.PID)
	return 0
}

func cmdStart(cfg *Config) int {
	fmt.Println("Starting server...")
	defaults := cfg.Defaults
	applyFallbacks(&defaults)
	profile := &ResolvedProfile{
		Backend:       cfg.DefaultBackend,
		ProfileParams: defaults,
	}
	state, started, err := EnsureServer(cfg, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if !started {
		fmt.Printf("Server already running on %s:%d (PID %d)\n", state.Host, state.Port, state.PID)
		return 0
	}
	fmt.Printf("Server started on %s:%d (PID %d)\n", state.Host, state.Port, state.PID)
	fmt.Printf("Log: %s\n", state.LogFile)
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
	fmt.Printf("Stopped server (PID %d)\n", state.PID)
	return 0
}

func cmdStatus() int {
	state, err := ReadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 3
	}
	if state == nil {
		fmt.Println("Status: stopped")
		return 1
	}
	if !IsProcessAlive(state.PID) {
		fmt.Printf("Status: stopped (server exited unexpectedly, PID %d)\n", state.PID)
		RemoveState()
		return 1
	}

	fmt.Println("Status:  running")
	if state.ActiveProfile != "" {
		fmt.Printf("Model:   %s\n", state.ActiveProfile)
	} else {
		fmt.Println("Model:   (none)")
	}
	fmt.Printf("Backend: %s\n", state.Backend)
	fmt.Printf("Server:  %s:%d\n", state.Host, state.Port)
	fmt.Printf("PID:     %d\n", state.PID)
	fmt.Printf("Uptime:  %s\n", formatUptime(state.Uptime()))
	fmt.Printf("Log:     %s\n", state.LogFile)
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
		backend := p.Backend
		if backend == "" {
			backend = cfg.DefaultBackend
		}
		fmt.Printf("  %-20s [%s] %s\n", name, backend, desc)
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

	if !IsProcessAlive(state.PID) {
		fmt.Fprintf(os.Stderr, "Notice: server exited unexpectedly (PID %d)\n", state.PID)
		RemoveState()
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

Run without arguments for interactive mode.
`)
}
