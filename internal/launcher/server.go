package launcher

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	startupGracePeriod = 500 * time.Millisecond
	sigtermTimeout     = 15 * time.Second
	stopPollInterval   = 100 * time.Millisecond
	healthPollInterval = 500 * time.Millisecond
	crashLogTailLines  = 10
	defaultTailLines   = "50"
	loopbackHost       = "127.0.0.1"
)

var ErrNotRunning = errors.New("no server running")

// IsServerAlive returns true when the backend's health check succeeds at the
// instance's address (see ADR-0001: liveness is decided by health check, not
// by PID).
func IsServerAlive(inst *RunningInstance) bool {
	if inst == nil {
		return false
	}
	b, err := GetLLMServer(inst.Backend)
	if err != nil {
		return false
	}
	return b.HealthCheck(inst.Addr()) == nil
}

// StartServer launches a managed server or connects to an external one.
func StartServer(cfg *Config, profile *ResolvedProfile) (*RunningInstance, error) {
	b, err := GetLLMServer(profile.Backend)
	if err != nil {
		return nil, err
	}

	if mb, ok := b.(ManagedLLMServer); ok {
		return startManagedServer(cfg, profile, mb)
	}
	return connectExternalServer(cfg, profile, b)
}

func startManagedServer(cfg *Config, profile *ResolvedProfile, mb ManagedLLMServer) (*RunningInstance, error) {
	binary := mb.ServerBinary(cfg)
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("server binary not found: %s", binary)
	}

	args := mb.BuildServerArgs(cfg, profile)
	logPath, err := createLogPath(cfg, profile.Backend)
	if err != nil {
		return nil, err
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("creating log file: %w", err)
	}

	cmd := exec.Command(binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if env := mb.BuildServerEnv(cfg, profile); env != nil {
		cmd.Env = append(os.Environ(), env...)
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("starting server: %w", err)
	}
	logFile.Close()

	inst := &RunningInstance{
		PID:       cmd.Process.Pid,
		Backend:   profile.Backend,
		Host:      *profile.Host,
		Port:      *profile.Port,
		StartedAt: time.Now(),
		LogFile:   logPath,
	}

	time.Sleep(startupGracePeriod)
	if !IsProcessAlive(inst.PID) {
		tail := readLastLines(logPath, crashLogTailLines)
		return nil, fmt.Errorf("server exited immediately after start\nLog tail:\n%s", tail)
	}

	return inst, nil
}

func connectExternalServer(cfg *Config, profile *ResolvedProfile, b LLMServer) (*RunningInstance, error) {
	addr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)

	launcherStarted := false
	if err := b.HealthCheck(addr); err != nil {
		if tryErr := b.TryStart(cfg, addr); tryErr == nil {
			launcherStarted = true
			if err := WaitForHealth(b, addr, 15*time.Second); err != nil {
				return nil, fmt.Errorf("%s not reachable at %s after start attempt: %w", b.DisplayName(), addr, err)
			}
		} else {
			return nil, fmt.Errorf("%s not reachable at %s — start it manually or check the endpoint in config", b.DisplayName(), addr)
		}
	}

	var pid int
	var logFile string
	if launcherStarted {
		if pt, ok := b.(PIDTracker); ok && pt.LastStartedPID() > 0 {
			pid = pt.LastStartedPID()
			logFile = pt.LastStartedLogFile()
		}
	}

	return &RunningInstance{
		PID:       pid,
		Backend:   profile.Backend,
		Host:      *profile.Host,
		Port:      *profile.Port,
		StartedAt: time.Now(),
		LogFile:   logFile,
	}, nil
}

// StopInstance stops whatever LLM-server instance is listening at addr. Stop
// is unconditional (ADR-0001): the launcher does not distinguish servers it
// started from servers that were already running. The backend's TryStop is
// called first; if the address remains reachable, the listening PID is
// discovered via lsof and signalled directly.
func StopInstance(addr string, progress ProgressFunc) (*RunningInstance, error) {
	host, port, ok := splitHostPort(addr)
	if !ok {
		return nil, fmt.Errorf("invalid address: %s", addr)
	}
	backend, err := identifyBackend(addr)
	if err != nil {
		return nil, err
	}
	pid, _ := findListeningPID(addr)
	if pid > 0 && IsProcessAlive(pid) {
		terminatePID(pid, progress)
	}
	if err := EnsureStopped(backend, addr, progress); err != nil {
		return nil, err
	}
	return &RunningInstance{
		Backend: backend,
		Host:    host,
		Port:    port,
		PID:     pid,
	}, nil
}

// identifyBackend asks each registered backend whether it owns the server
// reachable at addr. Used by stop paths that have an address but no caller-
// supplied backend name. Returns ErrNotRunning when nothing is reachable.
func identifyBackend(addr string) (string, error) {
	for name, b := range llmServers {
		if b.HealthCheck(addr) == nil {
			return name, nil
		}
	}
	return "", ErrNotRunning
}

// EnsureStopped terminates whatever process is listening at addr. It first
// asks the backend's CLI stop hook (a no-op for backends without one), and if
// the address is still reachable it locates the listening PID via lsof and
// signals it directly. Returns nil when the address is no longer healthy.
func EnsureStopped(backend, addr string, progress ProgressFunc) error {
	b, err := GetLLMServer(backend)
	if err != nil {
		return err
	}

	reportStep(progress, "Disconnecting")
	b.TryStop(addr)
	if b.HealthCheck(addr) != nil {
		return nil
	}

	pid, perr := findListeningPID(addr)
	if perr != nil || pid <= 0 {
		return fmt.Errorf("server at %s is still reachable and its PID could not be determined: %v", addr, perr)
	}

	terminatePID(pid, progress)
	if b.HealthCheck(addr) == nil {
		return fmt.Errorf("server at %s is still reachable after signalling PID %d", addr, pid)
	}
	return nil
}

func terminatePID(pid int, progress ProgressFunc) {
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}

	reportStep(progress, "Sending stop signal")
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return
	}
	// Also signal the process group (Setsid gives the child PGID=PID).
	_ = syscall.Kill(-pid, syscall.SIGTERM)

	reportStep(progress, "Waiting for shutdown")
	deadline := time.Now().Add(sigtermTimeout)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) {
			return
		}
		time.Sleep(stopPollInterval)
	}

	_ = proc.Signal(syscall.SIGKILL)
	_ = syscall.Kill(-pid, syscall.SIGKILL)

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) {
			break
		}
		time.Sleep(stopPollInterval)
	}
	time.Sleep(startupGracePeriod)
}

// findListeningPID returns the PID of the process listening at host:port,
// using lsof. Tries the host-specific filter first, then falls back to a
// port-only filter so we still find servers bound to 0.0.0.0 or another
// interface.
func findListeningPID(addr string) (int, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	attempts := [][]string{
		{"-nP", "-iTCP@" + host + ":" + port, "-sTCP:LISTEN", "-t"},
		{"-nP", "-iTCP:" + port, "-sTCP:LISTEN", "-t"},
	}
	for _, args := range attempts {
		out, err := exec.Command("lsof", args...).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if pid, err := strconv.Atoi(line); err == nil && pid > 0 {
				return pid, nil
			}
		}
	}
	return 0, fmt.Errorf("no process listening on %s (is lsof installed?)", addr)
}

// EnsureServer returns the running server instance, starting one if needed.
func EnsureServer(cfg *Config, profile *ResolvedProfile) (*RunningInstance, bool, error) {
	addr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)

	b, err := GetLLMServer(profile.Backend)
	if err != nil {
		return nil, false, err
	}

	if b.HealthCheck(addr) == nil {
		host, port, _ := splitHostPort(addr)
		return &RunningInstance{
			Backend: profile.Backend,
			Host:    host,
			Port:    port,
		}, false, nil
	}

	inst, err := StartServer(cfg, profile)
	if err != nil {
		return nil, false, err
	}

	if _, ok := b.(ManagedLLMServer); ok {
		if err := WaitForHealth(b, inst.Addr(), 15*time.Second); err != nil {
			return nil, false, err
		}
	}

	return inst, true, nil
}

// LoadProfile activates a profile at its target address. When a server is
// already reachable and serving the requested model, the call is idempotent
// (ADR-0007): if the live server's parameters match the freshly resolved
// profile, nothing happens; if they differ, a drift notice is printed and
// the caller is pointed at --restart. Drift detection is now live — the
// launcher queries the running server (llama-server /props) instead of
// reading a persisted snapshot. For backends that do not expose their
// parameters (Ollama, LM Studio), model-name match alone is enough for the
// idempotency no-op. Pass restart=true to force re-activation.
func LoadProfile(cfg *Config, profile *ResolvedProfile, restart bool, progress ProgressFunc) (*RunningInstance, bool, error) {
	targetAddr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)

	b, err := GetLLMServer(profile.Backend)
	if err != nil {
		return nil, false, err
	}

	healthy := b.HealthCheck(targetAddr) == nil

	if !restart && healthy {
		liveModel := liveLoadedModel(b, targetAddr)
		if liveModel != "" && profile.ModelPath != "" && modelNamesMatch(profile.ModelPath, liveModel) {
			drifts := liveParamDrift(b, targetAddr, profile.ProfileParams)
			if len(drifts) > 0 {
				printDriftNotice(profile.Name, targetAddr, drifts)
			}
			host, port, _ := splitHostPort(targetAddr)
			return &RunningInstance{
				Backend:       profile.Backend,
				Host:          host,
				Port:          port,
				ActiveProfile: profile.Name,
				ActiveModel:   liveModel,
			}, false, nil
		}
	}

	if cfg.ShouldAutoStopServer() {
		instances := DiscoverRunningInstances(cfg)
		for _, inst := range instances {
			if inst.Addr() == targetAddr {
				continue
			}
			reportStep(progress, fmt.Sprintf("Stopping %s", backendDisplayName(inst.Backend)))
			if _, err := StopInstance(inst.Addr(), nil); err != nil && !errors.Is(err, ErrNotRunning) {
				return nil, false, fmt.Errorf("auto-stopping %s: %w", inst.Backend, err)
			}
		}
	} else if cfg.ShouldAutoUnload() {
		instances := DiscoverRunningInstances(cfg)
		for _, inst := range instances {
			if inst.Addr() == targetAddr {
				continue
			}
			if !shouldCrossServerUnload(inst, profile.Backend) {
				continue
			}
			reportStep(progress, fmt.Sprintf("Unloading model on %s", backendDisplayName(inst.Backend)))
			if _, err := UnloadInstanceModel(inst.Addr(), nil); err != nil && !errors.Is(err, ErrNotRunning) {
				return nil, false, fmt.Errorf("auto-unloading %s: %w", inst.Backend, err)
			}
		}
	}

	if _, ok := b.(ManagedLLMServer); ok {
		return loadProfileManaged(cfg, profile, healthy, b, progress)
	}
	return loadProfileExternal(cfg, profile, b, healthy, progress)
}

// liveLoadedModel returns the name (or path) of the model currently loaded
// at addr, as reported by the backend. Empty string means "nothing loaded"
// or "backend does not expose a model list".
func liveLoadedModel(b LLMServer, addr string) string {
	ml, ok := b.(ModelLister)
	if !ok {
		return ""
	}
	models, err := ml.ListRunningModels(addr)
	if err != nil || len(models) == 0 {
		return ""
	}
	return models[0].Name
}

// liveParamDrift returns the drift list between a backend's live parameters
// and the freshly resolved profile. Backends that do not implement
// LiveParamsQuerier (Ollama, LM Studio) contribute no drift — model-name
// match is the only idempotency signal there. See ADR-0007.
func liveParamDrift(b LLMServer, addr string, fresh ProfileParams) []string {
	lp, ok := b.(LiveParamsQuerier)
	if !ok {
		return nil
	}
	live, err := lp.QueryLiveParams(addr)
	if err != nil || live == nil {
		return nil
	}
	return paramDrift(*live, fresh)
}

// paramDrift returns a human-readable list of fields that differ between
// two resolved-parameter sets. Slot-identity fields (Server, Host, Port)
// are intentionally skipped: drift in those puts the activation in a
// different address slot, which the idempotency check up the stack would
// not have matched in the first place. Each returned string has the form
// "field: old → new". Nil values on either side are treated as "unset" and
// only produce drift when the other side has a value.
func paramDrift(stored, fresh ProfileParams) []string {
	var drifts []string
	addInt := func(name string, a, b *int) {
		if a == nil && b == nil {
			return
		}
		if a == nil || b == nil || *a != *b {
			drifts = append(drifts, fmt.Sprintf("%s: %s → %s", name, formatIntPtr(a), formatIntPtr(b)))
		}
	}
	addBool := func(name string, a, b *bool) {
		if a == nil && b == nil {
			return
		}
		if a == nil || b == nil || *a != *b {
			drifts = append(drifts, fmt.Sprintf("%s: %s → %s", name, formatBoolPtr(a), formatBoolPtr(b)))
		}
	}
	addFloat := func(name string, a, b *float64) {
		if a == nil && b == nil {
			return
		}
		if a == nil || b == nil || *a != *b {
			drifts = append(drifts, fmt.Sprintf("%s: %s → %s", name, formatFloatPtr(a), formatFloatPtr(b)))
		}
	}

	addInt("gpu_layers", stored.GPULayers, fresh.GPULayers)
	addInt("threads", stored.Threads, fresh.Threads)
	addInt("threads_batch", stored.ThreadsBatch, fresh.ThreadsBatch)
	addInt("batch_size", stored.BatchSize, fresh.BatchSize)
	addInt("context_size", stored.ContextSize, fresh.ContextSize)
	addBool("flash_attn", stored.FlashAttn, fresh.FlashAttn)
	addBool("cont_batching", stored.ContBatching, fresh.ContBatching)
	addInt("parallel", stored.Parallel, fresh.Parallel)
	addBool("mlock", stored.Mlock, fresh.Mlock)
	addBool("no_mmap", stored.NoMmap, fresh.NoMmap)
	addBool("embedding", stored.Embedding, fresh.Embedding)
	addBool("jinja", stored.Jinja, fresh.Jinja)
	addFloat("temperature", stored.Temperature, fresh.Temperature)
	addFloat("repeat_penalty", stored.RepeatPenalty, fresh.RepeatPenalty)
	addInt("top_k", stored.TopK, fresh.TopK)
	addFloat("top_p", stored.TopP, fresh.TopP)
	addFloat("min_p", stored.MinP, fresh.MinP)
	return drifts
}

func formatIntPtr(p *int) string {
	if p == nil {
		return "(unset)"
	}
	return fmt.Sprintf("%d", *p)
}

func formatBoolPtr(p *bool) string {
	if p == nil {
		return "(unset)"
	}
	return fmt.Sprintf("%t", *p)
}

func formatFloatPtr(p *float64) string {
	if p == nil {
		return "(unset)"
	}
	return strconv.FormatFloat(*p, 'g', -1, 64)
}

func printDriftNotice(profileName, addr string, drifts []string) {
	fmt.Fprintf(os.Stderr, "Notice: profile %q already active at %s, but its parameters have drifted:\n", profileName, addr)
	for _, d := range drifts {
		fmt.Fprintf(os.Stderr, "  %s\n", d)
	}
	fmt.Fprintf(os.Stderr, "Run `llama-launcher load %s --restart` to apply the new parameters.\n", profileName)
}

// shouldCrossServerUnload reports whether the given instance is a candidate
// for cross-server auto_unload when activating a profile on targetBackend.
// Managed backends are skipped: an unload without a stop is not possible for
// them (see ADR-0004), so auto_unload is silently ignored on those instances.
func shouldCrossServerUnload(inst *RunningInstance, targetBackend string) bool {
	if inst == nil || inst.Backend == targetBackend || inst.ActiveModel == "" {
		return false
	}
	b, err := GetLLMServer(inst.Backend)
	if err != nil {
		return false
	}
	_, isManaged := b.(ManagedLLMServer)
	return !isManaged
}

func loadProfileManaged(cfg *Config, profile *ResolvedProfile, healthy bool, b LLMServer, progress ProgressFunc) (*RunningInstance, bool, error) {
	targetAddr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)
	if healthy {
		reportStep(progress, "Stopping current server")
		if _, err := StopInstance(targetAddr, nil); err != nil && !errors.Is(err, ErrNotRunning) {
			return nil, false, fmt.Errorf("stopping current server: %w", err)
		}
	}

	reportStep(progress, "Starting server")
	inst, err := StartServer(cfg, profile)
	if err != nil {
		return nil, false, err
	}

	reportStep(progress, "Waiting for server")
	if err := WaitForHealth(b, inst.Addr(), 30*time.Second); err != nil {
		return nil, false, err
	}

	inst.ActiveProfile = profile.Name
	inst.ActiveModel = profile.ModelPath
	inst.ResolvedParams = profile.ProfileParams

	return inst, true, nil
}

func loadProfileExternal(cfg *Config, profile *ResolvedProfile, b LLMServer, healthy bool, progress ProgressFunc) (*RunningInstance, bool, error) {
	var inst *RunningInstance
	if healthy {
		if cfg.ShouldAutoUnload() {
			if liveModel := liveLoadedModel(b, fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)); liveModel != "" {
				reportStep(progress, "Unloading current model")
				if err := b.UnloadModel(fmt.Sprintf("%s:%d", *profile.Host, *profile.Port), liveModel); err != nil {
					return nil, false, fmt.Errorf("unloading current model: %w", err)
				}
			}
		}
		inst = &RunningInstance{
			Backend: profile.Backend,
			Host:    *profile.Host,
			Port:    *profile.Port,
		}
	} else {
		reportStep(progress, "Connecting to server")
		newInst, err := connectExternalServer(cfg, profile, b)
		if err != nil {
			return nil, false, err
		}
		inst = newInst
	}

	reportStep(progress, "Loading model")
	if err := b.LoadModel(inst.Addr(), profile); err != nil {
		return nil, false, fmt.Errorf("loading model: %w", err)
	}

	inst.ActiveProfile = profile.Name
	inst.ActiveModel = profile.ModelPath
	inst.ResolvedParams = profile.ProfileParams

	return inst, true, nil
}

// WaitForHealth polls the backend's health check until it succeeds or times out.
func WaitForHealth(b LLMServer, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b.HealthCheck(addr) == nil {
			return nil
		}
		time.Sleep(healthPollInterval)
	}
	return fmt.Errorf("server at %s did not become healthy within %s", addr, timeout)
}

// UnloadInstanceModel unloads the active model for the instance at the given
// address without stopping the server.
func UnloadInstanceModel(addr string, progress ProgressFunc) (*RunningInstance, error) {
	host, port, ok := splitHostPort(addr)
	if !ok {
		return nil, fmt.Errorf("invalid address: %s", addr)
	}
	backend, err := identifyBackend(addr)
	if err != nil {
		return nil, err
	}
	b, err := GetLLMServer(backend)
	if err != nil {
		return nil, err
	}
	liveModel := liveLoadedModel(b, addr)
	if liveModel == "" {
		return &RunningInstance{Backend: backend, Host: host, Port: port}, nil
	}

	reportStep(progress, "Unloading model")
	if err := b.UnloadModel(addr, liveModel); err != nil {
		return nil, fmt.Errorf("unloading model: %w", err)
	}

	return &RunningInstance{Backend: backend, Host: host, Port: port}, nil
}

// cleanupLegacyStateFiles deletes state-*.json files left over from earlier
// versions of the launcher that persisted server state to disk. Runs once
// per process. Silent on failure — these files are best-effort cleanup, not
// load-bearing.
var legacyStateCleanupOnce sync.Once

func CleanupLegacyStateFiles() {
	legacyStateCleanupOnce.Do(func() {
		dir := DefaultConfigDir()
		os.Remove(filepath.Join(dir, "state.json"))
		matches, err := filepath.Glob(filepath.Join(dir, "state-*.json"))
		if err != nil {
			return
		}
		for _, path := range matches {
			os.Remove(path)
		}
	})
}

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func TailLog(logPath string, follow bool) error {
	args := []string{"-n", defaultTailLines}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, logPath)

	cmd := exec.Command("tail", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// createLogPath returns a fresh timestamped log path in cfg.LogDir for the
// named backend, applying the automatic log-retention cleanup first. The
// live config is threaded into the cleanup so logs of running servers are
// never removed (TDD §9.1).
func createLogPath(cfg *Config, name string) (string, error) {
	autoCleanupLogs(cfg)
	if err := os.MkdirAll(cfg.LogDir, 0o700); err != nil {
		return "", fmt.Errorf("creating log directory: %w", err)
	}
	ts := time.Now().Format(logTimestampFormat)
	return filepath.Join(cfg.LogDir, fmt.Sprintf("%s-%s.log", name, ts)), nil
}

func readLastLines(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "(could not read log)"
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
