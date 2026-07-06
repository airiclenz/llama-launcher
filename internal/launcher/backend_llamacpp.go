package launcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// LlamaCpp implements LLMServer for llama.cpp's llama-server.
type LlamaCpp struct {
	apiKey string
}

func init() {
	RegisterLLMServer(&LlamaCpp{})
}

func (b *LlamaCpp) Name() string         { return "llamacpp" }
func (b *LlamaCpp) DisplayName() string  { return "LLaMA.cpp" }
func (b *LlamaCpp) DefaultAddr() string  { return "127.0.0.1:8080" }
func (b *LlamaCpp) setAPIKey(key string) { b.apiKey = key }

func (b *LlamaCpp) HealthCheck(addr string) error {
	resp, err := authedGet(healthCheckTimeout, "http://"+addr+"/health", b.apiKey)
	if err != nil {
		return err
	}
	body, _ := readBodyLimited(resp.Body, maxStatusBodyBytes)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	// llama-server returns {"status":"ok"}. LM Studio returns 200 for all
	// paths but with {"error":"..."} — the missing "status" field rejects it.
	var health struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &health) != nil || health.Status == "" {
		return fmt.Errorf("not llamacpp: /health response missing status field")
	}
	return nil
}

func (b *LlamaCpp) LoadModel(_ string, _ *ResolvedProfile) error { return nil }
func (b *LlamaCpp) UnloadModel(_ string, _ string) error         { return nil }
func (b *LlamaCpp) TryStart(_ *Config, _ string) error           { return nil }
func (b *LlamaCpp) TryStop(_ string) error                       { return nil }

// ListRunningModels reports the model llama-server is currently serving by
// reading /v1/models. The OpenAI-style endpoint returns one entry whose `id`
// is the model path or alias the server was launched with.
func (b *LlamaCpp) ListRunningModels(addr string) ([]RunningModelInfo, error) {
	resp, err := authedGet(healthCheckTimeout, "http://"+addr+"/v1/models", b.apiKey)
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

// QueryLiveParams reads /props on a running llama-server and translates the
// fields llama-server exposes into a ProfileParams. Only fields that /props
// reports are populated; the rest remain nil so paramDrift will skip them.
// /props is available on recent llama.cpp builds; older builds return 404 and
// this function returns (nil, nil), which paramDrift treats as "no drift".
func (b *LlamaCpp) QueryLiveParams(addr string) (*ProfileParams, error) {
	resp, err := authedGet(healthCheckTimeout, "http://"+addr+"/props", b.apiKey)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if err := authFailedErr(resp.StatusCode); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/props returned status %d", resp.StatusCode)
	}
	var raw struct {
		DefaultGenerationSettings struct {
			NCtx          *int     `json:"n_ctx"`
			Temperature   *float64 `json:"temperature"`
			RepeatPenalty *float64 `json:"repeat_penalty"`
			TopK          *int     `json:"top_k"`
			TopP          *float64 `json:"top_p"`
			MinP          *float64 `json:"min_p"`
		} `json:"default_generation_settings"`
		TotalSlots *int   `json:"total_slots"`
		ModelPath  string `json:"model_path"`
	}
	if err := decodeJSONLimited(resp.Body, maxJSONBodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("parsing /props response: %w", err)
	}
	out := &ProfileParams{
		ContextSize:   raw.DefaultGenerationSettings.NCtx,
		Temperature:   raw.DefaultGenerationSettings.Temperature,
		RepeatPenalty: raw.DefaultGenerationSettings.RepeatPenalty,
		TopK:          raw.DefaultGenerationSettings.TopK,
		TopP:          raw.DefaultGenerationSettings.TopP,
		MinP:          raw.DefaultGenerationSettings.MinP,
		Parallel:      raw.TotalSlots,
	}
	return out, nil
}

func (b *LlamaCpp) BuildServerEnv(_ *Config, _ *ResolvedProfile) []string { return nil }

func (b *LlamaCpp) ServerBinary(_ *Config) string {
	return "llama-server"
}

func (b *LlamaCpp) ResolveModel(cfg *Config, modelRef string) (string, error) {
	if modelRef == "" {
		return "", nil
	}

	var path string
	if filepath.IsAbs(modelRef) {
		path = modelRef
	} else if cfg.ModelsDir != "" {
		path = filepath.Join(cfg.ModelsDir, modelRef)
	} else {
		path = modelRef
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("model not found: %s", path)
	}
	if info.IsDir() {
		return "", fmt.Errorf("model path is a directory: %s", path)
	}

	return path, nil
}

func (b *LlamaCpp) BuildServerArgs(cfg *Config, profile *ResolvedProfile) []string {
	var args []string
	params := &profile.ProfileParams

	if profile.ModelPath != "" {
		args = append(args, "--model", profile.ModelPath)
	}

	if params.Host != nil {
		args = append(args, "--host", *params.Host)
	}
	if params.Port != nil {
		args = append(args, "--port", strconv.Itoa(*params.Port))
	}
	if params.GPULayers != nil {
		args = append(args, "-ngl", strconv.Itoa(*params.GPULayers))
	}
	if params.Threads != nil {
		args = append(args, "-t", strconv.Itoa(*params.Threads))
	}
	if params.ThreadsBatch != nil {
		args = append(args, "-tb", strconv.Itoa(*params.ThreadsBatch))
	}
	if params.BatchSize != nil {
		args = append(args, "-b", strconv.Itoa(*params.BatchSize))
	}
	if params.ContextSize != nil {
		args = append(args, "-c", strconv.Itoa(*params.ContextSize))
	}
	if params.FlashAttn != nil {
		if *params.FlashAttn {
			args = append(args, "-fa", "on")
		} else {
			args = append(args, "-fa", "off")
		}
	}
	if params.ContBatching != nil && *params.ContBatching {
		args = append(args, "-cb")
	}
	if params.Parallel != nil {
		args = append(args, "-np", strconv.Itoa(*params.Parallel))
	}
	if params.Mlock != nil && *params.Mlock {
		args = append(args, "--mlock")
	}
	if params.NoMmap != nil && *params.NoMmap {
		args = append(args, "--no-mmap")
	}
	if params.Embedding != nil && *params.Embedding {
		args = append(args, "--embedding")
	}
	if params.Jinja != nil && *params.Jinja {
		args = append(args, "--jinja")
	}
	if params.Temperature != nil {
		args = append(args, "--temp", formatFloatArg(*params.Temperature))
	}
	if params.RepeatPenalty != nil {
		args = append(args, "--repeat-penalty", formatFloatArg(*params.RepeatPenalty))
	}
	if params.TopK != nil {
		args = append(args, "--top-k", strconv.Itoa(*params.TopK))
	}
	if params.TopP != nil {
		args = append(args, "--top-p", formatFloatArg(*params.TopP))
	}
	if params.MinP != nil {
		args = append(args, "--min-p", formatFloatArg(*params.MinP))
	}

	// Placed before extra_args so a user-supplied --api-key override wins
	// (llama-server uses the last occurrence of a repeated flag).
	if key := cfg.APIKeyFor(b.Name()); key != "" {
		args = append(args, "--api-key", key)
	}

	args = append(args, profile.ExtraArgs...)

	return args
}

// formatFloatArg renders a float CLI argument value in its shortest form that
// round-trips exactly (e.g. 0.7 -> "0.7").
func formatFloatArg(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
