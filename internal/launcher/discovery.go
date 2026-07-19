package launcher

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	// Starting marks an instance whose process is up and address bound but
	// whose health check does not pass yet — llama-server answers /health
	// with 503 for the whole model load. See ADR-0010.
	Starting bool
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
// Probes run in parallel. Backends that fail to register or fail their
// health check are silently omitted.
func DiscoverRunningInstances(cfg *Config) []*RunningInstance {
	type target struct {
		backend string
		host    string
		port    int
	}

	seen := make(map[string]target)
	add := func(backend, host string, port int) {
		t := target{backend: backend, host: host, port: port}
		key := fmt.Sprintf("%s|%s:%d", backend, host, port)
		if _, ok := seen[key]; !ok {
			seen[key] = t
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

	targets := make([]target, 0, len(seen))
	for _, t := range seen {
		targets = append(targets, t)
	}

	type result struct {
		instance *RunningInstance
	}
	ch := make(chan result, len(targets))
	for _, t := range targets {
		go func(t target) {
			ch <- result{instance: probeInstance(cfg, t.backend, t.host, t.port)}
		}(t)
	}

	var instances []*RunningInstance
	for range targets {
		r := <-ch
		if r.instance != nil {
			instances = append(instances, r.instance)
		}
	}

	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Backend != instances[j].Backend {
			return instances[i].Backend < instances[j].Backend
		}
		return instances[i].Addr() < instances[j].Addr()
	})
	return instances
}

func probeInstance(cfg *Config, backend, host string, port int) *RunningInstance {
	b, err := GetLLMServer(backend)
	if err != nil {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	inst := &RunningInstance{
		Backend: backend,
		Host:    host,
		Port:    port,
	}
	if b.HealthCheck(addr) != nil {
		// A failing health check does not always mean nothing is there: a
		// managed llama-server answers 503 during its whole model load. The
		// StartupProber fallback surfaces that window as a Starting instance
		// (ADR-0010). ListRunningModels is skipped — the server cannot
		// answer yet — so ActiveModel/ActiveProfile stay empty (with no
		// model, several profiles sharing the address would be ambiguous
		// anyway).
		if sp, ok := b.(StartupProber); ok && sp.StartingUp(addr) {
			inst.Starting = true
			return inst
		}
		return nil
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
	inst.ActiveProfile = matchProfileName(cfg, inst)
	return inst
}

// instancesSignature condenses a discovery result into a comparable string.
// Two discovery passes produce the same signature exactly when the same
// backends are reachable at the same addresses with the same models loaded
// and the same Starting state (so a Starting→healthy transition changes
// the signature). The interactive menu compares signatures across refresh
// ticks to notice background changes (e.g. a model loaded via the CLI in
// another terminal) without any persisted state.
func instancesSignature(instances []*RunningInstance) string {
	parts := make([]string, 0, len(instances))
	for _, inst := range instances {
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%t", inst.Backend, inst.Addr(), inst.ActiveModel, inst.Starting))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// modelNamesMatch reports whether a profile's resolved model path refers to
// the model a server reports as loaded. Exact equality wins; otherwise the
// basenames are compared, because servers started outside the launcher
// report whatever path or alias they were launched with — llama-server
// defaults the id to the model file's basename — so the full resolved path
// rarely equals the reported name.
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
	// Glob returns lexicographic order — log file names embed the start
	// timestamp (YYYYMMDD-HHMMSS), so the last entry is the most recent.
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
