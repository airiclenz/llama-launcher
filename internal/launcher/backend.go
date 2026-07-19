package launcher

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
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
	// ParamSpecs returns the ordered display specs for the profile
	// parameters this LLM Server actually applies when loading a model or
	// launching a server. The "Show model config" pop-up renders profile
	// parameters exclusively from this list, so a parameter absent here is
	// never displayed (a param the server never receives must not be shown).
	ParamSpecs() []ProfileParamSpec
}

// ProfileParamSpec describes how one profile parameter is displayed: the
// label shown in the profile pop-up and a formatter rendering the value from
// a ProfileParams. Format reports ok=false when the field is unset so the
// renderer skips the line entirely. Each LLM Server owns the list of specs
// it honours (LLMServer.ParamSpecs); the shared spec* values below keep
// labels and value formatting identical for parameters that more than one
// backend supports.
type ProfileParamSpec struct {
	Label  string
	Format func(p *ProfileParams) (value string, ok bool)
}

// intParamSpec builds a ProfileParamSpec for an optional integer field.
func intParamSpec(label string, field func(*ProfileParams) *int) ProfileParamSpec {
	return ProfileParamSpec{Label: label, Format: func(p *ProfileParams) (string, bool) {
		v := field(p)
		if v == nil {
			return "", false
		}
		return strconv.Itoa(*v), true
	}}
}

// boolParamSpec builds a ProfileParamSpec for an optional boolean field.
func boolParamSpec(label string, field func(*ProfileParams) *bool) ProfileParamSpec {
	return ProfileParamSpec{Label: label, Format: func(p *ProfileParams) (string, bool) {
		v := field(p)
		if v == nil {
			return "", false
		}
		return strconv.FormatBool(*v), true
	}}
}

// floatParamSpec builds a ProfileParamSpec for an optional float field.
func floatParamSpec(label string, field func(*ProfileParams) *float64) ProfileParamSpec {
	return ProfileParamSpec{Label: label, Format: func(p *ProfileParams) (string, bool) {
		v := field(p)
		if v == nil {
			return "", false
		}
		return formatFloatParam(*v), true
	}}
}

// formatFloatParam renders a float parameter with the shortest decimal
// representation that round-trips (0.7 stays "0.7", not "0.700000"). Shared
// by the display specs and llamacpp's launch-flag assembly so the pop-up
// shows exactly the value the server receives.
func formatFloatParam(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Display specs for the cross-backend profile parameters. Backends assemble
// their ParamSpecs lists from these so a parameter honoured by several
// backends keeps a single label and formatting.
var (
	specContextSize  = intParamSpec("Context size", func(p *ProfileParams) *int { return p.ContextSize })
	specGPULayers    = intParamSpec("GPU layers", func(p *ProfileParams) *int { return p.GPULayers })
	specThreads      = intParamSpec("Threads", func(p *ProfileParams) *int { return p.Threads })
	specThreadsBatch = intParamSpec("Threads (batch)", func(p *ProfileParams) *int { return p.ThreadsBatch })
	specBatchSize    = intParamSpec("Batch size", func(p *ProfileParams) *int { return p.BatchSize })
	specFlashAttn    = boolParamSpec("Flash attention", func(p *ProfileParams) *bool { return p.FlashAttn })
	specContBatching = boolParamSpec("Cont. batching", func(p *ProfileParams) *bool { return p.ContBatching })
	specParallel     = intParamSpec("Parallel", func(p *ProfileParams) *int { return p.Parallel })
	specMlock        = boolParamSpec("Mlock", func(p *ProfileParams) *bool { return p.Mlock })
	specNoMmap       = boolParamSpec("No mmap", func(p *ProfileParams) *bool { return p.NoMmap })
	specEmbedding    = boolParamSpec("Embedding", func(p *ProfileParams) *bool { return p.Embedding })
	specJinja        = boolParamSpec("Jinja", func(p *ProfileParams) *bool { return p.Jinja })

	specTemperature   = floatParamSpec("Temperature", func(p *ProfileParams) *float64 { return p.Temperature })
	specRepeatPenalty = floatParamSpec("Repeat penalty", func(p *ProfileParams) *float64 { return p.RepeatPenalty })
	specTopK          = intParamSpec("Top-k", func(p *ProfileParams) *int { return p.TopK })
	specTopP          = floatParamSpec("Top-p", func(p *ProfileParams) *float64 { return p.TopP })
	specMinP          = floatParamSpec("Min-p", func(p *ProfileParams) *float64 { return p.MinP })
)

// apiKeyConfigurable is implemented by LLM Servers that accept a per-server
// API key for the launcher's own HTTP calls (and, for managed servers, for
// enforcing client auth at launch).
type apiKeyConfigurable interface {
	setAPIKey(key string)
}

// apiKeyHolder stores a backend's per-server API key behind a RWMutex so a
// config load on one goroutine can replace the key while parallel discovery
// probes (HealthCheck and friends) read it. Backends embed it; the embedding
// provides the synchronized accessors and implements apiKeyConfigurable.
type apiKeyHolder struct {
	mu  sync.RWMutex
	key string
}

// setAPIKey replaces the stored API key.
func (h *apiKeyHolder) setAPIKey(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.key = key
}

// apiKey returns the currently stored API key.
func (h *apiKeyHolder) apiKey() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.key
}

// applyAPIKeys pushes the configured per-server API keys onto the registered
// backends. Keys are applied unconditionally — including the empty string —
// so removing a key from the config takes effect on reload. Called from
// LoadConfig (and thus on every Reload); the apiKeyHolder embedded in each
// backend synchronizes the update against probe goroutines that may still be
// reading the previous key.
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
// Fields the server does not report must stay nil — drift detection skips
// them rather than treating them as "unset".
type LiveParamsQuerier interface {
	QueryLiveParams(addr string) (*ProfileParams, error)
}

// StartupProber is implemented by LLM Servers that can tell a server that
// is reachable but still starting up (e.g. llama-server answering /health
// with 503 while it loads its model) apart from one that is not running
// at all. The managed start path uses it to refuse spawning a duplicate
// server onto an address where an earlier start is still coming up
// (TDD §6.2).
type StartupProber interface {
	StartingUp(addr string) bool
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
