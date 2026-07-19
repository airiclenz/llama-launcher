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
	// A server spawned by an earlier start may still be coming up at the
	// target address (llama-server answers /health with 503 while it loads
	// its model, and a large model can outlive the health-wait window).
	// Spawning a second server there would only die with "address already
	// in use", so the start is refused instead — the loading server is
	// deliberately left alone.
	addr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)
	if sp, ok := mb.(StartupProber); ok && sp.StartingUp(addr) {
		return nil, stillStartingUpErr(cfg, mb, addr)
	}

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

	// Reap the child so a fast-exiting server never lingers as a zombie. A
	// zombie still satisfies kill(pid, 0), so without reaping the liveness
	// check would report an already-dead child as alive and the start-crash
	// detection below would never fire. cmd.Wait runs in its own goroutine and
	// reports the exit through waitResult; if the launcher exits before the
	// child does, the detached (Setsid) child is reparented to init and reaped
	// there instead.
	waitResult := make(chan error, 1)
	go func() { waitResult <- cmd.Wait() }()

	inst := &RunningInstance{
		PID:       cmd.Process.Pid,
		Backend:   profile.Backend,
		Host:      *profile.Host,
		Port:      *profile.Port,
		StartedAt: time.Now(),
		LogFile:   logPath,
	}

	// If the child exits within the startup grace period the start failed
	// (port conflict, bad args, ...): surface the log tail instead of reporting
	// a server that has already died. Otherwise the child is still running and
	// the wait goroutine stays parked to reap it whenever it does exit.
	select {
	case waitErr := <-waitResult:
		tail := readLastLines(logPath, crashLogTailLines)
		return nil, fmt.Errorf("server exited immediately after start (%v)\nLog tail:\n%s", waitErr, tail)
	case <-time.After(startupGracePeriod):
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
// started from servers that were already running. Both stop mechanisms run
// exactly once, in the documented order (TDD §6.5): the listening PID is
// discovered via lsof and signalled (SIGTERM → SIGKILL → port-release wait),
// then the backend's native stop hook runs best-effort. Returns ErrNotRunning
// when no known backend answers at addr.
func StopInstance(addr string, progress ProgressFunc) (*RunningInstance, error) {
	host, port, ok := splitHostPort(addr)
	if !ok {
		return nil, fmt.Errorf("invalid address: %s", addr)
	}
	backend, err := identifyBackend(addr)
	if err != nil {
		return nil, err
	}
	pid, err := stopServerAt(backend, addr, progress)
	if err != nil {
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

// stopServerAt runs both stop mechanisms against addr exactly once, in the
// documented order (TDD §6.5): signal the listening PID first (with the
// SIGTERM → SIGKILL → port-release escalation), then invoke the backend's
// native stop hook. The hook is best-effort — its error surfaces only when
// the address is still serving afterwards. Returns the signalled PID (0 when
// none was found) and an error when the server survived both mechanisms.
func stopServerAt(backend, addr string, progress ProgressFunc) (int, error) {
	b, err := GetLLMServer(backend)
	if err != nil {
		return 0, err
	}

	pid, pidErr := findListeningPID(addr)
	if pid > 0 && IsProcessAlive(pid) {
		terminatePID(pid, progress)
	}

	reportStep(progress, "Disconnecting")
	stopErr := b.TryStop(addr)

	if b.HealthCheck(addr) != nil {
		return pid, nil
	}
	if stopErr != nil {
		return pid, fmt.Errorf("server at %s is still reachable; %s stop hook failed: %v", addr, b.DisplayName(), stopErr)
	}
	if pid <= 0 {
		return pid, fmt.Errorf("server at %s is still reachable and its PID could not be determined: %v", addr, pidErr)
	}
	return pid, fmt.Errorf("server at %s is still reachable after signalling PID %d", addr, pid)
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
			return nil, false, startupTimeoutErr(err, inst)
		}
	}

	return inst, true, nil
}

// activationOps is the seam behind LoadProfile (ADR-0009): the process,
// health, and probe operations the activation orchestration drives. The
// production adapter (realOps) executes them against the live system —
// exec/lsof/signals for processes, each backend's HTTP API for probes —
// while tests substitute an in-memory fake, so the ADR-0004/0007 decision
// logic is exercised without forking a process or opening a socket.
type activationOps interface {
	// healthy reports whether b's own server answers at addr
	// (backend-discriminating health check).
	healthy(b LLMServer, addr string) bool
	// loadedModel returns the model currently loaded at addr; empty when
	// nothing is loaded or b cannot list models.
	loadedModel(b LLMServer, addr string) string
	// liveDrift diffs the live server parameters at addr against the
	// freshly resolved profile params (ADR-0007 drift notice).
	liveDrift(b LLMServer, addr string, fresh ProfileParams) []string
	// discover returns the running instances derivable from cfg.
	discover(cfg *Config) []*RunningInstance
	// start launches a managed server or connects an external one.
	start(cfg *Config, profile *ResolvedProfile) (*RunningInstance, error)
	// waitHealthy polls b's health check at addr until success or timeout.
	waitHealthy(b LLMServer, addr string, timeout time.Duration) error
	// stop stops whatever instance is listening at addr (ADR-0001).
	stop(addr string, progress ProgressFunc) (*RunningInstance, error)
	// unloadInstance unloads the active model of the instance at addr
	// without stopping its server.
	unloadInstance(addr string, progress ProgressFunc) (*RunningInstance, error)
	// loadModel loads the profile's model on b at addr.
	loadModel(b LLMServer, addr string, profile *ResolvedProfile) error
	// unloadModel unloads modelID on b at addr.
	unloadModel(b LLMServer, addr, modelID string) error
}

// realOps is the production activationOps adapter. Each method delegates to
// the package's live implementation; no orchestration logic lives here.
type realOps struct{}

func (realOps) healthy(b LLMServer, addr string) bool { return b.HealthCheck(addr) == nil }

func (realOps) loadedModel(b LLMServer, addr string) string { return liveLoadedModel(b, addr) }

func (realOps) liveDrift(b LLMServer, addr string, fresh ProfileParams) []string {
	return liveParamDrift(b, addr, fresh)
}

func (realOps) discover(cfg *Config) []*RunningInstance { return DiscoverRunningInstances(cfg) }

func (realOps) start(cfg *Config, profile *ResolvedProfile) (*RunningInstance, error) {
	return StartServer(cfg, profile)
}

func (realOps) waitHealthy(b LLMServer, addr string, timeout time.Duration) error {
	return WaitForHealth(b, addr, timeout)
}

func (realOps) stop(addr string, progress ProgressFunc) (*RunningInstance, error) {
	return StopInstance(addr, progress)
}

func (realOps) unloadInstance(addr string, progress ProgressFunc) (*RunningInstance, error) {
	return UnloadInstanceModel(addr, progress)
}

func (realOps) loadModel(b LLMServer, addr string, profile *ResolvedProfile) error {
	return b.LoadModel(addr, profile)
}

func (realOps) unloadModel(b LLMServer, addr, modelID string) error {
	return b.UnloadModel(addr, modelID)
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
	return loadProfile(realOps{}, cfg, profile, restart, progress)
}

// loadProfile is the activation orchestration behind LoadProfile. It drives
// every process/health/probe effect through ops (ADR-0009) and carries the
// single targetAddr derived from the resolved profile.
func loadProfile(ops activationOps, cfg *Config, profile *ResolvedProfile, restart bool, progress ProgressFunc) (*RunningInstance, bool, error) {
	targetAddr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)

	b, err := GetLLMServer(profile.Backend)
	if err != nil {
		return nil, false, err
	}

	healthy := ops.healthy(b, targetAddr)

	if !restart && healthy {
		liveModel := ops.loadedModel(b, targetAddr)
		if liveModel != "" && profile.ModelPath != "" && modelNamesMatch(profile.ModelPath, liveModel) {
			drifts := ops.liveDrift(b, targetAddr, profile.ProfileParams)
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
		for _, inst := range ops.discover(cfg) {
			// Skip only a same-backend instance at the target address —
			// that is the instance being (re)activated. A *different*
			// backend occupying the shared target address (backends may
			// share one host:port) is a foreign occupant that must be
			// stopped, or the new server cannot bind (ADR-0004, ADR-0006).
			if inst.Addr() == targetAddr && inst.Backend == profile.Backend {
				continue
			}
			reportStep(progress, fmt.Sprintf("Stopping %s", backendDisplayName(inst.Backend)))
			if _, err := ops.stop(inst.Addr(), nil); err != nil && !errors.Is(err, ErrNotRunning) {
				return nil, false, fmt.Errorf("auto-stopping %s: %w", inst.Backend, err)
			}
		}
	} else if cfg.ShouldAutoUnload() {
		for _, inst := range ops.discover(cfg) {
			// Same skip rule as above: a same-backend instance at the
			// target address is the one being (re)activated; a foreign
			// backend there is a regular cross-server unload candidate.
			if inst.Addr() == targetAddr && inst.Backend == profile.Backend {
				continue
			}
			if !shouldCrossServerUnload(inst, profile.Backend) {
				continue
			}
			reportStep(progress, fmt.Sprintf("Unloading model on %s", backendDisplayName(inst.Backend)))
			if _, err := ops.unloadInstance(inst.Addr(), nil); err != nil && !errors.Is(err, ErrNotRunning) {
				return nil, false, fmt.Errorf("auto-unloading %s: %w", inst.Backend, err)
			}
		}
	}

	if _, ok := b.(ManagedLLMServer); ok {
		return loadProfileManaged(ops, cfg, profile, targetAddr, healthy, b, progress)
	}
	return loadProfileExternal(ops, cfg, profile, targetAddr, healthy, b, progress)
}

// liveLoadedModel returns the name (or path) of the model currently loaded
// at addr, as reported by the backend. Empty string means "nothing loaded"
// or "backend does not expose a model list". The name is server-reported
// and reaches the terminal (progress lines, RunningInstance.ActiveModel),
// so control characters are stripped here (sanitizeServerString).
func liveLoadedModel(b LLMServer, addr string) string {
	ml, ok := b.(ModelLister)
	if !ok {
		return ""
	}
	models, err := ml.ListRunningModels(addr)
	if err != nil || len(models) == 0 {
		return ""
	}
	return sanitizeServerString(models[0].Name)
}

// liveParamDrift returns the drift list between a backend's live parameters
// and the freshly resolved profile. Only fields the live probe actually
// reports are compared: a nil field on the live side means "not reported by
// the server", never "drifted to unset", so unreported fields cannot
// manufacture drift — a drift notice must mean real drift (ADR-0007).
// Backends that do not implement LiveParamsQuerier (Ollama, LM Studio)
// contribute no drift — model-name match is the only idempotency signal
// there.
func liveParamDrift(b LLMServer, addr string, fresh ProfileParams) []string {
	lp, ok := b.(LiveParamsQuerier)
	if !ok {
		return nil
	}
	live, err := lp.QueryLiveParams(addr)
	if err != nil || live == nil {
		return nil
	}
	return paramDrift(*live, maskUnreported(*live, fresh))
}

// maskUnreported returns fresh with every field nilled out that the live
// probe left nil, so paramDrift compares only the fields the server actually
// reported. Slot-identity fields (Server, Host, Port) are already excluded
// by paramDrift and need no masking.
func maskUnreported(live, fresh ProfileParams) ProfileParams {
	masked := fresh
	if live.GPULayers == nil {
		masked.GPULayers = nil
	}
	if live.Threads == nil {
		masked.Threads = nil
	}
	if live.ThreadsBatch == nil {
		masked.ThreadsBatch = nil
	}
	if live.BatchSize == nil {
		masked.BatchSize = nil
	}
	if live.ContextSize == nil {
		masked.ContextSize = nil
	}
	if live.FlashAttn == nil {
		masked.FlashAttn = nil
	}
	if live.ContBatching == nil {
		masked.ContBatching = nil
	}
	if live.Parallel == nil {
		masked.Parallel = nil
	}
	if live.Mlock == nil {
		masked.Mlock = nil
	}
	if live.NoMmap == nil {
		masked.NoMmap = nil
	}
	if live.Embedding == nil {
		masked.Embedding = nil
	}
	if live.Jinja == nil {
		masked.Jinja = nil
	}
	if live.Temperature == nil {
		masked.Temperature = nil
	}
	if live.RepeatPenalty == nil {
		masked.RepeatPenalty = nil
	}
	if live.TopK == nil {
		masked.TopK = nil
	}
	if live.TopP == nil {
		masked.TopP = nil
	}
	if live.MinP == nil {
		masked.MinP = nil
	}
	return masked
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

func loadProfileManaged(ops activationOps, cfg *Config, profile *ResolvedProfile, targetAddr string, healthy bool, b LLMServer, progress ProgressFunc) (*RunningInstance, bool, error) {
	if healthy {
		reportStep(progress, "Stopping current server")
		if _, err := ops.stop(targetAddr, nil); err != nil && !errors.Is(err, ErrNotRunning) {
			return nil, false, fmt.Errorf("stopping current server: %w", err)
		}
	}

	reportStep(progress, "Starting server")
	inst, err := ops.start(cfg, profile)
	if err != nil {
		return nil, false, err
	}

	reportStep(progress, "Waiting for server")
	if err := ops.waitHealthy(b, inst.Addr(), 30*time.Second); err != nil {
		return nil, false, startupTimeoutErr(err, inst)
	}

	inst.ActiveProfile = profile.Name
	inst.ActiveModel = profile.ModelPath

	return inst, true, nil
}

func loadProfileExternal(ops activationOps, cfg *Config, profile *ResolvedProfile, targetAddr string, healthy bool, b LLMServer, progress ProgressFunc) (*RunningInstance, bool, error) {
	var inst *RunningInstance
	if healthy {
		if cfg.ShouldAutoUnload() {
			if liveModel := ops.loadedModel(b, targetAddr); liveModel != "" {
				reportStep(progress, "Unloading current model")
				if err := ops.unloadModel(b, targetAddr, liveModel); err != nil {
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
		newInst, err := ops.start(cfg, profile)
		if err != nil {
			return nil, false, err
		}
		inst = newInst
	}

	reportStep(progress, "Loading model")
	if err := ops.loadModel(b, inst.Addr(), profile); err != nil {
		return nil, false, fmt.Errorf("loading model: %w", err)
	}

	inst.ActiveProfile = profile.Name
	inst.ActiveModel = profile.ModelPath

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

// startupTimeoutErr decorates a health-wait timeout that follows a
// managed start. The just-spawned server is deliberately left running —
// killing it would throw away a legitimately slow model load (a 30–70 GB
// GGUF on a cold disk can exceed the wait window) — so the error names
// its PID and log path instead of orphaning the process silently. A
// retry while it is still loading is refused (see stillStartingUpErr); a
// retry after it turns healthy is the idempotent no-op (ADR-0007).
func startupTimeoutErr(err error, inst *RunningInstance) error {
	return fmt.Errorf("%w\nThe server may still be loading its model — it was left running (PID %d)\nLog: %s\nWatch it with `llama-launcher logs %s` and retry once it is healthy, or stop it with `kill %d`",
		err, inst.PID, inst.LogFile, inst.Backend, inst.PID)
}

// stillStartingUpErr builds the refusal for a managed start onto an
// address where an earlier-spawned server is still coming up (the
// startup probe answers "still loading"). PID and log-path details are
// best-effort — the loading server fails its backend's health check, so
// discovery cannot see it and the PID comes straight from lsof.
func stillStartingUpErr(cfg *Config, b LLMServer, addr string) error {
	pid, err := findListeningPID(addr)
	if err != nil || pid <= 0 {
		pid = 0
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "a %s server at %s is still starting up", b.DisplayName(), addr)
	if pid > 0 {
		fmt.Fprintf(&sb, " (PID %d)", pid)
	}
	sb.WriteString(" — refusing to start a second one")
	if logFile := findManagedLogFile(cfg.LogDir, b.Name()); logFile != "" {
		fmt.Fprintf(&sb, "\nLog: %s", logFile)
	}
	fmt.Fprintf(&sb, "\nWatch it with `llama-launcher logs %s` and retry once it is healthy", b.Name())
	if pid > 0 {
		fmt.Fprintf(&sb, ", or stop it with `kill %d`", pid)
	}
	return errors.New(sb.String())
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

// StopResult reports what a Stop or Unload call did. The entry points
// collect the progress steps their mechanics report into the result
// instead of streaming them through a live UI callback, so the CLI and
// menu each render the same outcome after the fact, in their own style.
type StopResult struct {
	// Instance is the instance the operation acted on, as reported by the
	// stop/unload mechanics (Backend, Host, Port, and the signalled PID
	// when one was found). Nil when the operation failed.
	Instance *RunningInstance
	// ServerStopped distinguishes the two outcomes on success: true when
	// the server process itself was stopped (Stop always; Unload on a
	// managed backend), false when only the model was unloaded and the
	// server keeps running.
	ServerStopped bool
	// Steps lists the orchestration steps taken, in order.
	Steps []string
}

// stepRecorder accumulates progress steps for a StopResult. Its record
// method is a ProgressFunc, so the recorded mechanics are unchanged —
// they report through the same callback type as before, it just no
// longer reaches the UI directly.
type stepRecorder struct {
	steps []string
}

func (r *stepRecorder) record(step string) {
	r.steps = append(r.steps, step)
}

// Stop stops whatever LLM-server instance is listening at addr (ADR-0001)
// and returns the steps taken. The result is non-nil even on error,
// carrying the steps completed before the failure.
func Stop(addr string) (*StopResult, error) {
	return stopServer(realOps{}, addr)
}

// stopServer is the orchestration behind Stop, driven through the
// activation seam (ADR-0009) so tests run it against a fake.
func stopServer(ops activationOps, addr string) (*StopResult, error) {
	rec := &stepRecorder{}
	inst, err := ops.stop(addr, rec.record)
	return &StopResult{Instance: inst, ServerStopped: true, Steps: rec.steps}, err
}

// Unload unloads the model of the backend instance at addr and returns
// the steps taken. This is the single home of the "unload on a managed
// backend means stop the server" rule (ADR-0003, ADR-0004): a managed
// backend bakes the model into its process arguments, so its unload is a
// stop; an external backend gets an API unload and keeps running. The
// result is non-nil even on error, carrying the steps completed before
// the failure.
func Unload(backend, addr string) (*StopResult, error) {
	return unloadServerModel(realOps{}, backend, addr)
}

// unloadServerModel is the orchestration behind Unload, driven through
// the activation seam (ADR-0009) so tests run it against a fake.
func unloadServerModel(ops activationOps, backend, addr string) (*StopResult, error) {
	b, err := GetLLMServer(backend)
	if err != nil {
		return &StopResult{}, err
	}

	rec := &stepRecorder{}
	if _, isManaged := b.(ManagedLLMServer); isManaged {
		inst, stopErr := ops.stop(addr, rec.record)
		return &StopResult{Instance: inst, ServerStopped: true, Steps: rec.steps}, stopErr
	}

	inst, unloadErr := ops.unloadInstance(addr, rec.record)
	return &StopResult{Instance: inst, Steps: rec.steps}, unloadErr
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

func createLogPath(cfg *Config, name string) (string, error) {
	autoCleanupLogs(cfg)
	if err := os.MkdirAll(cfg.LogDir, 0o700); err != nil {
		return "", fmt.Errorf("creating log directory: %w", err)
	}
	ts := time.Now().Format("20060102-150405")
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
