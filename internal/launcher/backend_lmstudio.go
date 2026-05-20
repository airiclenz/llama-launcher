package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type LMStudio struct{}

func init() {
	RegisterBackend(&LMStudio{})
}

func (b *LMStudio) Name() string        { return "lmstudio" }
func (b *LMStudio) DisplayName() string { return "LM Studio" }
func (b *LMStudio) DefaultAddr() string { return "localhost:1234" }

func (b *LMStudio) HealthCheck(addr string) error {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + addr + "/v1/models")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

func (b *LMStudio) ResolveModel(_ *Config, modelRef string) (string, error) {
	if modelRef == "" {
		return "", nil
	}
	return modelRef, nil
}

func (b *LMStudio) LoadModel(addr string, profile *ResolvedProfile) error {
	payload := map[string]interface{}{
		"model": profile.ModelPath,
	}
	if profile.ContextSize != nil {
		payload["context_length"] = *profile.ContextSize
	}

	body, _ := json.Marshal(payload)
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Post(
		"http://"+addr+"/api/v1/models/load",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("loading model via LM Studio API: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := extractLMStudioError(respBody)
		if msg != "" {
			return fmt.Errorf("LM Studio: %s", msg)
		}
		return fmt.Errorf("LM Studio load returned status %d", resp.StatusCode)
	}
	return nil
}

func extractLMStudioError(body []byte) string {
	var result struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &result) == nil && result.Error.Message != "" {
		return result.Error.Message
	}
	return ""
}

func (b *LMStudio) UnloadModel(addr string, modelID string) error {
	payload := map[string]interface{}{
		"identifier": modelID,
	}
	body, _ := json.Marshal(payload)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Post(
		"http://"+addr+"/api/v1/models/unload",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("unloading model via LM Studio API: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		msg := extractLMStudioError(respBody)
		if msg != "" {
			return fmt.Errorf("LM Studio: %s", msg)
		}
	}
	return nil
}

func (b *LMStudio) TryStart(_ *Config, addr string) error {
	if _, err := exec.LookPath("lms"); err != nil {
		return fmt.Errorf("lms CLI not found in PATH")
	}

	args := []string{"server", "start"}
	_, portStr, ok := strings.Cut(addr, ":")
	if ok {
		if _, err := strconv.Atoi(portStr); err == nil {
			args = append(args, "--port", portStr)
		}
	}

	cmd := exec.Command("lms", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("starting LM Studio server: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (b *LMStudio) TryStop(addr string) error {
	if _, err := exec.LookPath("lms"); err != nil {
		return nil
	}
	exec.Command("lms", "server", "stop").Run()
	return nil
}
