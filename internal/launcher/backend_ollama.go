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

type Ollama struct{}

func init() {
	RegisterBackend(&Ollama{})
}

func (b *Ollama) Name() string        { return "ollama" }
func (b *Ollama) DisplayName() string { return "Ollama" }
func (b *Ollama) DefaultAddr() string { return "localhost:11434" }

func (b *Ollama) HealthCheck(addr string) error {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + addr + "/")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	if len(body) > 0 && !bytes.Contains(body, []byte("Ollama")) {
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
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Post(
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
	return nil
}

func (b *Ollama) TryStart(_ *Config, addr string) error {
	binary, err := exec.LookPath("ollama")
	if err != nil {
		return fmt.Errorf("ollama binary not found in PATH")
	}

	cmd := exec.Command(binary, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "OLLAMA_HOST="+addr, "OLLAMA_KEEP_ALIVE=24h")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ollama serve: %w", err)
	}
	return nil
}

func (b *Ollama) TryStop(_ string) error {
	return nil
}
