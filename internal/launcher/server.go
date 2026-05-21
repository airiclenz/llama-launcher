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
)

var ErrNotRunning = errors.New("no server running")

type ServerState struct {
	PID           int       `json:"pid"`
	Managed       bool      `json:"managed"`
	Backend       string    `json:"backend"`
	Host          string    `json:"host"`
	Port          int       `json:"port"`
	StartedAt     time.Time `json:"started_at"`
	LogFile       string    `json:"log_file,omitempty"`
	ActiveProfile string    `json:"active_profile,omitempty"`
	ActiveModel   string    `json:"active_model,omitempty"`
	ContextSize   int       `json:"context_size,omitempty"`
	GPULayers     int       `json:"gpu_layers,omitempty"`
}

func (s *ServerState) Uptime() time.Duration {
	return time.Since(s.StartedAt).Truncate(time.Second)
}

func (s *ServerState) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// IsServerAlive checks whether the tracked server is still reachable.
func IsServerAlive(state *ServerState) bool {
	if state.Managed {
		return IsProcessAlive(state.PID)
	}
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

	ctxSize := 0
	if profile.ContextSize != nil {
		ctxSize = *profile.ContextSize
	}
	gpuLayers := 0
	if profile.GPULayers != nil {
		gpuLayers = *profile.GPULayers
	}

	state := &ServerState{
		PID:           cmd.Process.Pid,
		Managed:       true,
		Backend:       profile.Backend,
		Host:          *profile.Host,
		Port:          *profile.Port,
		StartedAt:     time.Now(),
		LogFile:       logPath,
		ActiveProfile: profile.Name,
		ActiveModel:   profile.ModelPath,
		ContextSize:   ctxSize,
		GPULayers:     gpuLayers,
	}

	if err := writeBackendState(state.Backend, state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	time.Sleep(startupGracePeriod)
	if !IsProcessAlive(state.PID) {
		removeBackendState(profile.Backend)
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
		PID:           pid,
		Managed:       pid > 0,
		Backend:       profile.Backend,
		Host:          *profile.Host,
		Port:          *profile.Port,
		StartedAt:     time.Now(),
		LogFile:       logFile,
		ActiveProfile: profile.Name,
		ActiveModel:   profile.ModelPath,
	}

	if err := writeBackendState(state.Backend, state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	return state, nil
}

// StopBackendServer stops a managed server or disconnects from an external one for the given backend.
func StopBackendServer(backend string, progress ProgressFunc) (*ServerState, error) {
	state, err := ReadBackendState(backend)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, ErrNotRunning
	}

	if state.Managed {
		return stopManagedServer(state, progress)
	}
	return disconnectExternalServer(state, progress)
}

func stopManagedServer(state *ServerState, progress ProgressFunc) (*ServerState, error) {
	if !IsProcessAlive(state.PID) {
		removeBackendState(state.Backend)
		return state, nil
	}

	proc, err := os.FindProcess(state.PID)
	if err != nil {
		removeBackendState(state.Backend)
		return state, nil
	}

	reportStep(progress, "Sending stop signal")
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		removeBackendState(state.Backend)
		return state, fmt.Errorf("sending SIGTERM: %w", err)
	}
	// Also signal the process group (Setsid gives the child PGID=PID).
	_ = syscall.Kill(-state.PID, syscall.SIGTERM)

	reportStep(progress, "Waiting for shutdown")
	deadline := time.Now().Add(sigtermTimeout)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(state.PID) {
			removeBackendState(state.Backend)
			return state, nil
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

	removeBackendState(state.Backend)
	return state, nil
}

func disconnectExternalServer(state *ServerState, progress ProgressFunc) (*ServerState, error) {
	reportStep(progress, "Disconnecting")
	b, _ := GetBackend(state.Backend)
	if b != nil {
		b.TryStop(state.Addr())
	}
	removeBackendState(state.Backend)
	return state, nil
}

// EnsureServer returns the running server state, starting one if needed.
func EnsureServer(cfg *Config, profile *ResolvedProfile) (*ServerState, bool, error) {
	state, _ := ReadBackendState(profile.Backend)

	if state != nil && IsServerAlive(state) {
		return state, false, nil
	}
	if state != nil {
		removeBackendState(profile.Backend)
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
	state, _ := ReadBackendState(profile.Backend)

	b, err := GetBackend(profile.Backend)
	if err != nil {
		return nil, false, err
	}

	if cfg.ShouldAutoStopServer() {
		allStates, _ := ReadAllStates()
		for _, s := range allStates {
			if s.Backend != profile.Backend && IsServerAlive(s) {
				reportStep(progress, fmt.Sprintf("Stopping %s", backendDisplayName(s.Backend)))
				if _, err := StopBackendServer(s.Backend, nil); err != nil && !errors.Is(err, ErrNotRunning) {
					return nil, false, fmt.Errorf("auto-stopping %s: %w", s.Backend, err)
				}
			}
		}
	}

	if _, ok := b.(ManagedBackend); ok {
		return loadProfileManaged(cfg, profile, state, b, progress)
	}
	return loadProfileExternal(cfg, profile, b, state, progress)
}

func loadProfileManaged(cfg *Config, profile *ResolvedProfile, state *ServerState, b Backend, progress ProgressFunc) (*ServerState, bool, error) {
	if state != nil && IsServerAlive(state) {
		if state.ActiveProfile == profile.Name {
			return state, false, nil
		}
		reportStep(progress, "Stopping current server")
		if _, err := StopBackendServer(profile.Backend, nil); err != nil {
			return nil, false, fmt.Errorf("stopping current server: %w", err)
		}
	} else if state != nil {
		removeBackendState(profile.Backend)
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
			removeBackendState(profile.Backend)
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
	if err := writeBackendState(state.Backend, state); err != nil {
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

// UnloadBackendModel unloads the active model for the given backend without stopping the server.
func UnloadBackendModel(backend string, progress ProgressFunc) (*ServerState, error) {
	state, err := ReadBackendState(backend)
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
	if err := writeBackendState(state.Backend, state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	return state, nil
}

func backendStatePath(backend string) string {
	return filepath.Join(DefaultConfigDir(), fmt.Sprintf("state-%s.json", backend))
}

func ReadBackendState(backend string) (*ServerState, error) {
	migrateOldState()

	data, err := os.ReadFile(backendStatePath(backend))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var state ServerState
	if err := json.Unmarshal(data, &state); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: corrupt state file for %s, removing\n", backend)
		removeBackendState(backend)
		return nil, nil
	}

	return &state, nil
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

func writeBackendState(backend string, state *ServerState) error {
	dir := DefaultConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	path := backendStatePath(backend)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing temp state: %w", err)
	}

	return os.Rename(tmpPath, path)
}

func removeBackendState(backend string) {
	os.Remove(backendStatePath(backend))
}

var migrateOnce sync.Once

func migrateOldState() {
	migrateOnce.Do(func() {
		oldPath := filepath.Join(DefaultConfigDir(), "state.json")
		data, err := os.ReadFile(oldPath)
		if err != nil {
			return
		}
		var state ServerState
		if err := json.Unmarshal(data, &state); err != nil {
			os.Remove(oldPath)
			return
		}
		if state.Backend != "" {
			if err := writeBackendState(state.Backend, &state); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to migrate old state: %v\n", err)
				return
			}
		}
		os.Remove(oldPath)
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
