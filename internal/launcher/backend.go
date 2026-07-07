package launcher

import (
	"fmt"
	"sort"
	"time"
)

const (
	healthCheckTimeout = 2 * time.Second
	modelLoadTimeout   = 5 * time.Minute
)

// LLMServer abstracts an LLM-serving software implementation (llama.cpp,
// Ollama, LM Studio, ...). See CONTEXT.md for the domain term.
type LLMServer interface {
	Name() string
	DisplayName() string
	DefaultAddr() string
	HealthCheck(addr string) error
	ResolveModel(cfg *Config, modelRef string) (string, error)
	LoadModel(addr string, profile *ResolvedProfile) error
	UnloadModel(addr string, modelID string) error
	TryStart(cfg *Config, addr string) error
	TryStop(addr string) error
}

// apiKeyConfigurable is implemented by LLM Servers that accept a per-server
// API key for the launcher's own HTTP calls (and, for managed servers, for
// enforcing client auth at launch).
type apiKeyConfigurable interface {
	setAPIKey(key string)
}

// applyAPIKeys pushes the configured per-server API keys onto the registered
// backends. Keys are applied unconditionally — including the empty string —
// so removing a key from the config takes effect on reload. LoadConfig can run
// concurrently with in-flight discovery probes (and with another LoadConfig),
// so each backend's setAPIKey guards the field with its own mutex; readers use
// the matching getter.
func applyAPIKeys(cfg *Config) {
	for name, b := range llmServers {
		if configurable, ok := b.(apiKeyConfigurable); ok {
			configurable.setAPIKey(cfg.APIKeyFor(name))
		}
	}
}

// ManagedLLMServer is implemented by LLM Servers where the launcher forks and
// owns the server process (e.g. llama.cpp).
type ManagedLLMServer interface {
	LLMServer
	ServerBinary(cfg *Config) string
	BuildServerArgs(cfg *Config, profile *ResolvedProfile) []string
	BuildServerEnv(cfg *Config, profile *ResolvedProfile) []string
}

// PIDTracker is implemented by LLM Servers that track the PID of a server
// process they auto-started via TryStart.
type PIDTracker interface {
	LastStartedPID() int
	LastStartedLogFile() string
}

// ModelLister is implemented by LLM Servers that can list currently loaded models.
type ModelLister interface {
	ListRunningModels(addr string) ([]RunningModelInfo, error)
}

type RunningModelInfo struct {
	Name string
	Size int64
}

// LiveParamsQuerier is implemented by LLM Servers that can report their
// currently active parameters at runtime. Used by ADR-0007 drift detection
// (LoadProfile compares the live params against the freshly resolved profile).
// Returning (nil, nil) means the server is reachable but exposes no params.
type LiveParamsQuerier interface {
	QueryLiveParams(addr string) (*ProfileParams, error)
}

// ResolvedProfile holds a fully merged profile ready for use by a backend.
type ResolvedProfile struct {
	Name        string
	Title       string
	Description string
	ModelRef    string // original model reference from config (e.g. relative path, identifier)
	ModelPath   string // absolute resolved path on disk
	Backend     string
	ExtraArgs   []string
	ProfileParams
}

// DisplayName returns the label shown wherever the profile is presented to
// the user: the optional title when set, otherwise the profile name.
func (p *ResolvedProfile) DisplayName() string {
	if p.Title != "" {
		return p.Title
	}
	return p.Name
}

var llmServers = map[string]LLMServer{}

// RegisterLLMServer adds an LLM Server to the global registry. Panics on duplicate names.
func RegisterLLMServer(b LLMServer) {
	name := b.Name()
	if _, exists := llmServers[name]; exists {
		panic("duplicate LLM server: " + name)
	}
	llmServers[name] = b
}

// GetLLMServer returns the LLM Server registered under the given name.
func GetLLMServer(name string) (LLMServer, error) {
	b, ok := llmServers[name]
	if !ok {
		names := make([]string, 0, len(llmServers))
		for k := range llmServers {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown LLM server %q (available: %v)", name, names)
	}
	return b, nil
}
