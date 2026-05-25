package launcher

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errUserQuit = errors.New("quit")

// RunInteractiveMenu presents a menu based on current server state.
// When auto_close is false, the menu re-displays after each action.
func RunInteractiveMenu(cfg *Config) error {
	for {
		cfg.Reload()
		states, _ := ReadAllStates()

		var primaryState *ServerState
		anyModel := false
		anyServer := false
		for _, s := range states {
			if IsServerAlive(s) {
				anyServer = true
				if s.ActiveModel != "" {
					anyModel = true
					if primaryState == nil {
						primaryState = s
					}
				}
				if primaryState == nil {
					primaryState = s
				}
			} else {
				if !isTerminal() {
					if s.Managed {
						fmt.Fprintf(os.Stderr, "Notice: server exited unexpectedly (PID %d)\n", s.PID)
					} else {
						fmt.Fprintf(os.Stderr, "Notice: %s server no longer reachable at %s\n", backendDisplayName(s.Backend), s.Addr())
					}
				}
				removeBackendState(s.Backend)
			}
		}

		var err error
		if !anyServer {
			err = runStoppedMenu(cfg)
		} else if anyModel {
			err = runLoadedMenu(cfg, primaryState)
		} else {
			err = runIdleMenu(cfg, primaryState)
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
		cfg.Reload()
		return serverStatusLines(cfg)
	}

	items := buildProfileItems(cfg, names)
	items = append(items, menuItem{Separator: true})
	if runningServers := detectRunningServers(cfg); len(runningServers) > 0 {
		items = append(items, menuItem{Label: "Stop server"})
	}
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
	case "Stop server":
		return doStopServer(cfg, nil)
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
	headerFn := func() []string {
		cfg.Reload()
		return serverStatusLines(cfg)
	}

	items := []menuItem{}
	if len(cfg.ProfileNames()) > 1 {
		items = append(items, menuItem{Label: "Switch model"})
	}
	items = append(items, menuItem{Label: "Unload model"})
	items = append(items, menuItem{Label: "Stop server"})
	if state.LogFile != "" {
		items = append(items, menuItem{Label: "Show log"})
	}
	items = append(items, menuItem{Label: "Show model config"})
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
	case "Unload model":
		return doUnloadModel(cfg)
	case "Stop server":
		return doStopServer(cfg, state)
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
	headerFn := func() []string {
		cfg.Reload()
		return serverStatusLines(cfg)
	}

	items := buildProfileItems(cfg, names)
	items = append(items, menuItem{Separator: true})
	items = append(items, menuItem{Label: "Stop server"})
	if state.LogFile != "" {
		items = append(items, menuItem{Label: "Show log"})
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
	case "Stop server":
		return doStopServer(cfg, state)
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

	var progress ProgressFunc
	if isTerminal() {
		_, progress = newTUIProgress(fmt.Sprintf("Loading %s", displayName))
	} else {
		progress = newCLIProgress(fmt.Sprintf("Loading %s", displayName))
	}
	state, started, err := LoadProfile(cfg, profile, progress)
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

type runningServer struct {
	name string
	addr string
}

func detectRunningServers(cfg *Config) []runningServer {
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

	type result struct {
		name string
		addr string
		ok   bool
	}
	ch := make(chan result, len(names))
	for _, name := range names {
		go func(name string) {
			b, err := GetBackend(name)
			if err != nil {
				ch <- result{name: name}
				return
			}
			addr := cfg.ConfiguredBackendAddr(name)
			if s, ok := stateMap[name]; ok {
				addr = s.Addr()
			}
			ch <- result{name: name, addr: addr, ok: b.HealthCheck(addr) == nil}
		}(name)
	}

	resultMap := make(map[string]result)
	for range names {
		r := <-ch
		resultMap[r.name] = r
	}

	var running []runningServer
	for _, name := range names {
		r := resultMap[name]
		if r.ok {
			running = append(running, runningServer{name: name, addr: r.addr})
		}
	}
	return running
}

func doStopServer(cfg *Config, state *ServerState) error {
	running := detectRunningServers(cfg)
	if len(running) == 0 {
		fmt.Print(escClear + escCursorShow)
		fmt.Println("  No servers running.")
		return nil
	}

	var target runningServer
	if len(running) == 1 {
		target = running[0]
	} else {
		items := make([]menuItem, len(running))
		for i, s := range running {
			items[i] = menuItem{
				Label:       backendDisplayName(s.name),
				Description: s.addr,
			}
		}
		title := fmt.Sprintf("%sllama-launcher %s%s%s", cBoldLightGray, cReset+cDim, Version, cReset)
		headerFn := func() []string {
			return []string{"Select a server to stop"}
		}
		idx := selectMenu(title, headerFn, items, "↑↓ select · enter stop · q cancel", cfg.ShouldDisplayCentered())
		if idx < 0 {
			return nil
		}
		target = running[idx]
	}

	var progress ProgressFunc
	if isTerminal() {
		_, progress = newTUIProgress(fmt.Sprintf("Stopping %s", backendDisplayName(target.name)))
	} else {
		progress = newCLIProgress(fmt.Sprintf("Stopping %s", backendDisplayName(target.name)))
	}

	st, err := StopBackendServer(target.name, progress)
	fmt.Print(escClear + escCursorShow)
	if err != nil {
		if errors.Is(err, ErrNotRunning) {
			b, berr := GetBackend(target.name)
			if berr != nil {
				return berr
			}
			b.TryStop(target.addr)
			fmt.Printf("  Stopped %s at %s\n", backendDisplayName(target.name), target.addr)
			return nil
		}
		return err
	}
	if st.Managed {
		fmt.Printf("  Stopped %s (PID %d)\n", backendDisplayName(target.name), st.PID)
	} else {
		fmt.Printf("  Disconnected from %s\n", backendDisplayName(target.name))
	}
	return nil
}

func doUnloadModel(cfg *Config) error {
	states, _ := ReadAllStates()
	var loaded []*ServerState
	for _, s := range states {
		if IsServerAlive(s) && s.ActiveModel != "" {
			loaded = append(loaded, s)
		}
	}

	if len(loaded) == 0 {
		fmt.Print(escClear + escCursorShow)
		fmt.Println("  No model loaded.")
		return nil
	}

	var target *ServerState
	if len(loaded) == 1 {
		target = loaded[0]
	} else {
		items := make([]menuItem, len(loaded))
		for i, s := range loaded {
			items[i] = menuItem{
				Label:       s.ActiveProfile,
				Description: profileDisplayName(cfg, s.ActiveProfile),
			}
		}
		title := fmt.Sprintf("%sllama-launcher %s%s%s", cBoldLightGray, cReset+cDim, Version, cReset)
		headerFn := func() []string {
			return []string{"Select a model to unload"}
		}
		idx := selectMenu(title, headerFn, items, "↑↓ select · enter unload · q cancel", cfg.ShouldDisplayCentered())
		if idx < 0 {
			return nil
		}
		target = loaded[idx]
	}

	b, err := GetBackend(target.Backend)
	if err != nil {
		return err
	}

	displayName := profileDisplayName(cfg, target.ActiveProfile)

	var progress ProgressFunc
	if isTerminal() {
		_, progress = newTUIProgress(fmt.Sprintf("Unloading %s", displayName))
	} else {
		progress = newCLIProgress(fmt.Sprintf("Unloading %s", displayName))
	}

	if _, ok := b.(ManagedBackend); ok {
		st, err := StopBackendServer(target.Backend, progress)
		fmt.Print(escClear + escCursorShow)
		if err != nil {
			return err
		}
		fmt.Printf("  Model unloaded, server stopped (PID %d)\n", st.PID)
	} else {
		st, err := UnloadBackendModel(target.Backend, progress)
		fmt.Print(escClear + escCursorShow)
		if err != nil {
			return err
		}
		fmt.Printf("  Model unloaded (server still running at %s:%d)\n", st.Host, st.Port)
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
				add("GPU offload", strconv.Itoa(*p.GPULayers))
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

// favouriteSuffix returns the trailing fragment appended to a description so
// that the ★ marker right-aligns across the profile list. Non-favourite rows
// return an empty suffix — the surrounding frame auto-pads them, and plain
// text output stays free of trailing whitespace.
func favouriteSuffix(fav bool, desc string, maxDescWidth int, anyFavourite bool) string {
	if !anyFavourite || !fav {
		return ""
	}
	pad := strings.Repeat(" ", maxDescWidth-visibleWidth(desc))
	return pad + " ★"
}

func anyProfileFavourite(cfg *Config, names []string) bool {
	for _, name := range names {
		if cfg.Profiles[name].IsFavourite {
			return true
		}
	}
	return false
}

func buildSimpleProfileLines(cfg *Config, names []string) []string {
	hasMixed := hasMultipleBackends(cfg)
	anyFav := anyProfileFavourite(cfg, names)

	maxNameLen := 0
	maxTagLen := 0
	for _, name := range names {
		if len(name) > maxNameLen {
			maxNameLen = len(name)
		}
		if hasMixed {
			p := cfg.Profiles[name]
			server := resolveProfileServer(cfg, &p)
			if tag := backendDisplayName(server); len(tag) > maxTagLen {
				maxTagLen = len(tag)
			}
		}
	}

	descs := make([]string, len(names))
	maxDescWidth := 0
	for i, name := range names {
		descs[i] = cfg.Profiles[name].Description
		if w := visibleWidth(descs[i]); w > maxDescWidth {
			maxDescWidth = w
		}
	}

	lines := make([]string, len(names))
	for i, name := range names {
		p := cfg.Profiles[name]
		desc := descs[i]
		suffix := favouriteSuffix(p.IsFavourite, desc, maxDescWidth, anyFav)
		if hasMixed {
			server := resolveProfileServer(cfg, &p)
			tag := backendDisplayName(server)
			lines[i] = fmt.Sprintf("%-*s  [%-*s] %s%s", maxNameLen, name, maxTagLen, tag, desc, suffix)
		} else {
			lines[i] = fmt.Sprintf("%-*s  %s%s", maxNameLen, name, desc, suffix)
		}
	}
	return lines
}

func buildProfileItems(cfg *Config, names []string) []menuItem {
	hasMixed := hasMultipleBackends(cfg)
	anyFav := anyProfileFavourite(cfg, names)

	maxTagLen := 0
	if hasMixed {
		for _, name := range names {
			p := cfg.Profiles[name]
			server := resolveProfileServer(cfg, &p)
			tag := backendDisplayName(server)
			if len(tag) > maxTagLen {
				maxTagLen = len(tag)
			}
		}
	}

	descs := make([]string, len(names))
	maxDescWidth := 0
	for i, name := range names {
		p := cfg.Profiles[name]
		desc := p.Description
		if hasMixed {
			server := resolveProfileServer(cfg, &p)
			tag := backendDisplayName(server)
			if desc != "" {
				desc = fmt.Sprintf("[%-*s] %s", maxTagLen, tag, desc)
			} else {
				desc = fmt.Sprintf("[%-*s]", maxTagLen, tag)
			}
		}
		descs[i] = desc
		if w := visibleWidth(desc); w > maxDescWidth {
			maxDescWidth = w
		}
	}

	items := make([]menuItem, 0, len(names))
	for i, name := range names {
		p := cfg.Profiles[name]
		suffix := favouriteSuffix(p.IsFavourite, descs[i], maxDescWidth, anyFav)
		items = append(items, menuItem{Label: name, Description: descs[i] + suffix})
	}
	return items
}

func hasMultipleBackends(cfg *Config) bool {
	seen := make(map[string]bool)
	for _, p := range cfg.Profiles {
		server := resolveProfileServer(cfg, &p)
		if !cfg.IsServerEnabled(server) {
			continue
		}
		seen[server] = true
		if len(seen) > 1 {
			return true
		}
	}
	return false
}

func backendDisplayName(backendName string) string {
	b, err := GetBackend(backendName)
	if err != nil {
		return backendName
	}
	return b.DisplayName()
}

func serverStatusLines(cfg *Config) []string {
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

	maxLen := 0
	for _, name := range names {
		if n := len(backendDisplayName(name)); n > maxLen {
			maxLen = n
		}
	}

	var lines []string
	for _, name := range names {
		b, err := GetBackend(name)
		if err != nil {
			continue
		}
		r := healthMap[name]
		addr := cfg.ConfiguredBackendAddr(name)
		if s, ok := stateMap[name]; ok {
			addr = s.Addr()
		}

		if r.healthy {
			modelStr := ""
			if len(r.models) > 0 {
				modelNames := make([]string, len(r.models))
				for i, m := range r.models {
					modelNames[i] = m.Name
				}
				modelStr = strings.Join(modelNames, ", ")
			}
			detail := addr
			if s, ok := stateMap[name]; ok && s.ActiveProfile != "" {
				detail = addr + " · " + fmt.Sprintf("%s%s%s", cBoldLightGray, profileDisplayName(cfg, s.ActiveProfile), cReset)
			}
			if modelStr != "" {
				detail += " · " + modelStr
			}
			serverName := fmt.Sprintf("%s%s%s", cBoldLightGray, b.DisplayName(), cReset)
			lines = append(lines, fmt.Sprintf("%s●%s %-*s  %s", cGreen, cReset, maxLen, serverName, detail))
		} else {
			lines = append(lines, fmt.Sprintf("%s○%s %-*s  %sstopped%s", cDim, cReset, maxLen, b.DisplayName(), cDim, cReset))
		}
	}
	return lines
}

// --- Simple (non-terminal) fallbacks ---

func runStoppedMenuSimple(cfg *Config, names []string) error {
	fmt.Printf("\nllama-launcher %s\n\n  Profiles:\n\n", Version)
	simpleItems := buildSimpleProfileLines(cfg, names)
	for i, line := range simpleItems {
		fmt.Printf("    %d  %s\n", i+1, line)
	}
	fmt.Printf("    e  Edit config\n    q  Quit\n\n  Select [1-%d, e, q]: ", len(names))

	choice := readLine()
	if choice == "q" || choice == "" {
		return nil
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

	n := 0
	switchIdx, unloadIdx, stopIdx, logIdx, configIdx, editIdx := -1, -1, -1, -1, -1, -1
	if len(cfg.ProfileNames()) > 1 {
		n++
		switchIdx = n
		fmt.Printf("    %d  Switch model\n", n)
	}
	n++
	unloadIdx = n
	fmt.Printf("    %d  Unload model\n", n)
	n++
	stopIdx = n
	fmt.Printf("    %d  Stop server\n", n)
	if state.LogFile != "" {
		n++
		logIdx = n
		fmt.Printf("    %d  Show log\n", n)
	}
	n++
	configIdx = n
	fmt.Printf("    %d  Show config\n", n)
	n++
	editIdx = n
	fmt.Printf("    %d  Edit config\n", n)
	fmt.Println("    q  Quit")
	fmt.Printf("\n  Select [1-%d, q]: ", n)

	choice, _ := strconv.Atoi(readLine())
	switch choice {
	case switchIdx:
		return doSwitchModel(cfg, state)
	case configIdx:
		return doShowConfig(cfg, state)
	case unloadIdx:
		return doUnloadModel(cfg)
	case stopIdx:
		return doStopServer(cfg, state)
	case logIdx:
		return TailLog(state.LogFile, false)
	case editIdx:
		return doEditConfig(cfg)
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
	simpleItems := buildSimpleProfileLines(cfg, names)
	for i, line := range simpleItems {
		fmt.Printf("    %d  %s\n", i+1, line)
	}
	fmt.Printf("    s  Stop server\n    e  Edit config\n    q  Quit\n\n  Select [1-%d, s, e, q]: ", len(names))

	choice := readLine()
	if choice == "q" || choice == "" {
		return nil
	}
	if choice == "s" {
		return doStopServer(cfg, state)
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
	simpleItems := buildSimpleProfileLines(cfg, available)
	for i, line := range simpleItems {
		fmt.Printf("    %d  %s\n", i+1, line)
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
