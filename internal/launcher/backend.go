package launcher

import (
	"fmt"
	"sort"
)

// Backend abstracts an LLM server implementation.
type Backend interface {
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

// ManagedBackend is implemented by backends where the launcher forks and owns
// the server process (e.g. llama.cpp, optionally Ollama).
type ManagedBackend interface {
	Backend
	ServerBinary(cfg *Config) string
	BuildServerArgs(cfg *Config, profile *ResolvedProfile) []string
	BuildServerEnv(cfg *Config, profile *ResolvedProfile) []string
}

// ResolvedProfile holds a fully merged profile ready for use by a backend.
type ResolvedProfile struct {
	Name        string
	Description string
	ModelRef    string // original model reference from config (e.g. relative path, identifier)
	ModelPath   string // absolute resolved path on disk
	Backend     string
	ExtraArgs   []string
	ProfileParams
}

var backends = map[string]Backend{}

// RegisterBackend adds a backend to the global registry. Panics on duplicate names.
func RegisterBackend(b Backend) {
	name := b.Name()
	if _, exists := backends[name]; exists {
		panic("duplicate backend: " + name)
	}
	backends[name] = b
}

// GetBackend returns the backend registered under the given name.
func GetBackend(name string) (Backend, error) {
	b, ok := backends[name]
	if !ok {
		names := make([]string, 0, len(backends))
		for k := range backends {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown backend %q (available: %v)", name, names)
	}
	return b, nil
}
