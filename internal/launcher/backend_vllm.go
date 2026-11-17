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

// VLLM implements LLMServer for vLLM's OpenAI-compatible server. Like
// llama.cpp and Splash it is restarted per profile (ADR-0003): the launcher
// forks `vllm serve <model>` and stops it on switch, so unloading the Model
// means stopping the server.
type VLLM struct {
	apiKeyHolder
}

func init() {
	RegisterLLMServer(&VLLM{})
}

func (b *VLLM) Name() string        { return "vllm" }
func (b *VLLM) DisplayName() string { return "vLLM" }
func (b *VLLM) DefaultAddr() string { return "127.0.0.1:8000" }

// HealthCheck reports a vLLM server as healthy when it answers /health with
// 200 and /version with 200 and a JSON body carrying a non-empty "version"
// string, and its replies do not carry Splash's Server header. vLLM's api-key
// middleware guards only /v1/*, so the probe first does an authed GET of
// /v1/models: a 401 or 403 there wraps ErrAuthFailed (ADR-0010) where
// /health and /version alone would pass with a wrong key. A wildcard addr is
// probed over loopback (probeAddr).
func (b *VLLM) HealthCheck(addr string) error {
	base := "http://" + probeAddr(addr)

	resp, err := authedGet(healthCheckTimeout, base+"/v1/models", b.apiKey())
	if err != nil {
		return err
	}
	resp.Body.Close()
	if err := authFailedErr(resp.StatusCode); err != nil {
		return err
	}

	resp, err = authedGet(healthCheckTimeout, base+"/health", b.apiKey())
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: /health status %d", resp.StatusCode)
	}
	// Splash answers /health with 200 too; its Server header tells it apart.
	if isSplashResponse(resp) {
		return fmt.Errorf("not vllm: /health response carries Server: Splash header")
	}

	return b.checkVersion(base)
}

// checkVersion requires base's /version to answer 200 with a JSON body whose
// "version" field is a non-empty string, which vLLM sends and llama-server,
// LM Studio, Ollama and Splash do not.
func (b *VLLM) checkVersion(base string) error {
	resp, err := authedGet(healthCheckTimeout, base+"/version", b.apiKey())
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(boundedBody(resp.Body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not vllm: /version status %d", resp.StatusCode)
	}
	var version struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(body, &version) != nil || version.Version == "" {
		return fmt.Errorf("not vllm: /version response missing version field")
	}
	return nil
}

// ParamSpecs lists the profile parameters vLLM receives as launch flags:
// context_size (--max-model-len) and parallel (--max-num-seqs). Sampling
// parameters are not passed to vLLM and are therefore not displayed.
func (b *VLLM) ParamSpecs() []ProfileParamSpec {
	return []ProfileParamSpec{specContextSize, specParallel}
}

func (b *VLLM) LoadModel(_ string, _ *ResolvedProfile) error { return nil }
func (b *VLLM) UnloadModel(_ string, _ string) error         { return nil }
func (b *VLLM) TryStart(_ *Config, _ string) error           { return nil }
func (b *VLLM) TryStop(_ string) error                       { return nil }

// ListRunningModels reports the model vLLM is serving from its OpenAI-style
// /v1/models endpoint, dialing a wildcard addr over loopback as HealthCheck
// does.
func (b *VLLM) ListRunningModels(addr string) ([]RunningModelInfo, error) {
	return openAIModelList(probeAddr(addr), b.apiKey())
}

// BuildServerEnv hands the configured api_key to vLLM through VLLM_API_KEY,
// so the credential never shows up in ps output.
func (b *VLLM) BuildServerEnv(cfg *Config, _ *ResolvedProfile) []string {
	key := cfg.APIKeyFor(b.Name())
	if key == "" {
		return nil
	}
	return []string{"VLLM_API_KEY=" + key}
}

func (b *VLLM) ServerBinary(_ *Config) string {
	return "vllm"
}

// BinaryInstallHint explains how to make the `vllm` command reachable when
// the lookup on PATH fails.
func (b *VLLM) BinaryInstallHint() string {
	return "put vLLM's `vllm` command on PATH (for example `uv tool install vllm`), and make sure " +
		"a launchd or MCP-adapter PATH includes its directory"
}

// ResolveModel validates a vLLM model ref and returns it unchanged: either an
// absolute path to a local model that exists, or a Hugging Face `owner/repo`
// id. There is no install check for an id — vLLM downloads the Model itself
// on first start, bounded by startup_max_wait. An empty ref resolves to "".
func (b *VLLM) ResolveModel(_ *Config, modelRef string) (string, error) {
	if modelRef == "" {
		return "", nil
	}
	if filepath.IsAbs(modelRef) {
		if _, err := os.Stat(modelRef); err != nil {
			return "", fmt.Errorf("vllm model path is not usable: %w", err)
		}
		return modelRef, nil
	}
	if err := validateHFRepoID(b.Name(), modelRef); err != nil {
		return "", err
	}
	return modelRef, nil
}

// BuildServerArgs builds `serve <model> [--host H] [--port P]
// [--max-model-len N] [--max-num-seqs N] <extra_args...>`. extra_args come
// last so a user-supplied flag can extend or override the launcher's own.
// The api_key never appears here; BuildServerEnv passes it.
func (b *VLLM) BuildServerArgs(_ *Config, profile *ResolvedProfile) []string {
	args := []string{"serve"}
	params := &profile.ProfileParams

	if profile.ModelPath != "" {
		args = append(args, profile.ModelPath)
	}
	if params.Host != nil {
		args = append(args, "--host", *params.Host)
	}
	if params.Port != nil {
		args = append(args, "--port", strconv.Itoa(*params.Port))
	}
	if params.ContextSize != nil {
		args = append(args, "--max-model-len", strconv.Itoa(*params.ContextSize))
	}
	if params.Parallel != nil {
		args = append(args, "--max-num-seqs", strconv.Itoa(*params.Parallel))
	}

	return append(args, profile.ExtraArgs...)
}
