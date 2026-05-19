package launcher

import (
	"fmt"
	"sort"
)

// Backend abstracts an LLM server implementation. Each backend knows how to
// start its server process, build CLI arguments, and resolve model paths.
type Backend interface {
	// Name returns the identifier used in config files (e.g. "llamacpp").
	Name() string
	// ServerBinary returns the path to the server executable.
	ServerBinary(cfg *Config) string
	// BuildServerArgs assembles CLI flags for starting the server process.
	BuildServerArgs(cfg *Config, profile *ResolvedProfile) []string
	// ResolveModel validates and resolves a model reference to an absolute path.
	ResolveModel(cfg *Config, modelRef string) (string, error)
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
