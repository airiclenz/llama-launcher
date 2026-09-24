package launcher

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Splash implements LLMServer for Splash, an MLX-based OpenAI-compatible
// server. Like llama.cpp it is restarted per profile (ADR-0003): the launcher
// forks `splash serve` with the profile's model and stops it on switch.
type Splash struct {
	apiKeyHolder
}

func init() {
	RegisterLLMServer(&Splash{})
}

// splashServerHeader is the value prefix Splash sends in the Server response
// header on every reply. It is the discriminator that tells a Splash server
// apart from another OpenAI-compatible server on the same port.
const splashServerHeader = "Splash"

// splashMaxRepoLen caps the repo part of an owner/repo model ref, mirroring
// Splash's own REPO_ID rule (install/models.py validate_repo_id).
const splashMaxRepoLen = 96

func (b *Splash) Name() string        { return "splash" }
func (b *Splash) DisplayName() string { return "Splash" }
func (b *Splash) DefaultAddr() string { return "127.0.0.1:8000" }

// HealthCheck reports a Splash server as healthy only once it serves
// requests. Splash's /health answers 200 even while the model is still
// loading, so the probe reads /ready instead (200 once serving, 503 before),
// and it requires the `Server: Splash` header so a foreign server answering
// 200 on the same path is not mistaken for Splash.
func (b *Splash) HealthCheck(addr string) error {
	resp, err := authedGet(healthCheckTimeout, "http://"+addr+"/ready", b.apiKey())
	if err != nil {
		return err
	}
	resp.Body.Close()
	if err := authFailedErr(resp.StatusCode); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: /ready status %d", resp.StatusCode)
	}
	if !isSplashResponse(resp) {
		return fmt.Errorf("not splash: /ready response missing Server: Splash header")
	}
	return nil
}

// StartingUp reports whether a Splash server is reachable at addr but still
// loading its model: /ready answers 503 with the Splash Server header until
// the model is served. A connection error, any other status, or a 503 from a
// foreign server all return false.
func (b *Splash) StartingUp(addr string) bool {
	resp, err := authedGet(healthCheckTimeout, "http://"+addr+"/ready", b.apiKey())
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusServiceUnavailable && isSplashResponse(resp)
}

// isSplashResponse reports whether resp carries Splash's Server header.
func isSplashResponse(resp *http.Response) bool {
	return strings.HasPrefix(resp.Header.Get("Server"), splashServerHeader)
}

// ParamSpecs lists the one profile parameter Splash receives as a launch
// flag: context_size, passed as --max-context. Sampling parameters are not
// passed to Splash and are therefore not displayed.
func (b *Splash) ParamSpecs() []ProfileParamSpec {
	return []ProfileParamSpec{specContextSize}
}

func (b *Splash) LoadModel(_ string, _ *ResolvedProfile) error { return nil }
func (b *Splash) UnloadModel(_ string, _ string) error         { return nil }
func (b *Splash) TryStart(_ *Config, _ string) error           { return nil }
func (b *Splash) TryStop(_ string) error                       { return nil }

// ListRunningModels reports the model Splash is serving from its
// OpenAI-style /v1/models endpoint.
func (b *Splash) ListRunningModels(addr string) ([]RunningModelInfo, error) {
	return openAIModelList(addr, b.apiKey())
}

// BuildServerEnv hands the configured api_key to Splash through
// SPLASH_API_KEY, so the credential never shows up in ps output.
func (b *Splash) BuildServerEnv(cfg *Config, _ *ResolvedProfile) []string {
	key := cfg.APIKeyFor(b.Name())
	if key == "" {
		return nil
	}
	return []string{"SPLASH_API_KEY=" + key}
}

func (b *Splash) ServerBinary(_ *Config) string {
	return "splash"
}

// BinaryInstallHint explains how to make the `splash` command reachable when
// the lookup on PATH fails.
func (b *Splash) BinaryInstallHint() string {
	return "put Splash's `splash` command on PATH; for a source checkout use a wrapper script " +
		"that execs <checkout>/splash (a plain symlink breaks it), and make sure a launchd or " +
		"MCP-adapter PATH includes the wrapper's directory"
}

// ResolveModel validates a Splash model ref — a Hugging Face `owner/repo`
// id — and returns it unchanged. An empty ref resolves to "".
func (b *Splash) ResolveModel(_ *Config, modelRef string) (string, error) {
	if modelRef == "" {
		return "", nil
	}
	if err := validateSplashRepoID(modelRef); err != nil {
		return "", err
	}
	return modelRef, nil
}

// validateSplashRepoID mirrors Splash's REPO_ID rule (install/models.py
// validate_repo_id): exactly one `/`, both parts built from [A-Za-z0-9._-]
// with first and last characters from [A-Za-z0-9_], a repo of at most 96
// characters, and no `--`, `..` or trailing `.git`. Refusing here keeps refs
// like `../..` from escaping the model path and gives a clear error instead
// of Splash dying on argparse at start.
func validateSplashRepoID(ref string) error {
	invalid := fmt.Errorf("invalid splash model %q: want a Hugging Face id owner/repo", ref)
	owner, repo, ok := strings.Cut(ref, "/")
	if !ok || strings.Contains(repo, "/") {
		return invalid
	}
	if !isSplashRepoIDPart(owner) || !isSplashRepoIDPart(repo) || len(repo) > splashMaxRepoLen {
		return invalid
	}
	if strings.Contains(ref, "--") || strings.Contains(ref, "..") || strings.HasSuffix(ref, ".git") {
		return invalid
	}
	return nil
}

// isSplashRepoIDPart reports whether part is a non-empty run of
// [A-Za-z0-9._-] whose first and last characters are [A-Za-z0-9_].
func isSplashRepoIDPart(part string) bool {
	if part == "" {
		return false
	}
	for i := 0; i < len(part); i++ {
		c := part[i]
		edge := i == 0 || i == len(part)-1
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		case (c == '.' || c == '-') && !edge:
		default:
			return false
		}
	}
	return true
}

// BuildServerArgs builds `serve --model <ref> [--host H] [--port P]
// [--max-context N] <extra_args...>`. extra_args come last so a user-supplied
// flag (reasoning effort, kv format, max memory, allowed host) can extend or
// override the launcher's own.
func (b *Splash) BuildServerArgs(_ *Config, profile *ResolvedProfile) []string {
	args := []string{"serve"}
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
	if params.ContextSize != nil {
		args = append(args, "--max-context", strconv.Itoa(*params.ContextSize))
	}

	return append(args, profile.ExtraArgs...)
}
