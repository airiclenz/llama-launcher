package launcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrNotRunning indicates no server process is active.
var ErrNotRunning = errors.New("no server running")

// ServerState holds the persisted state of a running server instance.
type ServerState struct {
	PID           int       `json:"pid"`
	Backend       string    `json:"backend"`
	Host          string    `json:"host"`
	Port          int       `json:"port"`
	StartedAt     time.Time `json:"started_at"`
	LogFile       string    `json:"log_file"`
	ActiveProfile string    `json:"active_profile,omitempty"`
	ActiveModel   string    `json:"active_model,omitempty"`
	ContextSize   int       `json:"context_size,omitempty"`
	GPULayers     int       `json:"gpu_layers,omitempty"`
}

// Uptime returns the duration since the server started.
func (s *ServerState) Uptime() time.Duration {
	return time.Since(s.StartedAt).Truncate(time.Second)
}

// Addr returns the host:port address of the server.
func (s *ServerState) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// StartServer launches the server process in the background with the given profile.
func StartServer(cfg *Config, profile *ResolvedProfile) (*ServerState, error) {
	b, err := GetBackend(profile.Backend)
	if err != nil {
		return nil, err
	}

	binary := b.ServerBinary(cfg)
	if _, err := exec.LookPath(binary); err != nil {
		return nil, fmt.Errorf("server binary not found: %s", binary)
	}

	args := b.BuildServerArgs(cfg, profile)
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

// StopServer sends SIGTERM to the running server, escalating to SIGKILL after 10 seconds.
func StopServer() (*ServerState, error) {
	state, err := ReadState()
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, ErrNotRunning
	}

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

// EnsureServer returns the running server state, starting one if needed.
func EnsureServer(cfg *Config, profile *ResolvedProfile) (*ServerState, bool, error) {
	state, _ := ReadState()

	if state != nil && IsProcessAlive(state.PID) {
		return state, false, nil
	}
	if state != nil {
		RemoveState()
	}

	state, err := StartServer(cfg, profile)
	if err != nil {
		return nil, false, err
	}

	if err := WaitForHealth(state.Addr(), 15*time.Second); err != nil {
		return nil, false, err
	}

	return state, true, nil
}

// LoadProfile stops any existing server and starts a new one with the given profile's model.
func LoadProfile(cfg *Config, profile *ResolvedProfile) (*ServerState, bool, error) {
	state, _ := ReadState()

	if state != nil && IsProcessAlive(state.PID) {
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

	if err := WaitForHealth(state.Addr(), 30*time.Second); err != nil {
		return nil, false, err
	}

	return state, true, nil
}

// WaitForHealth polls the server's /health endpoint until it responds 200 or the timeout expires.
func WaitForHealth(addr string, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("server at %s did not become healthy within %s", addr, timeout)
}

// ReadState loads the persisted server state from disk. Returns nil if no state file exists.
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

// RemoveState deletes the state file from disk.
func RemoveState() {
	os.Remove(StatePath())
}

// IsProcessAlive checks whether a process with the given PID exists.
func IsProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// TailLog prints the last 50 lines of the log file, optionally following new output.
func TailLog(logPath string, follow bool) error {
	args := []string{"-n", "50"}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, logPath)

	cmd := exec.Command("tail", args...)
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
