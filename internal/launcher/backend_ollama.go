package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Ollama struct {
	lastPID     int
	lastLogFile string
}

func init() {
	RegisterLLMServer(&Ollama{})
}

func (b *Ollama) Name() string        { return "ollama" }
func (b *Ollama) DisplayName() string { return "Ollama" }
func (b *Ollama) DefaultAddr() string { return "localhost:11434" }

func (b *Ollama) HealthCheck(addr string) error {
	resp, err := (&http.Client{Timeout: healthCheckTimeout}).Get("http://" + addr + "/")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("Ollama")) {
		return fmt.Errorf("unexpected response from %s", addr)
	}
	return nil
}

func (b *Ollama) ResolveModel(_ *Config, modelRef string) (string, error) {
	if modelRef == "" {
		return "", nil
	}
	return modelRef, nil
}

func (b *Ollama) LoadModel(addr string, profile *ResolvedProfile) error {
	payload := map[string]interface{}{
		"model":      profile.ModelPath,
		"keep_alive": "24h",
	}
	body, _ := json.Marshal(payload)
	resp, err := (&http.Client{Timeout: modelLoadTimeout}).Post(
		"http://"+addr+"/api/generate",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("loading model via Ollama API: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama load returned status %d", resp.StatusCode)
	}
	return nil
}

func (b *Ollama) UnloadModel(addr string, modelID string) error {
	payload := map[string]interface{}{
		"model":      modelID,
		"keep_alive": 0,
	}
	body, _ := json.Marshal(payload)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Post(
		"http://"+addr+"/api/generate",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("unloading model via Ollama API: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama unload returned status %d", resp.StatusCode)
	}
	return nil
}

func (b *Ollama) TryStart(cfg *Config, addr string) error {
	binary, err := exec.LookPath("ollama")
	if err != nil {
		return fmt.Errorf("ollama binary not found in PATH")
	}

	logPath, err := createLogPath(cfg.LogDir, "ollama", cfg.LogRetention)
	if err != nil {
		return fmt.Errorf("creating log path: %w", err)
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("creating log file: %w", err)
	}

	cmd := exec.Command(binary, "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "OLLAMA_HOST="+addr, "OLLAMA_KEEP_ALIVE=24h")

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("starting ollama serve: %w", err)
	}
	logFile.Close()

	b.lastPID = cmd.Process.Pid
	b.lastLogFile = logPath
	return nil
}

func (b *Ollama) TryStop(_ string) error {
	binary, err := exec.LookPath("ollama")
	if err != nil {
		return nil
	}
	cmd := exec.Command(binary, "stop")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stopping Ollama: %w", err)
	}

	out, err := exec.Command("pgrep", "-f", "ollama serve").Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to signal PID %d: %v\n", pid, err)
		}
	}
	return nil
}

func (b *Ollama) LastStartedPID() int        { return b.lastPID }
func (b *Ollama) LastStartedLogFile() string  { return b.lastLogFile }

func (b *Ollama) ListRunningModels(addr string) ([]RunningModelInfo, error) {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/api/ps")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing /api/ps response: %w", err)
	}

	models := make([]RunningModelInfo, len(result.Models))
	for i, m := range result.Models {
		models[i] = RunningModelInfo{Name: m.Name, Size: m.Size}
	}
	return models, nil
}
