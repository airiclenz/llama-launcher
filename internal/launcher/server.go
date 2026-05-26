package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

type ServerState struct {
	PID            int           `json:"pid"`
	Backend        string        `json:"backend"`
	Host           string        `json:"host"`
	Port           int           `json:"port"`
	StartedAt      time.Time     `json:"started_at"`
	LogFile        string        `json:"log_file,omitempty"`
	ActiveProfile  string        `json:"active_profile,omitempty"`
	ActiveModel    string        `json:"active_model,omitempty"`
	ContextSize    int           `json:"context_size,omitempty"`
	GPULayers      int           `json:"gpu_layers,omitempty"`
	ResolvedParams ProfileParams `json:"resolved_params,omitempty"`
}

func (s *ServerState) Uptime() time.Duration {
	return time.Since(s.StartedAt).Truncate(time.Second)
}

func (s *ServerState) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// IsServerAlive checks whether the recorded server is reachable. Liveness is
// decided by the backend's health check, not by PID (see ADR-0001).
func IsServerAlive(state *ServerState) bool {
	b, err := GetBackend(state.Backend)
	if err != nil {
		return false
	}
	return b.HealthCheck(state.Addr()) == nil
}

// StartServer launches a managed server or connects to an external one.
func StartServer(cfg *Config, profile *ResolvedProfile) (*ServerState, error) {
	b, err := GetBackend(profile.Backend)
	if err != nil {
		return nil, err
	}

	if mb, ok := b.(ManagedBackend); ok {
		return startManagedServer(cfg, profile, mb)
	}
	return connectExternalServer(cfg, profile, b)
}

func startManagedServer(cfg *Config, profile *ResolvedProfile, mb ManagedBackend) (*ServerState, error) {
	binary := mb.ServerBinary(cfg)
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("server binary not found: %s", binary)
	}

	args := mb.BuildServerArgs(cfg, profile)
	logPath, err := createLogPath(cfg.LogDir, profile.Backend, cfg.LogRetention)
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

	state := &ServerState{
		PID:       cmd.Process.Pid,
		Backend:   profile.Backend,
		Host:      *profile.Host,
		Port:      *profile.Port,
		StartedAt: time.Now(),
		LogFile:   logPath,
	}

	if err := writeInstanceState(state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	time.Sleep(startupGracePeriod)
	if !IsProcessAlive(state.PID) {
		removeInstanceState(state)
		tail := readLastLines(logPath, crashLogTailLines)
		return nil, fmt.Errorf("server exited immediately after start\nLog tail:\n%s", tail)
	}

	return state, nil
}

func connectExternalServer(cfg *Config, profile *ResolvedProfile, b Backend) (*ServerState, error) {
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

	state := &ServerState{
		PID:       pid,
		Backend:   profile.Backend,
		Host:      *profile.Host,
		Port:      *profile.Port,
		StartedAt: time.Now(),
		LogFile:   logFile,
	}

	if err := writeInstanceState(state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	return state, nil
}

// StopInstance stops the LLM Server instance at the given address. Stop is
// unconditional (ADR-0001): the launcher does not distinguish servers it
// started from servers that were already running. If a PID is recorded and
// alive, the process is signalled; the backend's TryStop is also called so
// each backend's native shutdown command runs (`ollama stop`, `lms server
// stop`). The per-instance state file is removed in either case.
func StopInstance(addr string, progress ProgressFunc) (*ServerState, error) {
	state, err := ReadInstanceState(addr)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, ErrNotRunning
	}
	return stopInstance(state, progress)
}

func stopInstance(state *ServerState, progress ProgressFunc) (*ServerState, error) {
	if state.PID > 0 && IsProcessAlive(state.PID) {
		signalAndWait(state, progress)
	}

	if b, err := GetBackend(state.Backend); err == nil {
		reportStep(progress, "Disconnecting")
		b.TryStop(state.Addr())
	}

	removeInstanceState(state)
	return state, nil
}

func signalAndWait(state *ServerState, progress ProgressFunc) {
	proc, err := os.FindProcess(state.PID)
	if err != nil {
		return
	}

	reportStep(progress, "Sending stop signal")
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return
	}
	// Also signal the process group (Setsid gives the child PGID=PID).
	_ = syscall.Kill(-state.PID, syscall.SIGTERM)

	reportStep(progress, "Waiting for shutdown")
	deadline := time.Now().Add(sigtermTimeout)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(state.PID) {
			return
		}
		time.Sleep(stopPollInterval)
	}

	_ = proc.Signal(syscall.SIGKILL)
	_ = syscall.Kill(-state.PID, syscall.SIGKILL)

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(state.PID) {
			break
		}
		time.Sleep(stopPollInterval)
	}
	time.Sleep(startupGracePeriod)
}

// EnsureServer returns the running server state, starting one if needed.
func EnsureServer(cfg *Config, profile *ResolvedProfile) (*ServerState, bool, error) {
	addr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)
	state, _ := ReadInstanceState(addr)

	if state != nil && IsServerAlive(state) {
		return state, false, nil
	}
	if state != nil {
		removeInstanceState(state)
	}

	b, err := GetBackend(profile.Backend)
	if err != nil {
		return nil, false, err
	}

	state, err = StartServer(cfg, profile)
	if err != nil {
		return nil, false, err
	}

	if _, ok := b.(ManagedBackend); ok {
		if err := WaitForHealth(b, state.Addr(), 15*time.Second); err != nil {
			return nil, false, err
		}
	}

	return state, true, nil
}

// LoadProfile stops any existing server and starts a new one with the given profile's model.
func LoadProfile(cfg *Config, profile *ResolvedProfile, progress ProgressFunc) (*ServerState, bool, error) {
	targetAddr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)
	state, _ := ReadInstanceState(targetAddr)

	b, err := GetBackend(profile.Backend)
	if err != nil {
		return nil, false, err
	}

	if cfg.ShouldAutoStopServer() {
		allStates, _ := ReadAllStates()
		for _, s := range allStates {
			if s.Addr() == targetAddr {
				continue
			}
			if !IsServerAlive(s) {
				continue
			}
			reportStep(progress, fmt.Sprintf("Stopping %s", backendDisplayName(s.Backend)))
			if _, err := StopInstance(s.Addr(), nil); err != nil && !errors.Is(err, ErrNotRunning) {
				return nil, false, fmt.Errorf("auto-stopping %s: %w", s.Backend, err)
			}
		}
	} else if cfg.ShouldAutoUnload() {
		allStates, _ := ReadAllStates()
		for _, s := range allStates {
			if s.Addr() == targetAddr {
				continue
			}
			if !shouldCrossServerUnload(s, profile.Backend) {
				continue
			}
			if !IsServerAlive(s) {
				continue
			}
			reportStep(progress, fmt.Sprintf("Unloading model on %s", backendDisplayName(s.Backend)))
			if _, err := UnloadInstanceModel(s.Addr(), nil); err != nil && !errors.Is(err, ErrNotRunning) {
				return nil, false, fmt.Errorf("auto-unloading %s: %w", s.Backend, err)
			}
		}
	}

	if _, ok := b.(ManagedBackend); ok {
		return loadProfileManaged(cfg, profile, state, b, progress)
	}
	return loadProfileExternal(cfg, profile, b, state, progress)
}

// shouldCrossServerUnload reports whether the given instance is a candidate
// for cross-server auto_unload when activating a profile on targetBackend.
// Managed backends are skipped: an unload without a stop is not possible for
// them (see ADR-0004), so auto_unload is silently ignored on those instances.
func shouldCrossServerUnload(s *ServerState, targetBackend string) bool {
	if s == nil || s.Backend == targetBackend || s.ActiveModel == "" {
		return false
	}
	b, err := GetBackend(s.Backend)
	if err != nil {
		return false
	}
	_, isManaged := b.(ManagedBackend)
	return !isManaged
}

func loadProfileManaged(cfg *Config, profile *ResolvedProfile, state *ServerState, b Backend, progress ProgressFunc) (*ServerState, bool, error) {
	if state != nil && IsServerAlive(state) {
		if state.ActiveProfile == profile.Name {
			return state, false, nil
		}
		reportStep(progress, "Stopping current server")
		if _, err := StopInstance(state.Addr(), nil); err != nil {
			return nil, false, fmt.Errorf("stopping current server: %w", err)
		}
	} else if state != nil {
		removeInstanceState(state)
	}

	reportStep(progress, "Starting server")
	state, err := StartServer(cfg, profile)
	if err != nil {
		return nil, false, err
	}

	reportStep(progress, "Waiting for server")
	if err := WaitForHealth(b, state.Addr(), 30*time.Second); err != nil {
		return nil, false, err
	}

	state.ActiveProfile = profile.Name
	state.ActiveModel = profile.ModelPath
	if profile.ContextSize != nil {
		state.ContextSize = *profile.ContextSize
	}
	if profile.GPULayers != nil {
		state.GPULayers = *profile.GPULayers
	}
	state.ResolvedParams = profile.ProfileParams
	if err := writeInstanceState(state); err != nil {
		return nil, false, fmt.Errorf("writing state: %w", err)
	}

	return state, true, nil
}

func loadProfileExternal(cfg *Config, profile *ResolvedProfile, b Backend, state *ServerState, progress ProgressFunc) (*ServerState, bool, error) {
	if state != nil && IsServerAlive(state) {
		if state.ActiveProfile == profile.Name {
			return state, false, nil
		}
		if state.ActiveModel != "" && cfg.ShouldAutoUnload() {
			reportStep(progress, "Unloading current model")
			if err := b.UnloadModel(state.Addr(), state.ActiveModel); err != nil {
				return nil, false, fmt.Errorf("unloading current model: %w", err)
			}
		}
	} else {
		if state != nil {
			removeInstanceState(state)
		}
		reportStep(progress, "Connecting to server")
		newState, err := connectExternalServer(cfg, profile, b)
		if err != nil {
			return nil, false, err
		}
		state = newState
	}

	reportStep(progress, "Loading model")
	if err := b.LoadModel(state.Addr(), profile); err != nil {
		return nil, false, fmt.Errorf("loading model: %w", err)
	}

	state.ActiveProfile = profile.Name
	state.ActiveModel = profile.ModelPath
	if profile.ContextSize != nil {
		state.ContextSize = *profile.ContextSize
	}
	state.ResolvedParams = profile.ProfileParams
	if err := writeInstanceState(state); err != nil {
		return nil, false, fmt.Errorf("writing state: %w", err)
	}

	return state, true, nil
}

// WaitForHealth polls the backend's health check until it succeeds or times out.
func WaitForHealth(b Backend, addr string, timeout time.Duration) error {
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
func UnloadInstanceModel(addr string, progress ProgressFunc) (*ServerState, error) {
	state, err := ReadInstanceState(addr)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, ErrNotRunning
	}
	if state.ActiveModel == "" {
		return state, nil
	}

	b, err := GetBackend(state.Backend)
	if err != nil {
		return nil, err
	}

	reportStep(progress, "Unloading model")
	if err := b.UnloadModel(state.Addr(), state.ActiveModel); err != nil {
		return nil, fmt.Errorf("unloading model: %w", err)
	}

	state.ActiveProfile = ""
	state.ActiveModel = ""
	state.ContextSize = 0
	state.ResolvedParams = ProfileParams{}
	if err := writeInstanceState(state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	return state, nil
}

// instanceStatePath returns the state-file path for an instance. The host is
// omitted when loopback (127.0.0.1) and included otherwise. See ADR-0006.
func instanceStatePath(backend, host string, port int) string {
	var name string
	if host == "" || host == loopbackHost {
		name = fmt.Sprintf("state-%s-%d.json", backend, port)
	} else {
		name = fmt.Sprintf("state-%s-%s-%d.json", backend, host, port)
	}
	return filepath.Join(DefaultConfigDir(), name)
}

// ReadInstanceState returns the state record for the instance bound to addr,
// or nil if no record exists. Scans state files because the backend type is
// not known from the address alone (any backend could in principle bind a
// given port).
func ReadInstanceState(addr string) (*ServerState, error) {
	states, err := ReadAllStates()
	if err != nil {
		return nil, err
	}
	for _, s := range states {
		if s.Addr() == addr {
			return s, nil
		}
	}
	return nil, nil
}

// ReadInstancesForBackend returns all instance state records whose backend
// matches the given name.
func ReadInstancesForBackend(backend string) ([]*ServerState, error) {
	states, err := ReadAllStates()
	if err != nil {
		return nil, err
	}
	var matches []*ServerState
	for _, s := range states {
		if s.Backend == backend {
			matches = append(matches, s)
		}
	}
	return matches, nil
}

func ReadAllStates() ([]*ServerState, error) {
	migrateOldState()

	pattern := filepath.Join(DefaultConfigDir(), "state-*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	var states []*ServerState
	for _, path := range matches {
		if strings.HasSuffix(path, ".tmp") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state ServerState
		if err := json.Unmarshal(data, &state); err != nil {
			continue
		}
		states = append(states, &state)
	}
	return states, nil
}

func writeInstanceState(state *ServerState) error {
	dir := DefaultConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	path := instanceStatePath(state.Backend, state.Host, state.Port)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing temp state: %w", err)
	}

	return os.Rename(tmpPath, path)
}

func removeInstanceState(state *ServerState) {
	os.Remove(instanceStatePath(state.Backend, state.Host, state.Port))
}

var migrateOnce sync.Once

// migrateOldState removes legacy state files. The pre-ADR-0006 schemes
// (`state.json` and per-backend `state-{backend}.json`) are no longer
// readable — the per-instance API keys by address. Legacy records are
// treated as stale and deleted; if the recorded process is still alive,
// the user re-activates the relevant Profile to recreate the per-instance
// state record. See ADR-0006.
func migrateOldState() {
	migrateOnce.Do(func() {
		removeLegacyStateFiles(DefaultConfigDir())
	})
}

func removeLegacyStateFiles(dir string) {
	// Remove the original single-file state.
	os.Remove(filepath.Join(dir, "state.json"))

	// Remove legacy per-backend files of the form state-{backend}.json
	// (exactly one dash-separated segment after "state"). New per-instance
	// files always include a port and so have two or more segments.
	matches, err := filepath.Glob(filepath.Join(dir, "state-*.json"))
	if err != nil {
		return
	}
	for _, path := range matches {
		base := filepath.Base(path)
		name := strings.TrimSuffix(strings.TrimPrefix(base, "state-"), ".json")
		if !strings.Contains(name, "-") {
			os.Remove(path)
		}
	}
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

func createLogPath(logDir, name string, retentionDays *int) (string, error) {
	if retentionDays != nil {
		autoCleanupLogs(logDir, *retentionDays)
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return "", fmt.Errorf("creating log directory: %w", err)
	}
	ts := time.Now().Format("20060102-150405")
	return filepath.Join(logDir, fmt.Sprintf("%s-%s.log", name, ts)), nil
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
