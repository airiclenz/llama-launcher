package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
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
	logPath, err := createLogPath(cfg.LogDir, profile.Backend)
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

	if err := writeState(state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	time.Sleep(500 * time.Millisecond)
	if !IsProcessAlive(state.PID) {
		RemoveState()
		tail := readLastLines(logPath, 10)
		return nil, fmt.Errorf("server exited immediately after start\nLog tail:\n%s", tail)
	}

	return state, nil
}

func connectExternalServer(cfg *Config, profile *ResolvedProfile, b Backend) (*ServerState, error) {
	addr := fmt.Sprintf("%s:%d", *profile.Host, *profile.Port)

	if err := b.HealthCheck(addr); err != nil {
		if tryErr := b.TryStart(cfg, addr); tryErr == nil {
			if err := waitForBackendHealth(b, addr, 15*time.Second); err != nil {
				return nil, fmt.Errorf("%s not reachable at %s after start attempt: %w", b.DisplayName(), addr, err)
			}
		} else {
			return nil, fmt.Errorf("%s not reachable at %s — start it manually or check the endpoint in config", b.DisplayName(), addr)
		}
	}

	state := &ServerState{
		Managed:       false,
		Backend:       profile.Backend,
		Host:          *profile.Host,
		Port:          *profile.Port,
		StartedAt:     time.Now(),
		ActiveProfile: profile.Name,
		ActiveModel:   profile.ModelPath,
	}

	if err := writeState(state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	return state, nil
}

// StopServer stops a managed server or disconnects from an external one.
func StopServer() (*ServerState, error) {
	state, err := ReadState()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, ErrNotRunning
	}

	if state.Managed {
		return stopManagedServer(state)
	}
	return disconnectExternalServer(state)
}

func stopManagedServer(state *ServerState) (*ServerState, error) {
	if !IsProcessAlive(state.PID) {
		RemoveState()
		return state, nil
	}

	proc, err := os.FindProcess(state.PID)
	if err != nil {
		RemoveState()
		return state, nil
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		RemoveState()
		return state, fmt.Errorf("sending SIGTERM: %w", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(state.PID) {
			RemoveState()
			return state, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = proc.Signal(syscall.SIGKILL)
	fmt.Fprintf(os.Stderr, "Warning: server did not respond to SIGTERM, sent SIGKILL\n")
	RemoveState()
	return state, nil
}

func disconnectExternalServer(state *ServerState) (*ServerState, error) {
	b, _ := GetBackend(state.Backend)
	if b != nil {
		b.TryStop(state.Addr())
	}
	RemoveState()
	return state, nil
}

// EnsureServer returns the running server state, starting one if needed.
func EnsureServer(cfg *Config, profile *ResolvedProfile) (*ServerState, bool, error) {
	state, _ := ReadState()

	if state != nil && IsServerAlive(state) {
		return state, false, nil
	}
	if state != nil {
		RemoveState()
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
func LoadProfile(cfg *Config, profile *ResolvedProfile) (*ServerState, bool, error) {
	state, _ := ReadState()

	b, err := GetBackend(profile.Backend)
	if err != nil {
		return nil, false, err
	}

	if state != nil && state.Backend != profile.Backend {
		if _, err := StopServer(); err != nil {
			return nil, false, fmt.Errorf("stopping current server: %w", err)
		}
		state = nil
	}

	if _, ok := b.(ManagedBackend); ok {
		return loadProfileManaged(cfg, profile, state, b)
	}
	return loadProfileExternal(cfg, profile, b, state)
}

func loadProfileManaged(cfg *Config, profile *ResolvedProfile, state *ServerState, b Backend) (*ServerState, bool, error) {
	if state != nil && IsServerAlive(state) {
		if state.ActiveProfile == profile.Name {
			return state, false, nil
		}
		if _, err := StopServer(); err != nil {
			return nil, false, fmt.Errorf("stopping current server: %w", err)
		}
	} else if state != nil {
		RemoveState()
	}

	state, err := StartServer(cfg, profile)
	if err != nil {
		return nil, false, err
	}

	if err := WaitForHealth(b, state.Addr(), 30*time.Second); err != nil {
		return nil, false, err
	}

	return state, true, nil
}

func loadProfileExternal(cfg *Config, profile *ResolvedProfile, b Backend, state *ServerState) (*ServerState, bool, error) {
	if state != nil && IsServerAlive(state) {
		if state.ActiveProfile == profile.Name {
			return state, false, nil
		}
		if state.ActiveModel != "" {
			b.UnloadModel(state.Addr(), state.ActiveModel)
		}
	} else {
		if state != nil {
			RemoveState()
		}
		newState, err := connectExternalServer(cfg, profile, b)
		if err != nil {
			return nil, false, err
		}
		state = newState
	}

	if err := b.LoadModel(state.Addr(), profile); err != nil {
		return nil, false, fmt.Errorf("loading model: %w", err)
	}

	state.ActiveProfile = profile.Name
	state.ActiveModel = profile.ModelPath
	if profile.ContextSize != nil {
		state.ContextSize = *profile.ContextSize
	}
	if err := writeState(state); err != nil {
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
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("server at %s did not become healthy within %s", addr, timeout)
}

func waitForBackendHealth(b Backend, addr string, timeout time.Duration) error {
	return WaitForHealth(b, addr, timeout)
}

// UnloadCurrentModel unloads the active model for external backends without stopping the server.
func UnloadCurrentModel() (*ServerState, error) {
	state, err := ReadState()
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

	if err := b.UnloadModel(state.Addr(), state.ActiveModel); err != nil {
		return nil, fmt.Errorf("unloading model: %w", err)
	}

	state.ActiveProfile = ""
	state.ActiveModel = ""
	state.ContextSize = 0
	if err := writeState(state); err != nil {
		return nil, fmt.Errorf("writing state: %w", err)
	}

	return state, nil
}

func ReadState() (*ServerState, error) {
	data, err := os.ReadFile(StatePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var state ServerState
	if err := json.Unmarshal(data, &state); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: corrupt state file, removing\n")
		RemoveState()
		return nil, nil
	}

	// Backward compat: legacy state files lack the managed field.
	if state.PID > 0 && !state.Managed {
		state.Managed = true
	}

	return &state, nil
}

func writeState(state *ServerState) error {
	dir := filepath.Dir(StatePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	tmpPath := StatePath() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("writing temp state: %w", err)
	}

	return os.Rename(tmpPath, StatePath())
}

func RemoveState() {
	os.Remove(StatePath())
}

func IsProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TailLog(logPath string, follow bool) error {
	args := []string{"-n", "50"}
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

func createLogPath(logDir, name string) (string, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
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
