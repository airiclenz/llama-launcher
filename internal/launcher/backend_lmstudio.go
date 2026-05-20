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
func (b *LMStudio) DisplayName() string { return "LM-Studio" }
func (b *LMStudio) DefaultAddr() string { return "localhost:1234" }

func (b *LMStudio) HealthCheck(addr string) error {
	client := &http.Client{Timeout: healthCheckTimeout}
	base := "http://" + addr

	resp, err := client.Get(base + "/v1/models")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}

	// Exclude llamacpp: its /health returns {"status":"ok"}.
	// LM Studio returns {"error":"..."} for the same path.
	r, err := client.Get(base + "/health")
	if err == nil {
		healthBody, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode == http.StatusOK {
			var h struct{ Status string `json:"status"` }
			if json.Unmarshal(healthBody, &h) == nil && h.Status != "" {
				return fmt.Errorf("not LM Studio: /health body matches llamacpp (status=%q)", h.Status)
			}
		}
	}

	// Exclude Ollama: /api/tags returns {"models":[...]}.
	// LM Studio returns 200 for all paths but with {"error":"..."}.
	r, err = client.Get(base + "/api/tags")
	if err == nil {
		tagsBody, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode == http.StatusOK {
			var tags struct{ Models json.RawMessage `json:"models"` }
			if json.Unmarshal(tagsBody, &tags) == nil && tags.Models != nil {
				return fmt.Errorf("not LM Studio: /api/tags body matches Ollama")
			}
		}
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
	resp, err := (&http.Client{Timeout: modelLoadTimeout}).Post(
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
		return fmt.Errorf("LM Studio unload returned status %d", resp.StatusCode)
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
	if err := exec.Command("lms", "server", "stop").Run(); err != nil {
		return fmt.Errorf("stopping LM Studio server: %w", err)
	}
	return nil
}
