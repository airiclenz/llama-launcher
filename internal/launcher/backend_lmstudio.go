package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LMStudio struct {
	// mu guards apiKey: applyAPIKeys/setAPIKey writes it on the config-load
	// goroutine while probe goroutines read it via HealthCheck and
	// ListRunningModels.
	mu     sync.RWMutex
	apiKey string
}

func init() {
	RegisterLLMServer(&LMStudio{})
}

func (b *LMStudio) Name() string        { return "lmstudio" }
func (b *LMStudio) DisplayName() string { return "LM-Studio" }
func (b *LMStudio) DefaultAddr() string { return "localhost:1234" }

func (b *LMStudio) setAPIKey(key string) {
	b.mu.Lock()
	b.apiKey = key
	b.mu.Unlock()
}

func (b *LMStudio) getAPIKey() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.apiKey
}

func (b *LMStudio) HealthCheck(addr string) error {
	base := "http://" + addr
	key := b.getAPIKey()

	resp, err := authedGet(healthCheckTimeout, base+"/v1/models", key)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if err := authFailedErr(resp.StatusCode); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}

	// Exclude llamacpp: its /health returns {"status":"ok"}.
	// LM Studio returns {"error":"..."} for the same path.
	r, err := authedGet(healthCheckTimeout, base+"/health", key)
	if err == nil {
		healthBody, _ := readBodyLimited(r.Body, maxStatusBodyBytes)
		r.Body.Close()
		if r.StatusCode == http.StatusOK {
			var h struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(healthBody, &h) == nil && h.Status != "" {
				return fmt.Errorf("not LM Studio: /health body matches llamacpp (status=%q)", h.Status)
			}
		}
	}

	// Exclude Ollama: /api/tags returns {"models":[...]}.
	// LM Studio returns 200 for all paths but with {"error":"..."}.
	r, err = authedGet(healthCheckTimeout, base+"/api/tags", key)
	if err == nil {
		tagsBody, _ := readBodyLimited(r.Body, maxStatusBodyBytes)
		r.Body.Close()
		if r.StatusCode == http.StatusOK {
			var tags struct {
				Models json.RawMessage `json:"models"`
			}
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

// LoadModel loads the Profile's model via LM Studio's REST API
// (POST /api/v1/models/load), forwarding the profile parameters that
// endpoint accepts: context_size → context_length, batch_size →
// eval_batch_size, flash_attn → flash_attention. The endpoint has no
// GPU-offload field, so gpu_layers is intentionally not sent.
func (b *LMStudio) LoadModel(addr string, profile *ResolvedProfile) error {
	payload := map[string]interface{}{
		"model": profile.ModelPath,
	}
	if profile.ContextSize != nil {
		payload["context_length"] = *profile.ContextSize
	}
	if profile.BatchSize != nil {
		payload["eval_batch_size"] = *profile.BatchSize
	}
	if profile.FlashAttn != nil {
		payload["flash_attention"] = *profile.FlashAttn
	}

	body, _ := json.Marshal(payload)
	resp, err := authedPostJSON(modelLoadTimeout, "http://"+addr+"/api/v1/models/load", b.getAPIKey(), body)
	if err != nil {
		return fmt.Errorf("loading model via LM Studio API: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := readBodyLimited(resp.Body, maxStatusBodyBytes)
	if err := authFailedErr(resp.StatusCode); err != nil {
		return err
	}
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
	resp, err := authedPostJSON(30*time.Second, "http://"+addr+"/api/v1/models/unload", b.getAPIKey(), body)
	if err != nil {
		return fmt.Errorf("unloading model via LM Studio API: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := readBodyLimited(resp.Body, maxStatusBodyBytes)
	if err := authFailedErr(resp.StatusCode); err != nil {
		return err
	}
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

// ListRunningModels reports models LM Studio currently has loaded by reading
// the OpenAI-compatible /v1/models endpoint. LM Studio omits unloaded models
// from this list (in contrast to its /api/v0/models, which also lists them).
func (b *LMStudio) ListRunningModels(addr string) ([]RunningModelInfo, error) {
	resp, err := authedGet(healthCheckTimeout, "http://"+addr+"/v1/models", b.getAPIKey())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := authFailedErr(resp.StatusCode); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/v1/models returned status %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := decodeJSONLimited(resp.Body, maxJSONBodyBytes, &result); err != nil {
		return nil, fmt.Errorf("parsing /v1/models response: %w", err)
	}
	models := make([]RunningModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, RunningModelInfo{Name: m.ID})
	}
	return models, nil
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
