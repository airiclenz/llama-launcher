package launcher

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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

// ErrStartupTimeout reports that a managed server this launcher started did
// not report healthy inside the activation wait window. It is not a failed
// start: the server is deliberately left running (see startupTimeoutErr), so
// a slow model load can still finish and turn the address healthy later. The
// decorated timeout errors wrap it; test for it with errors.Is.
var ErrStartupTimeout = errors.New("server startup timed out")

// ErrUnsupported reports an operation the platform this binary was built for
// cannot perform. Windows has neither a session to detach a spawned server
// into nor a process group to signal, so the process-control paths return it
// instead of acting, while everything the launcher drives over HTTP keeps
// working there (ADR-0012). Errors from those paths wrap it; test for it with
// errors.Is.
var ErrUnsupported = errors.New("operation not supported on this platform")

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
	// Refuse before forking on a platform without unix process control: a
	// child spawned there would have no session of its own and no process
	// group to stop it by, so every stop path would leave it running
	// (ADR-0012).
	if err := requireProcessControl(); err != nil {
		return nil, err
	}

	// A server spawned by an earlier start may still be coming up at the
	// target address (llama-server answers /health with 503 while it loads
	// its model, and a large model can outlive the health-wait window; a
	// loading Splash has not bound the address yet and is found through the
	// process table instead, ADR-0015). Spawning a second server there would
	// only collide with the first, so the start is refused instead — the
	// loading server is deliberately left alone.
	addr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)
	if startingUp(mb, addr) {
		return nil, stillStartingUpErr(cfg, mb, addr)
	}

	// Whatever else still listens on the port is a foreign occupant — the
	// caller stops a healthy or starting server of this backend before
	// reaching here, and the guard above catches the race that leaves one.
	// A foreign occupant never steps aside, so the spawn could only end in a
	// bind failure; refusing here names the port and who holds it instead.
	if occupants := portOccupants(addr); len(occupants) > 0 {
		return nil, portConflictErr(mb, addr, occupants)
	}

	binary := mb.ServerBinary(cfg)
	if _, err := exec.LookPath(binary); err != nil {
		if hinter, ok := mb.(binaryInstallHinter); ok {
			return nil, fmt.Errorf("server binary not found: %s — %s", binary, hinter.BinaryInstallHint())
		}
		return nil, fmt.Errorf("server binary not found: %s", binary)
	}

	args := mb.BuildServerArgs(cfg, profile)
	logFile, err := createLogPath(cfg, profile.Backend)
	if err != nil {
		return nil, err
	}
	logPath := logFile.Name()

	cmd := exec.Command(binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedSysProcAttr()

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
		tail := readLastLines(logPath, crashLogTailLines, []string{cfg.APIKeyFor(profile.Backend)})
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
// then the backend's native stop hook runs best-effort. A server that
// answers only with 401/403 (identifyBackend's third pass) is stopped by
// the signal alone — no native hook runs against a server that refuses the
// api_key — and the returned instance carries Backend "", since no backend
// identified it. Returns ErrNotRunning when no known backend answers at addr.
func StopInstance(addr string, progress ProgressFunc) (*RunningInstance, error) {
	host, port, ok := splitHostPort(addr)
	if !ok {
		return nil, fmt.Errorf("invalid address: %s", addr)
	}
	backend, err := identifyBackend(addr)
	authFailed := errors.Is(err, ErrAuthFailed)
	if err != nil && !authFailed {
		return nil, err
	}
	pid, err := stopServerAt(backend, addr, !authFailed, progress)
	if err != nil {
		return nil, err
	}
	if authFailed {
		// The probing backend's name only drove the stop verification; it
		// does not identify the server, so it is not reported.
		backend = ""
	}
	return &RunningInstance{
		Backend: backend,
		Host:    host,
		Port:    port,
		PID:     pid,
	}, nil
}

// identifyBackend asks each registered backend whether it owns the server
// reachable at addr. Used by stop and unload paths that have an address but
// no caller-supplied backend name. Three passes, each in sorted-name order
// so the answer is deterministic (map iteration is random): first the
// backends' discriminating health checks, then the startup probes of
// backends that implement StartupProber or LoadingProcessFinder — a
// Starting instance fails its health check for the whole model load but
// must still be identifiable so it can be stopped (ADR-0010, ADR-0015) —
// and last a health check that answered 401/403: a server refusing the
// configured api_key, which only an explicit stop may act on. That third
// pass returns the first such backend's name together with an error
// wrapping ErrAuthFailed (the authFailedErr message); a caller that acts on
// an identified server treats it as a refusal, while stop proceeds with the
// name. Returns ErrNotRunning when no pass identifies anything.
func identifyBackend(addr string) (string, error) {
	names := make([]string, 0, len(llmServers))
	for name := range llmServers {
		names = append(names, name)
	}
	sort.Strings(names)

	authName, authErr := "", error(nil)
	for _, name := range names {
		healthErr := llmServers[name].HealthCheck(addr)
		if healthErr == nil {
			return name, nil
		}
		if authErr == nil && errors.Is(healthErr, ErrAuthFailed) {
			authName, authErr = name, healthErr
		}
	}
	for _, name := range names {
		if startingUp(llmServers[name], addr) {
			return name, nil
		}
	}
	if authErr != nil {
		return authName, fmt.Errorf("server at %s: %w", addr, authErr)
	}
	return "", ErrNotRunning
}

// startingUp reports whether a still-starting (Starting, ADR-0010) server
// of b's is at addr: either one that answers there but is still loading
// (StartupProber), or one whose process is up but has not bound the address
// yet (LoadingProcessFinder, ADR-0015). Backends implementing neither never
// report Starting.
func startingUp(b LLMServer, addr string) bool {
	if sp, ok := b.(StartupProber); ok && sp.StartingUp(addr) {
		return true
	}
	return loadingPID(b, addr) > 0
}

// loadingPID returns the PID of a server of b's that is loading its model
// for addr without having bound it yet, or 0 when there is none. It asks
// only backends implementing LoadingProcessFinder, and only trusts the
// answer while nothing listens at addr: once a process holds the address,
// the HTTP probes and lsof identify it, and a listener that fails them is
// never attributed to b by its command line (ADR-0015).
func loadingPID(b LLMServer, addr string) int {
	finder, ok := b.(LoadingProcessFinder)
	if !ok {
		return 0
	}
	pid := finder.LoadingPID(addr)
	if pid <= 0 || addrHasListener(addr) {
		return 0
	}
	return pid
}

// addrHasListener reports whether any process listens on addr's port, via
// the same lsof lookup the stop path uses. A variable so tests can pin it.
var addrHasListener = func(addr string) bool {
	_, err := findListeningPIDs(addr)
	return err == nil
}

// processEntry is one row of the process table: a PID, its process group,
// and its command line — the true argv for a session leader whose argv the
// kernel shows (withTrueArgv), else ps's command split on whitespace.
type processEntry struct {
	PID  int
	PGID int
	Args []string
}

// processTable reads the machine's process table (listProcesses in the
// per-platform process files). A variable so tests can substitute a fixed
// table instead of the live one.
var processTable = listProcesses

// parseProcessTable parses `ps -o pid=,pgid=,command=` output, skipping rows
// whose PID or PGID does not parse. Only the PID and PGID are read by
// position; ps joins the arguments with spaces, so the command it prints
// is split on whitespace only as a fallback, and an argument containing
// spaces arrives as several fields until withTrueArgv replaces the row's
// Args with the true argv.
func parseProcessTable(out string) []processEntry {
	var entries []processEntry
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		pgid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		entries = append(entries, processEntry{PID: pid, PGID: pgid, Args: fields[2:]})
	}
	return entries
}

// stopServerAt runs both stop mechanisms against addr exactly once, in the
// documented order (TDD §6.5): signal the listening PID first (with the
// SIGTERM → SIGKILL → port-release escalation), then invoke the backend's
// native stop hook. The hook is best-effort — its error surfaces only when
// the address is still serving afterwards. A loading server that has not
// bound addr yet has no listening PID; its PID comes from the backend's
// LoadingProcessFinder instead (ADR-0015). With nativeHook false — a server
// that refuses the api_key, which no backend identified — only the listening
// PID is signalled: no loading-process lookup, no native hook. Stopped means
// not healthy, not refusing auth *and* not still starting up: a survived
// Starting server also fails the health check (llama-server answers /health
// with 503 for the whole model load), and a survived auth-refusing one
// answers 401/403, so health alone would report either as stopped
// (ADR-0010). Returns the signalled PID (0 when none was found) and an error
// when the server survived the mechanisms run.
func stopServerAt(backend, addr string, nativeHook bool, progress ProgressFunc) (int, error) {
	b, err := GetLLMServer(backend)
	if err != nil {
		return 0, err
	}

	pid, pidErr := findListeningPID(addr)
	if pid <= 0 && nativeHook {
		if loading := loadingPID(b, addr); loading > 0 {
			pid, pidErr = loading, nil
		}
	}
	if pid > 0 && IsProcessAlive(pid) {
		// Recorded as the PID is resolved: an identity that cannot be read
		// means the process is already gone, so nothing is signalled.
		if identity, err := processIdentity(pid); err == nil {
			terminatePID(pid, identity, progress)
		}
	}

	var stopErr error
	if nativeHook {
		reportStep(progress, "Disconnecting")
		stopErr = b.TryStop(addr)
	}

	healthErr := b.HealthCheck(addr)
	if healthErr != nil && !errors.Is(healthErr, ErrAuthFailed) && !startingUp(b, addr) {
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

// terminatePID runs the SIGTERM → SIGKILL → port-release escalation (TDD
// §6.5) against pid and its process group, but only while pid still names the
// process whose start time identity records: the check runs before SIGTERM,
// on every poll, and again before SIGKILL. A mismatch or an unreadable
// identity means the process exited and the kernel may have handed its PID to
// an unrelated one, so no further signal is sent and the PID counts as gone.
// The window between a check and the signal after it is a syscall wide.
func terminatePID(pid int, identity int64, progress ProgressFunc) {
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}

	if !sameProcess(pid, identity) {
		return
	}
	reportStep(progress, "Sending stop signal")
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return
	}
	// Also signal the process group (Setsid gives the child PGID=PID).
	_ = signalGroup(pid, syscall.SIGTERM)

	reportStep(progress, "Waiting for shutdown")
	deadline := time.Now().Add(sigtermTimeout)
	for time.Now().Before(deadline) {
		if !sameProcess(pid, identity) {
			return
		}
		time.Sleep(stopPollInterval)
	}

	if !sameProcess(pid, identity) {
		return
	}
	_ = proc.Signal(syscall.SIGKILL)
	_ = signalGroup(pid, syscall.SIGKILL)

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !sameProcess(pid, identity) {
			break
		}
		time.Sleep(stopPollInterval)
	}
	time.Sleep(startupGracePeriod)
}

// findListeningPID returns the PID of the first process listening at
// host:port. See findListeningPIDs for how the lookup is made.
func findListeningPID(addr string) (int, error) {
	pids, err := findListeningPIDs(addr)
	if err != nil {
		return 0, err
	}
	return pids[0], nil
}

// findListeningPIDs returns the PIDs of every process listening at
// host:port, using lsof. Tries the host-specific filter first, then falls
// back to a port-only filter so we still find servers bound to 0.0.0.0 or
// another interface. The result is never empty when the error is nil.
func findListeningPIDs(addr string) ([]int, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
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
		if pids := parseListeningPIDs(string(out)); len(pids) > 0 {
			return pids, nil
		}
	}
	return nil, fmt.Errorf("no process listening on %s (is lsof installed?)", addr)
}

// parseListeningPIDs extracts PIDs from `lsof -t` output, discarding
// unparseable lines and repeats — a process listening on several interfaces
// reports one line per socket — while keeping lsof's order.
func parseListeningPIDs(out string) []int {
	var pids []int
	seen := make(map[int]bool)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil || pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		pids = append(pids, pid)
	}
	return pids
}

// portOccupant is a process found listening on a port the launcher is about
// to bind, carrying its executable name when ps could report one.
type portOccupant struct {
	PID  int
	Name string
}

// portOccupants reports every process listening on addr's port, annotated
// with its executable name. An empty result means the port is free — or that
// lsof could not answer, which the caller treats the same way, since a check
// that cannot run must not block a start that would have worked.
func portOccupants(addr string) []portOccupant {
	pids, err := findListeningPIDs(addr)
	if err != nil {
		return nil
	}
	occupants := make([]portOccupant, 0, len(pids))
	for _, pid := range pids {
		occupants = append(occupants, portOccupant{PID: pid, Name: processName(pid)})
	}
	return occupants
}

// processName reports a PID's executable name via ps, or "" when ps cannot
// say. macOS reports the full path for a bundled executable, so only the
// base name is kept — "Code Helper (Plugin)", not its whole bundle path.
func processName(pid int) string {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return ""
	}
	return filepath.Base(name)
}

// portConflictErr builds the refusal for starting a managed server on a port
// another process already holds. By this point the caller has established
// that the address carries no healthy instance of this backend, and the
// StartingUp guard has ruled out one of ours still coming up — so whatever is
// listening will not step aside, and the server would fork only to die on
// "couldn't bind HTTP server socket". That death surfaces as a log tail
// naming neither the port's occupant nor, for a foreign listener on loopback,
// the reason discovery reported nothing running: naming both here is what
// makes the failure diagnosable without reaching for lsof.
func portConflictErr(b LLMServer, addr string, occupants []portOccupant) error {
	port := addr
	if _, p, err := net.SplitHostPort(addr); err == nil {
		port = p
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "port %s is already in use — %s cannot bind %s", port, b.DisplayName(), addr)
	sb.WriteString("\nListening now:")
	for _, o := range occupants {
		fmt.Fprintf(&sb, "\n  PID %d", o.PID)
		if o.Name != "" {
			fmt.Fprintf(&sb, " (%s)", o.Name)
		}
	}
	sb.WriteString("\nStop the occupying process, or give this profile a different `port` — in its own section or under `defaults`")
	return errors.New(sb.String())
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
	// starting reports whether a still-starting (Starting, ADR-0010)
	// server of b's answers at addr.
	starting(b LLMServer, addr string) bool
	// loadedModel returns the model currently loaded at addr; empty when
	// nothing is loaded or b cannot list models.
	loadedModel(b LLMServer, addr string) string
	// liveDrift diffs the live server parameters at addr against the
	// freshly resolved profile params (ADR-0007 drift notice).
	liveDrift(b LLMServer, addr string, fresh ProfileParams) []string
	// discover returns the running instances derivable from cfg.
	discover(cfg *Config) []*RunningInstance
	// authRefusal returns the authFailedErr-wrapping error when the server
	// at addr refuses the configured api_key by discovery's rule (every
	// backend the config points there answers 401/403, none is healthy or
	// Starting), else nil.
	authRefusal(cfg *Config, addr string) error
	// identify names the backend serving at addr (identifyBackend): an
	// error wrapping ErrAuthFailed when only a 401/403 answered there,
	// ErrNotRunning when nothing did.
	identify(addr string) (string, error)
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

func (realOps) starting(b LLMServer, addr string) bool { return startingUp(b, addr) }

func (realOps) loadedModel(b LLMServer, addr string) string { return liveLoadedModel(b, addr) }

func (realOps) liveDrift(b LLMServer, addr string, fresh ProfileParams) []string {
	return liveParamDrift(b, addr, fresh)
}

func (realOps) discover(cfg *Config) []*RunningInstance { return DiscoverRunningInstances(cfg) }

func (realOps) authRefusal(cfg *Config, addr string) error { return authRefusalAt(cfg, addr) }

func (realOps) identify(addr string) (string, error) { return identifyBackend(addr) }

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
// parameters (Ollama, LM Studio, Splash), model-name match alone is enough
// for the idempotency no-op. Pass restart=true to force re-activation. A
// Starting occupant at the target address (ADR-0010) is never displaced by
// a plain load — the call refuses with guidance; with restart=true it is
// stopped and replaced like a healthy one.
func LoadProfile(cfg *Config, profile *ResolvedProfile, restart bool, progress ProgressFunc) (*RunningInstance, bool, error) {
	return LoadProfileNotify(cfg, profile, restart, progress, func(n string) { fmt.Fprint(os.Stderr, n) })
}

// LoadProfileNotify activates a profile exactly as LoadProfile does, but
// delivers the ADR-0007 drift notice to notify instead of stderr: one call
// carrying the full formatted text (header, one line per drifted field, the
// --restart guidance). A nil sink discards it.
func LoadProfileNotify(cfg *Config, profile *ResolvedProfile, restart bool, progress ProgressFunc, notify NoticeFunc) (*RunningInstance, bool, error) {
	return loadProfile(realOps{}, cfg, profile, restart, progress, notify)
}

// loadProfile is the activation orchestration behind LoadProfile. It drives
// every process/health/probe effect through ops (ADR-0009) and carries the
// single targetAddr derived from the resolved profile.
func loadProfile(ops activationOps, cfg *Config, profile *ResolvedProfile, restart bool, progress ProgressFunc, notify NoticeFunc) (*RunningInstance, bool, error) {
	targetAddr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)

	b, err := GetLLMServer(profile.Backend)
	if err != nil {
		return nil, false, err
	}

	healthy := ops.healthy(b, targetAddr)
	starting := ops.starting(b, targetAddr)

	// A server refusing the configured api_key can be neither loaded into
	// nor safely replaced: refuse with the auth error. The profile backend's
	// own health error never decides this — a healthy Splash answers 403 to
	// a probe naming a wildcard host, and it must still be auto-stopped —
	// only discovery's every-backend rule does.
	if !healthy && !starting {
		if err := ops.authRefusal(cfg, targetAddr); err != nil {
			return nil, false, err
		}
	}

	if !restart && healthy {
		liveModel := ops.loadedModel(b, targetAddr)
		if liveModel != "" && profile.ModelPath != "" && modelNamesMatch(profile.ModelPath, liveModel) {
			drifts := ops.liveDrift(b, targetAddr, profile.ProfileParams)
			if len(drifts) > 0 {
				reportNotice(notify, driftNotice(profile.Name, targetAddr, drifts))
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
			// A server refusing the api_key is never swept: no backend
			// identified it, so only an explicit stop signals it.
			if inst.AuthFailed {
				continue
			}
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
		return loadProfileManaged(ops, cfg, profile, targetAddr, healthy, starting, restart, b, progress)
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
// Backends that do not implement LiveParamsQuerier (Ollama, LM Studio,
// Splash) contribute no drift — model-name match is the only idempotency signal
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

// driftNotice builds the ADR-0007 drift notice as one block of text: the
// header naming the profile and its address, one indented line per drifted
// field, and the --restart guidance. The CLI prints the result verbatim to
// stderr; library clients receive it through their NoticeFunc.
func driftNotice(profileName, addr string, drifts []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Notice: profile %q already active at %s, but its parameters have drifted:\n", profileName, addr)
	for _, d := range drifts {
		fmt.Fprintf(&b, "  %s\n", d)
	}
	fmt.Fprintf(&b, "Run `llama-launcher load %s --restart` to apply the new parameters.\n", profileName)
	return b.String()
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

// loadProfileManaged is the managed-backend arm of the activation: stop
// the current occupant of the target address when there is one, start a
// fresh server, and wait for it to turn healthy. A Starting occupant
// (ADR-0010) is displaced only by an explicit --restart — a plain load
// refuses with guidance instead of silently killing an in-flight model
// load. The StartingUp guard inside startManagedServer stays as the
// backstop for the race where an occupant appears between this decision
// and the spawn.
func loadProfileManaged(ops activationOps, cfg *Config, profile *ResolvedProfile, targetAddr string, healthy, starting, restart bool, b LLMServer, progress ProgressFunc) (*RunningInstance, bool, error) {
	if starting && !restart {
		return nil, false, stillStartingUpErr(cfg, b, targetAddr)
	}

	if healthy || starting {
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
// plain retry while it is still loading is refused (see
// stillStartingUpErr); a retry after it turns healthy is the idempotent
// no-op (ADR-0007). Stopping a Starting server is the launcher's own job
// now (ADR-0010), so the guidance points at `llama-launcher stop`, not
// at a manual `kill`.
func startupTimeoutErr(err error, inst *RunningInstance) error {
	return startupTimeout{fmt.Errorf("%w\nThe server may still be loading its model — it was left running (PID %d)\nLog: %s\nWatch it with `llama-launcher logs %s` and retry once it is healthy, or stop it with `llama-launcher stop %s`",
		err, inst.PID, inst.LogFile, inst.Backend, inst.Backend)}
}

// startupTimeout carries a decorated health-wait timeout unchanged and adds
// ErrStartupTimeout as a second unwrap branch. Wrapping rather than
// reformatting is what keeps the message — the PID and log path a user acts
// on — byte-identical while errors.Is finds both the sentinel and the
// underlying wait failure.
type startupTimeout struct{ decorated error }

func (e startupTimeout) Error() string { return e.decorated.Error() }

func (e startupTimeout) Unwrap() []error { return []error{e.decorated, ErrStartupTimeout} }

// stillStartingUpErr builds the refusal for activating an address where
// a server of the same managed backend is still coming up (Starting,
// ADR-0010): neither a plain load nor a bare start displaces an
// in-flight model load — only an explicit stop or --restart does. PID
// and log-path details are best-effort — a Starting server cannot answer
// a model list, so the PID comes straight from lsof.
func stillStartingUpErr(cfg *Config, b LLMServer, addr string) error {
	pid, err := findListeningPID(addr)
	if err != nil || pid <= 0 {
		pid = loadingPID(b, addr)
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
	fmt.Fprintf(&sb, "\nWatch it with `llama-launcher logs %s` and retry once it is healthy, stop it with `llama-launcher stop %s`, or pass `--restart` to replace it", b.Name(), b.Name())
	return errors.New(sb.String())
}

// UnloadInstanceModel unloads the active model for the instance at the given
// address without stopping the server. A server that answers only with
// 401/403 is refused with the auth error: its model list cannot be read, so
// "nothing loaded" would be a false success.
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

	// Refused before the managed/external split: a managed unload is a stop,
	// and a server that refuses the api_key is stopped only on an explicit
	// stop. Any other identification outcome is left to the mechanics below.
	if _, identifyErr := ops.identify(addr); errors.Is(identifyErr, ErrAuthFailed) {
		return &StopResult{}, identifyErr
	}

	rec := &stepRecorder{}
	if _, isManaged := b.(ManagedLLMServer); isManaged {
		inst, stopErr := ops.stop(addr, rec.record)
		return &StopResult{Instance: inst, ServerStopped: true, Steps: rec.steps}, stopErr
	}

	inst, unloadErr := ops.unloadInstance(addr, rec.record)
	return &StopResult{Instance: inst, Steps: rec.steps}, unloadErr
}

// legacyStateFileMaxBytes caps the size of a file the legacy state cleanup
// will read and judge. A launcher-written state file held one small JSON
// object; anything larger is not one and stays.
const legacyStateFileMaxBytes = 64 << 10

// legacySharedStateFileName is the single state file the earliest launcher
// versions wrote, before state was split per backend.
const legacySharedStateFileName = "state.json"

// legacyStateFileNamePattern matches the per-backend state file names earlier
// launcher versions wrote: state-<backend>.json and state-<backend>-<suffix>.json.
// The captured group is the backend the name claims.
var legacyStateFileNamePattern = regexp.MustCompile(`^state-(llamacpp|ollama|lmstudio)(-.+)?\.json$`)

// legacyStateBackends are the backend names a launcher-written state file
// could carry; state.json, whose name claims none, must carry one of them.
var legacyStateBackends = []string{"llamacpp", "ollama", "lmstudio"}

var legacyStateCleanupOnce sync.Once

// CleanupLegacyStateFiles deletes the state files earlier versions of the
// launcher persisted server state to, from the default config directory. It
// runs once per process. Silent on failure — these files are best-effort
// cleanup, not load-bearing.
func CleanupLegacyStateFiles() {
	legacyStateCleanupOnce.Do(func() {
		cleanupLegacyStateFiles(DefaultConfigDir())
	})
}

// cleanupLegacyStateFiles removes every state.json and state-*.json file in dir
// that isLegacyStateFile recognises as launcher-written. The config directory
// is the user's, so a file that merely shares the name pattern stays. Removal
// failures are ignored on purpose: nothing reads these files any more.
func cleanupLegacyStateFiles(dir string) {
	candidates := []string{filepath.Join(dir, legacySharedStateFileName)}
	if matches, err := filepath.Glob(filepath.Join(dir, "state-*.json")); err == nil {
		candidates = append(candidates, matches...)
	}
	for _, path := range candidates {
		if isLegacyStateFile(path) {
			os.Remove(path)
		}
	}
}

// isLegacyStateFile reports whether path is a state file an earlier launcher
// version wrote: a legacy state file name, a regular file (never a symlink)
// owned by the current user where the platform can tell, at most
// legacyStateFileMaxBytes long, holding one JSON object whose backend matches
// the name's, whose pid is a number ≥ 0 (external connects stored 0) and whose
// port is a number > 0.
func isLegacyStateFile(path string) bool {
	nameBackend := ""
	if name := filepath.Base(path); name != legacySharedStateFileName {
		match := legacyStateFileNamePattern.FindStringSubmatch(name)
		if match == nil {
			return false
		}
		nameBackend = match[1]
	}

	data, ok := readLegacyStateCandidate(path)
	if !ok {
		return false
	}

	// Pointers tell an absent pid or port from a zero one; float64 refuses a
	// quoted number, which the launcher never wrote.
	var state struct {
		Backend string   `json:"backend"`
		PID     *float64 `json:"pid"`
		Port    *float64 `json:"port"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false
	}
	if state.PID == nil || *state.PID < 0 || state.Port == nil || *state.Port <= 0 {
		return false
	}
	if nameBackend != "" {
		return state.Backend == nameBackend
	}
	return slices.Contains(legacyStateBackends, state.Backend)
}

// readLegacyStateCandidate returns the contents of path when it is a regular
// file owned by the current user and no larger than legacyStateFileMaxBytes.
// The opened file is checked against the inspected one, so a file swapped in
// between the two is never read.
func readLegacyStateCandidate(path string) ([]byte, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > legacyStateFileMaxBytes || !ownedByCurrentUser(info) {
		return nil, false
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, legacyStateFileMaxBytes+1))
	if err != nil || len(data) > legacyStateFileMaxBytes {
		return nil, false
	}
	return data, true
}

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return signalPID(pid, 0) == nil
}

// TailLog prints the last lines of logPath to stdout, following the file as it
// grows when follow is set. Every line passes through RedactLogText first.
//
// redaction-required: callers must pass the profile's resolved keys
// (cfg.APIKeyFor(inst.Backend)); redaction sits in RedactLogText.
func TailLog(logPath string, follow bool, keys []string) error {
	args := []string{"-n", defaultTailLines}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, logPath)

	cmd := exec.Command("tail", args...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("piping tail output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting tail: %w", err)
	}

	// Line by line, so a key is never split across two redaction passes and
	// a followed log still appears as each line lands.
	copyErr := copyRedactedLines(os.Stdout, stdout, keys)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return waitErr
	}
	return copyErr
}

// copyRedactedLines copies src to dst one line at a time, masking each line
// with RedactLogText. A final line without a trailing newline is copied too.
func copyRedactedLines(dst io.Writer, src io.Reader, keys []string) error {
	reader := bufio.NewReader(src)
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			if _, err := io.WriteString(dst, RedactLogText(line, keys)); err != nil {
				return fmt.Errorf("writing log output: %w", err)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("reading log output: %w", readErr)
		}
	}
}

// logCreateAttempts bounds createLogPath's retries when a freshly stamped
// name is already taken; each retry waits a millisecond for a new stamp.
const logCreateAttempts = 50

// createLogPath creates a new log file for a server start named name and
// returns it open for writing; its Name() is the log's path. The name embeds
// the start time at millisecond precision (logTimestampFormat) and the file is
// created exclusively, so two starts never share — or truncate — one log: a
// name that already exists is retried with a fresh stamp.
func createLogPath(cfg *Config, name string) (*os.File, error) {
	autoCleanupLogs(cfg)
	if err := os.MkdirAll(cfg.LogDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating log directory: %w", err)
	}
	for range logCreateAttempts {
		ts := time.Now().Format(logTimestampFormat)
		path := filepath.Join(cfg.LogDir, fmt.Sprintf("%s-%s.log", name, ts))
		logFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err == nil {
			return logFile, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("creating log file: %w", err)
		}
		time.Sleep(time.Millisecond)
	}
	return nil, fmt.Errorf("creating log file: no unused %s log name in %s after %d attempts", name, cfg.LogDir, logCreateAttempts)
}

// readLastLines returns the last n lines of the log at path, masked by
// RedactLogText, or a placeholder when the file cannot be read.
//
// redaction-required: callers must pass the profile's resolved keys
// (cfg.APIKeyFor(profile.Backend)); redaction sits in RedactLogText.
func readLastLines(path string, n int, keys []string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "(could not read log)"
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return RedactLogText(strings.Join(lines, "\n"), keys)
}
