package launcher

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// RunningInstance is a transient, in-memory snapshot of an LLM-server
// instance discovered at runtime. It is never persisted — every command
// rebuilds it from live probes of configured backend addresses (see
// DiscoverRunningInstances). Optional fields (PID, StartedAt, LogFile) are
// filled lazily and may be zero when the launcher has not needed them.
type RunningInstance struct {
	Backend       string
	Host          string
	Port          int
	PID           int
	StartedAt     time.Time
	LogFile       string
	ActiveProfile string
	ActiveModel   string
	// Starting marks an instance whose process is up but whose health check
	// does not pass yet — llama-server holds its address and answers /health
	// with 503 for the whole model load; a loading Splash has not bound its
	// address yet and is found by its command line instead. See ADR-0010
	// and ADR-0015.
	Starting bool
	// AuthFailed marks an address whose server answers every configured
	// backend's health check with 401/403 — it refuses the configured
	// api_key, so no backend can identify it. Such a row has Backend "", no
	// model or profile, and is at most one per address; an explicit stop
	// signals its listener, while load and unload refuse with the auth
	// error and the auto_stop_server sweep leaves it alone.
	AuthFailed bool

	// exit is the reaped exit of a server this launcher process forked
	// (startManagedServer); nil for every discovered or connected instance.
	exit *processExit
}

// exited returns the channel that closes when the instance's forked server
// exits, or nil — which never fires — when this process did not fork it.
func (r *RunningInstance) exited() <-chan struct{} {
	if r.exit == nil {
		return nil
	}
	return r.exit.done
}

func (r *RunningInstance) Addr() string {
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}

func (r *RunningInstance) Uptime() time.Duration {
	if r.StartedAt.IsZero() {
		return 0
	}
	return time.Since(r.StartedAt).Truncate(time.Second)
}

// DiscoverRunningInstances probes every (backend, addr) pair derived from
// the config and returns one RunningInstance per reachable server. The set
// of addresses probed per backend is the union of:
//   - the backend's configured address (cfg.ConfiguredBackendAddr)
//   - every distinct host:port a profile for that backend would resolve to
//
// Probes run in parallel. A backend whose probe finds neither a healthy nor
// a Starting server of its own contributes no row, with one exception: an
// address where every probing backend's health check answered 401/403 is a
// server refusing the configured api_key, and it yields exactly one row with
// AuthFailed set, Backend "" and no model or profile. That row sorts first
// (Backend "" precedes every name).
func DiscoverRunningInstances(cfg *Config) []*RunningInstance {
	results := probeTargets(cfg, discoveryTargets(cfg))

	var instances []*RunningInstance
	for _, r := range results {
		if r.instance != nil {
			instances = append(instances, r.instance)
		}
	}
	instances = append(instances, authFailedRows(results)...)

	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Backend != instances[j].Backend {
			return instances[i].Backend < instances[j].Backend
		}
		return instances[i].Addr() < instances[j].Addr()
	})
	return instances
}

// authRefusalAt reports whether the server at addr refuses the configured
// api_key, by the same rule discovery applies to its AuthFailed rows: every
// backend the config points at addr answers its health check with 401/403.
// It returns that probe's error (the authFailedErr message, wrapping
// ErrAuthFailed) prefixed with the address, or nil — also when any backend
// finds a healthy or Starting server of its own there, or when the config
// points no backend at addr.
func authRefusalAt(cfg *Config, addr string) error {
	var targets []discoveryTarget
	for _, t := range discoveryTargets(cfg) {
		if t.addr() == addr {
			targets = append(targets, t)
		}
	}
	results := probeTargets(cfg, targets)
	if len(results) == 0 || !everyProbeRefusedAuth(results) {
		return nil
	}
	return fmt.Errorf("server at %s: %w", addr, results[0].authErr)
}

// discoveryTarget is one (backend, addr) pair discovery probes.
type discoveryTarget struct {
	backend string
	server  LLMServer
	host    string
	port    int
}

func (t discoveryTarget) addr() string {
	return fmt.Sprintf("%s:%d", t.host, t.port)
}

// discoveryTargets derives the distinct (backend, addr) pairs to probe from
// the config (see DiscoverRunningInstances), sorted by backend name and
// then address so every consumer sees them in a deterministic order.
// Backends that are not registered are left out — nothing could probe them.
func discoveryTargets(cfg *Config) []discoveryTarget {
	seen := make(map[string]discoveryTarget)
	add := func(backend, host string, port int) {
		server, err := GetLLMServer(backend)
		if err != nil {
			return
		}
		key := fmt.Sprintf("%s|%s:%d", backend, host, port)
		if _, ok := seen[key]; !ok {
			seen[key] = discoveryTarget{backend: backend, server: server, host: host, port: port}
		}
	}

	for name := range cfg.Servers {
		if !cfg.IsServerEnabled(name) {
			continue
		}
		host, port, ok := splitHostPort(cfg.ConfiguredBackendAddr(name))
		if ok {
			add(name, host, port)
		}
	}
	for name := range cfg.Profiles {
		profile, err := cfg.ResolveProfile(name)
		if err != nil || profile.Host == nil || profile.Port == nil {
			continue
		}
		add(profile.Backend, *profile.Host, *profile.Port)
	}

	targets := make([]discoveryTarget, 0, len(seen))
	for _, t := range seen {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].backend != targets[j].backend {
			return targets[i].backend < targets[j].backend
		}
		return targets[i].addr() < targets[j].addr()
	})
	return targets
}

// probeResult is what probing one discoveryTarget found: a healthy or
// Starting server of the target's backend (instance), a 401/403 answer to
// its health check with no Starting server behind it (authErr), or neither.
type probeResult struct {
	target   discoveryTarget
	instance *RunningInstance
	authErr  error
}

// probeTargets probes every target in parallel and returns the results in
// target order.
func probeTargets(cfg *Config, targets []discoveryTarget) []probeResult {
	results := make([]probeResult, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t discoveryTarget) {
			defer wg.Done()
			results[i] = probeInstance(cfg, t)
		}(i, t)
	}
	wg.Wait()
	return results
}

// authFailedRows returns one AuthFailed row per address at which every
// probing backend's health check answered 401/403 — so an address where any
// backend found a healthy or Starting server yields none.
func authFailedRows(results []probeResult) []*RunningInstance {
	byAddr := make(map[string][]probeResult)
	var addrs []string
	for _, r := range results {
		addr := r.target.addr()
		if _, ok := byAddr[addr]; !ok {
			addrs = append(addrs, addr)
		}
		byAddr[addr] = append(byAddr[addr], r)
	}

	var rows []*RunningInstance
	for _, addr := range addrs {
		atAddr := byAddr[addr]
		if !everyProbeRefusedAuth(atAddr) {
			continue
		}
		rows = append(rows, &RunningInstance{
			Host:       atAddr[0].target.host,
			Port:       atAddr[0].target.port,
			AuthFailed: true,
		})
	}
	return rows
}

// everyProbeRefusedAuth reports whether every result carries a 401/403
// health answer.
func everyProbeRefusedAuth(results []probeResult) bool {
	for _, r := range results {
		if r.authErr == nil {
			return false
		}
	}
	return true
}

func probeInstance(cfg *Config, t discoveryTarget) probeResult {
	result := probeResult{target: t}
	b := t.server
	addr := t.addr()
	inst := &RunningInstance{
		Backend: t.backend,
		Host:    t.host,
		Port:    t.port,
	}
	if healthErr := b.HealthCheck(addr); healthErr != nil {
		// A failing health check does not always mean nothing is there: a
		// managed llama-server answers 503 during its whole model load, and
		// a loading Splash is up but has not bound its address yet. The
		// startingUp fallback surfaces that window as a Starting instance
		// (ADR-0010, ADR-0015). ListRunningModels is skipped — the server
		// cannot answer yet — so ActiveModel/ActiveProfile stay empty (with
		// no model, several profiles sharing the address would be ambiguous
		// anyway).
		if startingUp(b, addr) {
			inst.Starting = true
			result.instance = inst
			return result
		}
		// A 401/403 is a server refusing the configured api_key — a real
		// listener, not an absent one. It is recorded rather than dropped so
		// the address can surface as one AuthFailed row (authFailedRows).
		if errors.Is(healthErr, ErrAuthFailed) {
			result.authErr = healthErr
		}
		return result
	}
	if ml, ok := b.(ModelLister); ok {
		models, err := ml.ListRunningModels(addr)
		if err == nil && len(models) > 0 {
			// The name is server-reported and reaches the terminal via every
			// display site (status, menu header, pop-ups) — sanitize it here,
			// at the point it enters RunningInstance, so all of them are
			// covered once.
			inst.ActiveModel = sanitizeServerString(models[0].Name)
		}
	}
	// Profile matching runs on the whole sanitized id; only the stored,
	// displayed copy is bounded, so a long id still matches its profile.
	inst.ActiveProfile = matchProfileName(cfg, inst)
	inst.ActiveModel = boundModelID(inst.ActiveModel)
	result.instance = inst
	return result
}

// maxModelIDBytes caps a server-reported model id where it is stored in
// RunningInstance.ActiveModel (boundModelID).
const maxModelIDBytes = 512

// boundModelID truncates a sanitized server-reported model id to
// maxModelIDBytes, cutting on a rune boundary and appending "…". Whatever
// answers on a configured port is untrusted, and boundedBody alone still
// lets a single id run to hundreds of KiB, which every display site (status,
// menu header, pop-ups, MCP output) would print whole. An id within the cap
// is returned unchanged. Only the stored copy is bounded: an id handed back
// to the server (UnloadModel, the model-name match) must stay whole.
func boundModelID(id string) string {
	if len(id) <= maxModelIDBytes {
		return id
	}
	cut := maxModelIDBytes
	for cut > 0 && !utf8.RuneStart(id[cut]) {
		cut--
	}
	return id[:cut] + "…"
}

// instancesSignature condenses a discovery result into a comparable string.
// Two discovery passes produce the same signature exactly when the same
// backends are reachable at the same addresses with the same models loaded
// and the same Starting and AuthFailed states (so a Starting→healthy or an
// auth-failed→healthy transition changes the signature). The interactive menu compares signatures across refresh
// ticks to notice background changes (e.g. a model loaded via the CLI in
// another terminal) without any persisted state.
func instancesSignature(instances []*RunningInstance) string {
	parts := make([]string, 0, len(instances))
	for _, inst := range instances {
		parts = append(parts, fmt.Sprintf(
			"%s|%s|%s|%t|%t",
			inst.Backend,
			inst.Addr(),
			inst.ActiveModel,
			inst.Starting,
			inst.AuthFailed,
		))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// modelNamesMatch reports whether a profile's resolved model path refers to
// the model a server reports as loaded. Exact equality wins; otherwise the
// basenames are compared, because a server reports whatever path or alias it
// was launched with: current llama.cpp builds report the absolute --model
// path they were started with, and a server started outside the launcher may
// name the same file by a different path or by a bare alias — so the full
// resolved path rarely equals the reported name.
func modelNamesMatch(profilePath, liveModel string) bool {
	if profilePath == liveModel {
		return true
	}
	return filepath.Base(profilePath) == filepath.Base(liveModel)
}

// matchProfileName returns the name of the profile that best matches a
// running instance. A profile is a candidate when its backend and address
// equal those of the instance. Among candidates, an exact model-path match
// wins over a basename match (see modelNamesMatch). Returns the empty
// string when no profile matches or several do equally well.
func matchProfileName(cfg *Config, inst *RunningInstance) string {
	var exact, loose []string
	for name := range cfg.Profiles {
		profile, err := cfg.ResolveProfile(name)
		if err != nil {
			continue
		}
		if profile.Backend != inst.Backend {
			continue
		}
		if profile.Host == nil || profile.Port == nil {
			continue
		}
		if *profile.Host != inst.Host || *profile.Port != inst.Port {
			continue
		}
		switch {
		case inst.ActiveModel == "" || profile.ModelPath == "":
			loose = append(loose, name)
		case profile.ModelPath == inst.ActiveModel:
			exact = append(exact, name)
		case modelNamesMatch(profile.ModelPath, inst.ActiveModel):
			loose = append(loose, name)
		}
	}
	if len(exact) == 1 {
		return exact[0]
	}
	if len(exact) == 0 && len(loose) == 1 {
		return loose[0]
	}
	return ""
}

// findInstance returns the RunningInstance bound to addr, or nil.
func findInstance(instances []*RunningInstance, addr string) *RunningInstance {
	for _, inst := range instances {
		if inst.Addr() == addr {
			return inst
		}
	}
	return nil
}

// processStartTime asks `ps` for the process start time. Used by status
// display for uptime. Returns the zero time on any failure — uptime then
// renders as 0s, which is acceptable for a best-effort field.
func processStartTime(pid int) time.Time {
	if pid <= 0 {
		return time.Time{}
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return time.Time{}
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}
	}
	// ps lstart format on macOS/Linux: "Mon Jan  2 15:04:05 2006"
	t, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// fillRuntimeDetails populates PID, StartedAt and LogFile on a discovered
// instance — fields the discovery itself does not collect because they need
// shell-outs (lsof, ps) and a log-directory scan. Used by status and logs
// commands which want to display these fields.
func fillRuntimeDetails(cfg *Config, inst *RunningInstance) {
	if inst == nil {
		return
	}
	if inst.PID == 0 {
		if pid, err := findListeningPID(inst.Addr()); err == nil && pid > 0 {
			inst.PID = pid
		}
	}
	// A Starting Splash has no listening socket yet; its PID comes from the
	// process table instead (ADR-0015).
	if inst.PID == 0 && inst.Starting {
		if b, err := GetLLMServer(inst.Backend); err == nil {
			inst.PID = loadingPID(b, inst.Addr())
		}
	}
	if inst.StartedAt.IsZero() && inst.PID > 0 {
		inst.StartedAt = processStartTime(inst.PID)
	}
	if inst.LogFile == "" {
		inst.LogFile = findManagedLogFile(cfg.LogDir, inst.Backend)
	}
}

// findManagedLogFile returns the most recent launcher-managed log file for
// the given backend, or "" if none exist. Log paths are deterministic by
// naming convention (createLogPath), so we can locate them without a state
// file. Externally-started servers log wherever the user started them and
// are therefore not discoverable here.
func findManagedLogFile(logDir, backend string) string {
	if logDir == "" || backend == "" {
		return ""
	}
	pattern := filepath.Join(logDir, backend+"-*.log")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	// Log file names embed the start timestamp (YYYYMMDD-HHMMSS.mmm, or
	// YYYYMMDD-HHMMSS for older logs), so after sorting the last entry is the
	// most recent.
	sort.Strings(matches)
	return matches[len(matches)-1]
}

func splitHostPort(addr string) (string, int, bool) {
	host, portStr, ok := strings.Cut(addr, ":")
	if !ok {
		return "", 0, false
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port <= 0 {
		return "", 0, false
	}
	return host, port, true
}
