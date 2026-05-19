package launcher

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var errUserQuit = errors.New("quit")

// RunInteractiveMenu presents a menu based on current server state.
// When auto_close is false, the menu re-displays after each action.
func RunInteractiveMenu(cfg *Config) error {
	for {
		state, _ := ReadState()

		if state != nil && !IsProcessAlive(state.PID) {
			if !isTerminal() {
				fmt.Fprintf(os.Stderr, "Notice: server exited unexpectedly (PID %d)\n", state.PID)
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
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	}
}

func runStoppedMenu(cfg *Config) error {
	names := cfg.ProfileNames()

	if !isTerminal() {
		return runStoppedMenuSimple(cfg, names)
	}

	title := fmt.Sprintf("llama-launcher %s", Version)
	headerFn := func() []string {
		return []string{
			fmt.Sprintf("Status  %s● stopped%s", cRed, cReset),
		}
	}

	items := buildProfileItems(cfg, names)
	items = append(items, menuItem{Separator: true})
	items = append(items, menuItem{Label: "Start server only"})

	idx := selectMenu(title, headerFn, items, "↑↓ select · enter start & load · q quit", cfg.ShouldDisplayCentered())

	if idx < 0 {
		fmt.Print(escClear + escCursorShow)
		return errUserQuit
	}

	if idx < len(names) {
		return doLoadProfile(cfg, names[idx])
	}

	return doStartOnly(cfg)
}

func runLoadedMenu(cfg *Config, state *ServerState) error {
	if !isTerminal() {
		return runLoadedMenuSimple(cfg, state)
	}

	title := fmt.Sprintf("llama-launcher %s", Version)
	headerFn := func() []string {
		if !IsProcessAlive(state.PID) {
			return []string{
				fmt.Sprintf("Status   %s● stopped%s", cRed, cReset),
			}
		}
		return []string{
			fmt.Sprintf("Status   %s● running%s", cGreen, cReset),
			fmt.Sprintf("Model    %s", state.ActiveProfile),
			fmt.Sprintf("Server   %s:%d  PID %d  Uptime %s",
				state.Host, state.Port, state.PID, formatUptime(state.Uptime())),
		}
	}

	items := []menuItem{
		{Label: "Switch model"},
		{Label: "Stop server"},
		{Label: "Show log"},
	}

	idx := selectMenu(title, headerFn, items, "↑↓ select · enter confirm · q quit", cfg.ShouldDisplayCentered())

	if idx < 0 {
		fmt.Print(escClear + escCursorShow)
		return errUserQuit
	}

	switch idx {
	case 0:
		return doSwitchModel(cfg, state)
	case 1:
		return doStop()
	case 2:
		fmt.Print(escClear + escCursorShow)
		return TailLog(state.LogFile, false)
	}
	return errUserQuit
}

func runIdleMenu(cfg *Config, state *ServerState) error {
	names := cfg.ProfileNames()

	if !isTerminal() {
		return runIdleMenuSimple(cfg, state, names)
	}

	title := fmt.Sprintf("llama-launcher %s", Version)
	headerFn := func() []string {
		if !IsProcessAlive(state.PID) {
			return []string{
				fmt.Sprintf("Status   %s● stopped%s", cRed, cReset),
			}
		}
		return []string{
			fmt.Sprintf("Status   %s● running%s %s(no model)%s", cGreen, cReset, cDim, cReset),
			fmt.Sprintf("Server   %s:%d  PID %d  Uptime %s",
				state.Host, state.Port, state.PID, formatUptime(state.Uptime())),
		}
	}

	items := buildProfileItems(cfg, names)
	items = append(items, menuItem{Separator: true})
	items = append(items, menuItem{Label: "Stop server"})
	items = append(items, menuItem{Label: "Show log"})

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
	case "Stop server":
		return doStop()
	case "Show log":
		fmt.Print(escClear + escCursorShow)
		return TailLog(state.LogFile, false)
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

	showActivity(fmt.Sprintf("Loading %s...", name))
	state, started, err := LoadProfile(cfg, profile)
	fmt.Print(escClear + escCursorShow)
	if err != nil {
		return err
	}

	if started {
		fmt.Printf("  %s●%s Server started (PID %d)\n", cGreen, cReset, state.PID)
	}
	fmt.Printf("  %s●%s Loaded %s on %s:%d\n", cGreen, cReset, name, state.Host, state.Port)
	fmt.Printf("    Log: %s\n", state.LogFile)
	return nil
}

func doStartOnly(cfg *Config) error {
	showActivity("Starting server...")
	defaults := cfg.Defaults
	applyFallbacks(&defaults)
	profile := &ResolvedProfile{
		Backend:       cfg.DefaultBackend,
		ProfileParams: defaults,
	}
	state, _, err := EnsureServer(cfg, profile)
	fmt.Print(escClear + escCursorShow)
	if err != nil {
		return err
	}
	fmt.Printf("  %s●%s Server started on %s:%d (PID %d)\n", cGreen, cReset, state.Host, state.Port, state.PID)
	fmt.Printf("    Log: %s\n", state.LogFile)
	return nil
}

func doStop() error {
	showActivity("Stopping server...")
	state, err := StopServer()
	fmt.Print(escClear + escCursorShow)
	if err != nil {
		return err
	}
	fmt.Printf("  Stopped server (PID %d)\n", state.PID)
	return nil
}

func buildProfileItems(cfg *Config, names []string) []menuItem {
	items := make([]menuItem, 0, len(names))
	for _, name := range names {
		p := cfg.Profiles[name]
		items = append(items, menuItem{Label: name, Description: p.Description})
	}
	return items
}

// --- Simple (non-terminal) fallbacks ---

func runStoppedMenuSimple(cfg *Config, names []string) error {
	fmt.Printf("\nllama-launcher %s\n\n  Status: stopped\n\n  Profiles:\n\n", Version)
	for i, name := range names {
		fmt.Printf("    %d  %-20s %s\n", i+1, name, cfg.Profiles[name].Description)
	}
	fmt.Printf("    s  Start server only\n    q  Quit\n\n  Select [1-%d, s, q]: ", len(names))

	choice := readLine()
	if choice == "q" || choice == "" {
		return nil
	}
	if choice == "s" {
		return doStartOnly(cfg)
	}
	idx := parseChoice(choice, len(names))
	if idx < 0 {
		return fmt.Errorf("invalid selection: %s", choice)
	}
	return doLoadProfile(cfg, names[idx])
}

func runLoadedMenuSimple(cfg *Config, state *ServerState) error {
	fmt.Printf("\nllama-launcher %s\n\n", Version)
	fmt.Printf("  Status:  running\n  Model:   %s\n  Server:  %s:%d  PID %d  Uptime %s\n\n",
		state.ActiveProfile,
		state.Host, state.Port, state.PID, formatUptime(state.Uptime()))
	fmt.Println("    1  Switch model\n    2  Stop server\n    3  Show log\n    q  Quit")
	fmt.Print("\n  Select [1-3, q]: ")

	switch readLine() {
	case "1":
		return doSwitchModel(cfg, state)
	case "2":
		return doStop()
	case "3":
		return TailLog(state.LogFile, false)
	}
	return nil
}

func runIdleMenuSimple(cfg *Config, state *ServerState, names []string) error {
	fmt.Printf("\nllama-launcher %s\n\n", Version)
	fmt.Printf("  Status:  running (no model)\n  Server:  %s:%d  PID %d  Uptime %s\n\n  Load a profile:\n\n",
		state.Host, state.Port, state.PID, formatUptime(state.Uptime()))
	for i, name := range names {
		fmt.Printf("    %d  %-20s %s\n", i+1, name, cfg.Profiles[name].Description)
	}
	fmt.Printf("    s  Stop server\n    q  Quit\n\n  Select [1-%d, s, q]: ", len(names))

	choice := readLine()
	if choice == "q" || choice == "" {
		return nil
	}
	if choice == "s" {
		return doStop()
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
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm %ds", m, int(d.Seconds())%60)
}
