package launcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// LlamaCpp implements LLMServer for llama.cpp's llama-server.
type LlamaCpp struct{}

func init() {
	RegisterLLMServer(&LlamaCpp{})
}

func (b *LlamaCpp) Name() string        { return "llamacpp" }
func (b *LlamaCpp) DisplayName() string { return "LLaMA.cpp" }
func (b *LlamaCpp) DefaultAddr() string { return "127.0.0.1:8080" }

func (b *LlamaCpp) HealthCheck(addr string) error {
	resp, err := (&http.Client{Timeout: healthCheckTimeout}).Get("http://" + addr + "/health")
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	// llama-server returns {"status":"ok"}. LM Studio returns 200 for all
	// paths but with {"error":"..."} — the missing "status" field rejects it.
	var health struct{ Status string `json:"status"` }
	if json.Unmarshal(body, &health) != nil || health.Status == "" {
		return fmt.Errorf("not llamacpp: /health response missing status field")
	}
	return nil
}

func (b *LlamaCpp) LoadModel(_ string, _ *ResolvedProfile) error { return nil }
func (b *LlamaCpp) UnloadModel(_ string, _ string) error         { return nil }
func (b *LlamaCpp) TryStart(_ *Config, _ string) error           { return nil }
func (b *LlamaCpp) TryStop(_ string) error                       { return nil }

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

	args = append(args, profile.ExtraArgs...)

	return args
}
