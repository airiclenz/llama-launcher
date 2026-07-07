package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
)

type Ollama struct {
	lastPID     int
	lastLogFile string
	apiKey      string
}

func init() {
	RegisterLLMServer(&Ollama{})
}

func (b *Ollama) Name() string         { return "ollama" }
func (b *Ollama) DisplayName() string  { return "Ollama" }
func (b *Ollama) DefaultAddr() string  { return "localhost:11434" }
func (b *Ollama) setAPIKey(key string) { b.apiKey = key }

func (b *Ollama) HealthCheck(addr string) error {
	resp, err := authedGet(healthCheckTimeout, "http://"+addr+"/", b.apiKey)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := readBodyLimited(resp.Body, maxStatusBodyBytes)
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
	resp, err := authedPostJSON(modelLoadTimeout, "http://"+addr+"/api/generate", b.apiKey, body)
	if err != nil {
		return fmt.Errorf("loading model via Ollama API: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxStatusBodyBytes))
	if err := authFailedErr(resp.StatusCode); err != nil {
		return err
	}
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
	resp, err := authedPostJSON(30*time.Second, "http://"+addr+"/api/generate", b.apiKey, body)
	if err != nil {
		return fmt.Errorf("unloading model via Ollama API: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxStatusBodyBytes))
	if err := authFailedErr(resp.StatusCode); err != nil {
		return err
	}
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

	logPath, err := createLogPath(cfg, "ollama")
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

// TryStop is intentionally a no-op for Ollama. The launcher stops the specific
// instance by signalling whatever process is listening at addr — the
// address-scoped lsof/PID path in EnsureStopped, which already handles the
// listener. It deliberately does not shell out to `ollama stop <model>` (that
// only unloads a model from a still-running `ollama serve`, so it would not
// make the listener go away, and the subcommand is absent on older ollama
// versions), and it never sweeps every `ollama serve` on the host by PID —
// that would kill unrelated instances regardless of addr.
func (b *Ollama) TryStop(_ string) error { return nil }

func (b *Ollama) LastStartedPID() int        { return b.lastPID }
func (b *Ollama) LastStartedLogFile() string { return b.lastLogFile }

func (b *Ollama) ListRunningModels(addr string) ([]RunningModelInfo, error) {
	resp, err := authedGet(5*time.Second, "http://"+addr+"/api/ps", b.apiKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := authFailedErr(resp.StatusCode); err != nil {
		return nil, err
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"models"`
	}
	if err := decodeJSONLimited(resp.Body, maxJSONBodyBytes, &result); err != nil {
		return nil, fmt.Errorf("parsing /api/ps response: %w", err)
	}

	models := make([]RunningModelInfo, len(result.Models))
	for i, m := range result.Models {
		models[i] = RunningModelInfo{Name: m.Name, Size: m.Size}
	}
	return models, nil
}
