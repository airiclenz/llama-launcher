package launcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type LMStudio struct {
	apiKeyHolder
}

func init() {
	RegisterLLMServer(&LMStudio{})
}

func (b *LMStudio) Name() string        { return "lmstudio" }
func (b *LMStudio) DisplayName() string { return "LM-Studio" }
func (b *LMStudio) DefaultAddr() string { return "localhost:1234" }

func (b *LMStudio) HealthCheck(addr string) error {
	base := "http://" + addr

	resp, err := authedGet(healthCheckTimeout, base+"/v1/models", b.apiKey())
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
	r, err := authedGet(healthCheckTimeout, base+"/health", b.apiKey())
	if err == nil {
		healthBody, _ := io.ReadAll(boundedBody(r.Body))
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
	r, err = authedGet(healthCheckTimeout, base+"/api/tags", b.apiKey())
	if err == nil {
		tagsBody, _ := io.ReadAll(boundedBody(r.Body))
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

// LoadModel loads a Model via LM Studio's REST API (POST /api/v1/models/load).
// Only parameters that endpoint accepts (verified against LM Studio 0.4.15 and
// its REST docs) are sent: context_size maps to context_length, batch_size to
// eval_batch_size, flash_attn to flash_attention, and parallel passes through
// under the same name. gpu_layers has no REST-API equivalent and is
// deliberately not sent — LM Studio decides GPU offload itself.
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
	if profile.Parallel != nil {
		payload["parallel"] = *profile.Parallel
	}

	body, _ := json.Marshal(payload)
	resp, err := authedPostJSON(modelLoadTimeout, "http://"+addr+"/api/v1/models/load", b.apiKey(), body)
	if err != nil {
		return fmt.Errorf("loading model via LM Studio API: %w", err)
	}
	return expectOK(resp, lmStudioStatusErr("load"))
}

// ParamSpecs lists, in display order, the parameters LoadModel sends to LM
// Studio's load endpoint: context_size, batch_size (as eval_batch_size),
// flash_attn (as flash_attention), and parallel. gpu_layers is deliberately
// absent — the REST API has no GPU-offload field (see LoadModel).
func (b *LMStudio) ParamSpecs() []ProfileParamSpec {
	return []ProfileParamSpec{
		specContextSize,
		specBatchSize,
		specFlashAttn,
		specParallel,
	}
}

// lmStudioStatusErr returns the non-200 status mapper for LM Studio's model
// load/unload endpoints: it prefers the server-reported error message and
// falls back to a generic "<verb> returned status" error.
func lmStudioStatusErr(verb string) func(statusCode int, body []byte) error {
	return func(statusCode int, body []byte) error {
		if msg := extractLMStudioError(body); msg != "" {
			return fmt.Errorf("LM Studio: %s", msg)
		}
		return fmt.Errorf("LM Studio %s returned status %d", verb, statusCode)
	}
}

// extractLMStudioError pulls the human-readable message out of an LM Studio
// error response body. The message is server-reported and ends up printed to
// the terminal, so control characters are stripped (sanitizeServerString).
func extractLMStudioError(body []byte) string {
	var result struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &result) == nil && result.Error.Message != "" {
		return sanitizeServerString(result.Error.Message)
	}
	return ""
}

func (b *LMStudio) UnloadModel(addr string, modelID string) error {
	payload := map[string]interface{}{
		"identifier": modelID,
	}
	body, _ := json.Marshal(payload)
	resp, err := authedPostJSON(30*time.Second, "http://"+addr+"/api/v1/models/unload", b.apiKey(), body)
	if err != nil {
		return fmt.Errorf("unloading model via LM Studio API: %w", err)
	}
	return expectOK(resp, lmStudioStatusErr("unload"))
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
	return openAIModelList(addr, b.apiKey())
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
