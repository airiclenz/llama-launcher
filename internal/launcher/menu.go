package launcher

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var errUserQuit = errors.New("quit")

// RunInteractiveMenu presents a menu based on current server state.
// When auto_close is false, the menu re-displays after each action.
func RunInteractiveMenu(cfg *Config) error {
	for {
		state, _ := ReadState()

		if state != nil && !IsServerAlive(state) {
			if !isTerminal() {
				if state.Managed {
					fmt.Fprintf(os.Stderr, "Notice: server exited unexpectedly (PID %d)\n", state.PID)
				} else {
					fmt.Fprintf(os.Stderr, "Notice: %s server no longer reachable at %s\n", backendDisplayName(state.Backend), state.Addr())
				}
			}
			RemoveState()
			state = nil
		}

		var err error
		if state == nil {
			err = runStoppedMenu(cfg)
		} else if state.ActiveModel != "" {
			err = runLoadedMenu(cfg, state)
		} else {
			err = runIdleMenu(cfg, state)
		}

		if err == errUserQuit || cfg.ShouldAutoClose() || !isTerminal() {
			if err == errUserQuit {
				err = nil
			}
			return err
		}
		if err != nil {
			showErrorPopup(err)
		}
	}
}

func runStoppedMenu(cfg *Config) error {
	names := cfg.ProfileNames()

	if !isTerminal() {
		return runStoppedMenuSimple(cfg, names)
	}

	title := fmt.Sprintf("%sllama-launcher %s%s%s", cBoldLightGray, cReset+cDim, Version, cReset)
	headerFn := func() []string {
		return []string{
			fmt.Sprintf("Status  %s● stopped%s", cRed, cReset),
		}
	}

	items := buildProfileItems(cfg, names)
	items = append(items, menuItem{Separator: true})
	items = append(items, menuItem{Label: "Start server only"})
	items = append(items, menuItem{Label: "Edit config"})

	idx := selectMenu(title, headerFn, items, "↑↓ select · enter start & load · q quit", cfg.ShouldDisplayCentered())

	if idx < 0 {
		fmt.Print(escClear + escCursorShow)
		return errUserQuit
	}

	if idx < len(names) {
		return doLoadProfile(cfg, names[idx])
	}

	item := items[idx]
	switch item.Label {
	case "Start server only":
		return doStartOnly(cfg)
	case "Edit config":
		return doEditConfig(cfg)
	}
	return nil
}

func runLoadedMenu(cfg *Config, state *ServerState) error {
	if !isTerminal() {
		return runLoadedMenuSimple(cfg, state)
	}

	title := fmt.Sprintf("%sllama-launcher %s%s%s", cBoldLightGray, cReset+cDim, Version, cReset)
	modelLabel := profileDisplayName(cfg, state.ActiveProfile)
	displayName := backendDisplayName(state.Backend)
	headerFn := func() []string {
		if !IsServerAlive(state) {
			return []string{
				fmt.Sprintf("Status   %s● stopped%s", cRed, cReset),
			}
		}
		lines := []string{
			fmt.Sprintf("Status   %s● running%s", cGreen, cReset),
			fmt.Sprintf("Model    %s", modelLabel),
		}
		if state.Managed {
			lines = append(lines, fmt.Sprintf("Server   %s · %s:%d · PID %d · Uptime %s",
				displayName, state.Host, state.Port, state.PID, formatUptime(state.Uptime())))
		} else {
			lines = append(lines, fmt.Sprintf("Server   %s · %s:%d",
				displayName, state.Host, state.Port))
		}
		return lines
	}

	items := []menuItem{
		{Label: "Switch model"},
		{Label: "Show model config"},
	}
	if state.Managed {
		items = append(items, menuItem{Label: "Stop server"})
		items = append(items, menuItem{Label: "Show log"})
	} else {
		items = append(items, menuItem{Label: "Disconnect"})
	}
	items = append(items, menuItem{Label: "Edit config"})

	idx := selectMenu(title, headerFn, items, "↑↓ select · enter confirm · q quit", cfg.ShouldDisplayCentered())

	if idx < 0 {
		fmt.Print(escClear + escCursorShow)
		return errUserQuit
	}

	item := items[idx]
	switch item.Label {
	case "Switch model":
		return doSwitchModel(cfg, state)
	case "Show model config":
		return doShowConfig(cfg, state)
	case "Stop server", "Disconnect":
		return doStop()
	case "Show log":
		fmt.Print(escClear + escCursorShow)
		return TailLog(state.LogFile, true)
	case "Edit config":
		return doEditConfig(cfg)
	}
	return errUserQuit
}

func runIdleMenu(cfg *Config, state *ServerState) error {
	names := cfg.ProfileNames()

	if !isTerminal() {
		return runIdleMenuSimple(cfg, state, names)
	}

	title := fmt.Sprintf("%sllama-launcher %s%s%s", cBoldLightGray, cReset+cDim, Version, cReset)
	displayName := backendDisplayName(state.Backend)
	headerFn := func() []string {
		if !IsServerAlive(state) {
			return []string{
				fmt.Sprintf("Status   %s● stopped%s", cRed, cReset),
			}
		}
		lines := []string{
			fmt.Sprintf("Status   %s● running%s %s(no model)%s", cGreen, cReset, cDim, cReset),
		}
		if state.Managed {
			lines = append(lines, fmt.Sprintf("Server   %s · %s:%d · PID %d · Uptime %s",
				displayName, state.Host, state.Port, state.PID, formatUptime(state.Uptime())))
		} else {
			lines = append(lines, fmt.Sprintf("Server   %s · %s:%d",
				displayName, state.Host, state.Port))
		}
		return lines
	}

	items := buildProfileItems(cfg, names)
	items = append(items, menuItem{Separator: true})
	if state.Managed {
		items = append(items, menuItem{Label: "Stop server"})
		items = append(items, menuItem{Label: "Show log"})
	} else {
		items = append(items, menuItem{Label: "Disconnect"})
	}
	items = append(items, menuItem{Label: "Edit config"})

	idx := selectMenu(title, headerFn, items, "↑↓ select · enter load · q quit", cfg.ShouldDisplayCentered())

	if idx < 0 {
		fmt.Print(escClear + escCursorShow)
		return errUserQuit
	}

	if idx < len(names) {
		return doLoadProfile(cfg, names[idx])
	}

	item := items[idx]
	switch item.Label {
	case "Stop server", "Disconnect":
		return doStop()
	case "Show log":
		fmt.Print(escClear + escCursorShow)
		return TailLog(state.LogFile, true)
	case "Edit config":
		return doEditConfig(cfg)
	}
	return nil
}

func doSwitchModel(cfg *Config, currentState *ServerState) error {
	names := cfg.ProfileNames()
	var available []string
	for _, name := range names {
		if name != currentState.ActiveProfile {
			available = append(available, name)
		}
	}

	if len(available) == 0 {
		fmt.Println("No other profiles available.")
		return nil
	}

	if !isTerminal() {
		return doSwitchSimple(cfg, available)
	}

	items := buildProfileItems(cfg, available)

	idx := selectMenu("Switch model", nil, items, "↑↓ select · enter confirm · q cancel", cfg.ShouldDisplayCentered())

	if idx < 0 || idx >= len(available) {
		fmt.Print(escClear + escCursorShow)
		return nil
	}

	return doLoadProfile(cfg, available[idx])
}

func doLoadProfile(cfg *Config, name string) error {
	profile, err := cfg.ResolveProfile(name)
	if err != nil {
		return err
	}

	displayName := profile.Description
	if displayName == "" {
		displayName = name
	}
	showActivity(fmt.Sprintf("Loading %s...", displayName))
	state, started, err := LoadProfile(cfg, profile)
	fmt.Print(escClear + escCursorShow)
	if err != nil {
		return err
	}

	if started && state.Managed {
		fmt.Printf("  %s●%s Server started (PID %d)\n", cGreen, cReset, state.PID)
	} else if started && !state.Managed {
		fmt.Printf("  %s●%s Connected to %s\n", cGreen, cReset, backendDisplayName(state.Backend))
	}
	fmt.Printf("  %s●%s Loaded %s on %s:%d\n", cGreen, cReset, displayName, state.Host, state.Port)
	if state.LogFile != "" {
		fmt.Printf("    Log: %s\n", state.LogFile)
	}
	return nil
}

func doStartOnly(cfg *Config) error {
	if cfg.Defaults.Server == nil {
		return fmt.Errorf("no default server configured in defaults section")
	}
	serverName := *cfg.Defaults.Server
	showActivity("Starting server...")
	defaults := cfg.Defaults
	b, err := GetBackend(serverName)
	if err != nil {
		return err
	}
	applyBackendFallbacks(&defaults, cfg, serverName, b)
	profile := &ResolvedProfile{
		Backend:       serverName,
		ProfileParams: defaults,
	}
	state, _, err := EnsureServer(cfg, profile)
	fmt.Print(escClear + escCursorShow)
	if err != nil {
		return err
	}
	if state.Managed {
		fmt.Printf("  %s●%s Server started on %s:%d (PID %d)\n", cGreen, cReset, state.Host, state.Port, state.PID)
		fmt.Printf("    Log: %s\n", state.LogFile)
	} else {
		fmt.Printf("  %s●%s Connected to %s at %s:%d\n", cGreen, cReset, b.DisplayName(), state.Host, state.Port)
	}
	return nil
}

func doStop() error {
	showActivity("Stopping server...")
	state, err := StopServer()
	fmt.Print(escClear + escCursorShow)
	if err != nil {
		return err
	}
	if state.Managed {
		fmt.Printf("  Stopped server (PID %d)\n", state.PID)
	} else {
		fmt.Printf("  Disconnected from %s (server still running at %s:%d)\n",
			backendDisplayName(state.Backend), state.Host, state.Port)
	}
	return nil
}

func doShowConfig(cfg *Config, state *ServerState) error {
	profile, err := cfg.ResolveProfile(state.ActiveProfile)
	if err != nil {
		return err
	}
	showPopup(profile.Description, formatProfileParams(profile))
	return nil
}

func doEditConfig(cfg *Config) error {
	fmt.Print(escClear + escCursorShow)
	return exec.Command("open", cfg.ConfigPath).Run()
}

func formatProfileParams(profile *ResolvedProfile) []string {
	p := &profile.ProfileParams
	var lines []string

	add := func(label, value string) {
		lines = append(lines, fmt.Sprintf("%s%-18s%s %s", cDim, label, cReset, value))
	}

	add("Backend", backendDisplayName(profile.Backend))
	add("Model", profile.ModelPath)

	if p.ContextSize != nil {
		add("Context size", strconv.Itoa(*p.ContextSize))
	}

	isLlamaCpp := profile.Backend == "llamacpp"
	isLMStudio := profile.Backend == "lmstudio"

	if p.GPULayers != nil {
		if isLlamaCpp {
			add("GPU layers", strconv.Itoa(*p.GPULayers))
		} else if isLMStudio {
			switch {
			case *p.GPULayers >= 99:
				add("GPU offload", "max")
			case *p.GPULayers <= 0:
				add("GPU offload", "off")
			default:
				add("GPU offload", "max")
			}
		}
	}
	if isLlamaCpp && p.Threads != nil {
		add("Threads", strconv.Itoa(*p.Threads))
	}
	if isLlamaCpp && p.ThreadsBatch != nil {
		add("Threads (batch)", strconv.Itoa(*p.ThreadsBatch))
	}
	if (isLlamaCpp || isLMStudio) && p.BatchSize != nil {
		add("Batch size", strconv.Itoa(*p.BatchSize))
	}
	if (isLlamaCpp || isLMStudio) && p.FlashAttn != nil {
		add("Flash attention", strconv.FormatBool(*p.FlashAttn))
	}
	if isLlamaCpp && p.ContBatching != nil {
		add("Cont. batching", strconv.FormatBool(*p.ContBatching))
	}
	if isLlamaCpp && p.Parallel != nil {
		add("Parallel", strconv.Itoa(*p.Parallel))
	}
	if isLlamaCpp && p.Mlock != nil {
		add("Mlock", strconv.FormatBool(*p.Mlock))
	}
	if isLlamaCpp && p.NoMmap != nil {
		add("No mmap", strconv.FormatBool(*p.NoMmap))
	}
	if isLlamaCpp && p.Embedding != nil {
		add("Embedding", strconv.FormatBool(*p.Embedding))
	}
	if isLlamaCpp && p.Jinja != nil {
		add("Jinja", strconv.FormatBool(*p.Jinja))
	}

	if len(profile.ExtraArgs) > 0 {
		add("Extra args", strings.Join(profile.ExtraArgs, " "))
	}

	return lines
}

func profileDisplayName(cfg *Config, profileName string) string {
	if p, ok := cfg.Profiles[profileName]; ok && p.Description != "" {
		return p.Description
	}
	return profileName
}

func buildProfileItems(cfg *Config, names []string) []menuItem {
	hasMixed := hasMultipleBackends(cfg)
	defaultServer := resolveDefaultServer(cfg)
	items := make([]menuItem, 0, len(names))
	for _, name := range names {
		p := cfg.Profiles[name]
		desc := p.Description
		if hasMixed {
			server := resolveProfileServer(cfg, &p)
			if server != defaultServer {
				tag := backendDisplayName(server)
				if desc != "" {
					desc = fmt.Sprintf("[%s] %s", tag, desc)
				} else {
					desc = fmt.Sprintf("[%s]", tag)
				}
			}
		}
		items = append(items, menuItem{Label: name, Description: desc})
	}
	return items
}

func hasMultipleBackends(cfg *Config) bool {
	seen := make(map[string]bool)
	for _, p := range cfg.Profiles {
		server := resolveProfileServer(cfg, &p)
		seen[server] = true
		if len(seen) > 1 {
			return true
		}
	}
	return false
}

func resolveDefaultServer(cfg *Config) string {
	if cfg.Defaults.Server != nil {
		return *cfg.Defaults.Server
	}
	return ""
}

func resolveProfileServer(cfg *Config, p *Profile) string {
	if p.Server != nil {
		return *p.Server
	}
	return resolveDefaultServer(cfg)
}

func backendDisplayName(backendName string) string {
	b, err := GetBackend(backendName)
	if err != nil {
		return backendName
	}
	return b.DisplayName()
}

// --- Simple (non-terminal) fallbacks ---

func runStoppedMenuSimple(cfg *Config, names []string) error {
	fmt.Printf("\nllama-launcher %s\n\n  Status: stopped\n\n  Profiles:\n\n", Version)
	for i, name := range names {
		fmt.Printf("    %d  %-20s %s\n", i+1, name, cfg.Profiles[name].Description)
	}
	fmt.Printf("    s  Start server only\n    e  Edit config\n    q  Quit\n\n  Select [1-%d, s, e, q]: ", len(names))

	choice := readLine()
	if choice == "q" || choice == "" {
		return nil
	}
	if choice == "s" {
		return doStartOnly(cfg)
	}
	if choice == "e" {
		return doEditConfig(cfg)
	}
	idx := parseChoice(choice, len(names))
	if idx < 0 {
		return fmt.Errorf("invalid selection: %s", choice)
	}
	return doLoadProfile(cfg, names[idx])
}

func runLoadedMenuSimple(cfg *Config, state *ServerState) error {
	fmt.Printf("\nllama-launcher %s\n\n", Version)
	displayName := backendDisplayName(state.Backend)
	if state.Managed {
		fmt.Printf("  Status:  running\n  Model:   %s\n  Server:  %s · %s:%d · PID %d · Uptime %s\n\n",
			profileDisplayName(cfg, state.ActiveProfile),
			displayName, state.Host, state.Port, state.PID, formatUptime(state.Uptime()))
	} else {
		fmt.Printf("  Status:  running\n  Model:   %s\n  Server:  %s · %s:%d\n\n",
			profileDisplayName(cfg, state.ActiveProfile),
			displayName, state.Host, state.Port)
	}

	n := 1
	fmt.Printf("    %d  Switch model\n", n)
	n++
	fmt.Printf("    %d  Show config\n", n)
	n++
	if state.Managed {
		fmt.Printf("    %d  Stop server\n", n)
		n++
		fmt.Printf("    %d  Show log\n", n)
		n++
	} else {
		fmt.Printf("    %d  Disconnect\n", n)
		n++
	}
	fmt.Printf("    %d  Edit config\n", n)
	fmt.Println("    q  Quit")
	fmt.Printf("\n  Select [1-%d, q]: ", n)

	choice := readLine()
	switch choice {
	case "1":
		return doSwitchModel(cfg, state)
	case "2":
		return doShowConfig(cfg, state)
	case "3":
		if state.Managed {
			return doStop()
		}
		return doStop()
	case "4":
		if state.Managed {
			return TailLog(state.LogFile, false)
		}
		return doEditConfig(cfg)
	case "5":
		if state.Managed {
			return doEditConfig(cfg)
		}
	}
	return nil
}

func runIdleMenuSimple(cfg *Config, state *ServerState, names []string) error {
	fmt.Printf("\nllama-launcher %s\n\n", Version)
	displayName := backendDisplayName(state.Backend)
	if state.Managed {
		fmt.Printf("  Status:  running (no model)\n  Server:  %s · %s:%d · PID %d · Uptime %s\n\n  Load a profile:\n\n",
			displayName, state.Host, state.Port, state.PID, formatUptime(state.Uptime()))
	} else {
		fmt.Printf("  Status:  running (no model)\n  Server:  %s · %s:%d\n\n  Load a profile:\n\n",
			displayName, state.Host, state.Port)
	}
	for i, name := range names {
		fmt.Printf("    %d  %-20s %s\n", i+1, name, cfg.Profiles[name].Description)
	}
	fmt.Printf("    s  Stop server\n    e  Edit config\n    q  Quit\n\n  Select [1-%d, s, e, q]: ", len(names))

	choice := readLine()
	if choice == "q" || choice == "" {
		return nil
	}
	if choice == "s" {
		return doStop()
	}
	if choice == "e" {
		return doEditConfig(cfg)
	}
	idx := parseChoice(choice, len(names))
	if idx < 0 {
		return fmt.Errorf("invalid selection: %s", choice)
	}
	return doLoadProfile(cfg, names[idx])
}

func doSwitchSimple(cfg *Config, available []string) error {
	fmt.Println()
	fmt.Println("  Switch to:")
	fmt.Println()
	for i, name := range available {
		fmt.Printf("    %d  %-20s %s\n", i+1, name, cfg.Profiles[name].Description)
	}
	fmt.Printf("    q  Cancel\n\n  Select [1-%d, q]: ", len(available))

	choice := readLine()
	if choice == "q" || choice == "" {
		return nil
	}
	idx := parseChoice(choice, len(available))
	if idx < 0 {
		return fmt.Errorf("invalid selection: %s", choice)
	}
	return doLoadProfile(cfg, available[idx])
}

func readLine() string {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

func parseChoice(s string, max int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < 1 || n > max {
		return -1
	}
	return n - 1
}

func formatUptime(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm %02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
