package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/airiclenz/llama-launcher/internal/launcher/defaults"
)

const (
	defaultHost      = "127.0.0.1"
	defaultPort      = 8080
	defaultConfigDir = ".config/llama-launcher"
	configFileName = "config.yaml"
)

// ErrConfigNotFound indicates the configuration file does not exist.
var ErrConfigNotFound = errors.New("config file not found")

// Config represents the top-level YAML configuration file.
type Config struct {
	Servers         map[string]bool    `yaml:"servers"`
	DefaultBackend  string             `yaml:"default_backend"` // deprecated: use defaults.server
	Endpoints       map[string]string  `yaml:"endpoints"`       // deprecated: merge into servers
	ModelsDir       string             `yaml:"models_dir"`
	LogDir          string             `yaml:"log_dir"`
	AutoClose       *bool              `yaml:"auto_close"`
	AutoStopServer  *bool              `yaml:"auto_stop_server"`
	AutoUnload      *bool              `yaml:"auto_unload"`
	DisplayCentered *bool              `yaml:"display_centered"`
	Defaults        ProfileParams      `yaml:"defaults"`
	Profiles        map[string]Profile `yaml:"profiles"`

	ConfigPath string `yaml:"-"`
}

func (c *Config) ShouldAutoClose() bool {
	return c.AutoClose == nil || *c.AutoClose
}

func (c *Config) ShouldAutoStopServer() bool {
	return c.AutoStopServer == nil || *c.AutoStopServer
}

func (c *Config) ShouldAutoUnload() bool {
	return c.AutoUnload == nil || *c.AutoUnload
}

func (c *Config) ShouldDisplayCentered() bool {
	return c.DisplayCentered != nil && *c.DisplayCentered
}

func (c *Config) IsServerEnabled(name string) bool {
	return c.Servers[name]
}

// Profile represents a named model configuration within the YAML config.
type Profile struct {
	Description   string   `yaml:"description"`
	Model         string   `yaml:"model"`
	Backend       string   `yaml:"backend"`
	ExtraArgs     []string `yaml:"extra_args"`
	ProfileParams `yaml:",inline"`
}

// ProfileParams contains tunable server parameters. Pointer types distinguish
// "not set" from zero values, enabling three-tier merge (profile -> defaults -> fallback).
type ProfileParams struct {
	Server        *string  `yaml:"server,omitempty"`
	GPULayers     *int     `yaml:"gpu_layers,omitempty"`
	Threads       *int     `yaml:"threads,omitempty"`
	ThreadsBatch  *int     `yaml:"threads_batch,omitempty"`
	BatchSize     *int     `yaml:"batch_size,omitempty"`
	ContextSize   *int     `yaml:"context_size,omitempty"`
	Host          *string  `yaml:"host,omitempty"`
	Port          *int     `yaml:"port,omitempty"`
	FlashAttn     *bool    `yaml:"flash_attn,omitempty"`
	ContBatching  *bool    `yaml:"cont_batching,omitempty"`
	Parallel      *int     `yaml:"parallel,omitempty"`
	Mlock         *bool    `yaml:"mlock,omitempty"`
	NoMmap        *bool    `yaml:"no_mmap,omitempty"`
	Embedding     *bool    `yaml:"embedding,omitempty"`
	Jinja         *bool    `yaml:"jinja,omitempty"`
	Temperature   *float64 `yaml:"temperature,omitempty"`
	RepeatPenalty *float64 `yaml:"repeat_penalty,omitempty"`
	TopK          *int     `yaml:"top_k,omitempty"`
	TopP          *float64 `yaml:"top_p,omitempty"`
	MinP          *float64 `yaml:"min_p,omitempty"`
}

// DefaultConfigDir returns ~/.config/llama-launcher.
func DefaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, defaultConfigDir)
}

// DefaultConfigPath returns the default config file location.
func DefaultConfigPath() string {
	return filepath.Join(DefaultConfigDir(), configFileName)
}

// LoadConfig reads, parses, and validates the YAML configuration at the given path.
func LoadConfig(path string) (*Config, error) {
	path = ExpandTilde(path)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrConfigNotFound, path)
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.ConfigPath = path
	cfg.ModelsDir = ExpandTilde(cfg.ModelsDir)
	cfg.LogDir = ExpandTilde(cfg.LogDir)

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.DefaultBackend != "" {
		return fmt.Errorf("config: 'default_backend' is no longer supported — use 'server' in the defaults section instead\n  Move to:\n    defaults:\n      server: %s", c.DefaultBackend)
	}
	if len(c.Endpoints) > 0 {
		return fmt.Errorf("config: 'endpoints' has been merged into 'servers' — move entries to the servers section")
	}
	if len(c.Servers) == 0 {
		return fmt.Errorf("config: no servers defined")
	}
	for name := range c.Servers {
		if _, err := GetBackend(name); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	if len(c.Profiles) == 0 {
		return fmt.Errorf("config: no profiles defined")
	}
	for name, p := range c.Profiles {
		if p.Backend != "" {
			return fmt.Errorf("config: profile %q uses 'backend' which has been renamed to 'server'\n  Change to: server: %s", name, p.Backend)
		}
	}
	if c.LogDir == "" {
		c.LogDir = filepath.Join(DefaultConfigDir(), "logs")
	}
	var enabledServers []string
	for name, enabled := range c.Servers {
		if enabled {
			enabledServers = append(enabledServers, name)
		}
	}
	if len(enabledServers) == 0 {
		return fmt.Errorf("config: no servers enabled")
	}
	if c.Defaults.Server == nil && len(enabledServers) == 1 {
		c.Defaults.Server = &enabledServers[0]
	}
	return nil
}

// BackendAddr returns the default address for a backend.
func (c *Config) BackendAddr(backendName string) string {
	b, err := GetBackend(backendName)
	if err != nil {
		return ""
	}
	return b.DefaultAddr()
}

// ConfiguredBackendAddr returns the default address for a backend. Each
// backend uses its own default address (e.g. llamacpp→:8080,
// lmstudio→:1234) so that health checks don't cross-detect another
// backend's server.
func (c *Config) ConfiguredBackendAddr(backendName string) string {
	b, err := GetBackend(backendName)
	if err != nil {
		return ""
	}
	var params ProfileParams
	applyBackendFallbacks(&params, c, backendName, b)
	return fmt.Sprintf("%s:%d", *params.Host, *params.Port)
}

// IsManaged returns true if the backend implements ManagedBackend.
func (c *Config) IsManaged(backendName string) bool {
	b, err := GetBackend(backendName)
	if err != nil {
		return false
	}
	_, ok := b.(ManagedBackend)
	return ok
}

// ResolveProfile merges a named profile with defaults and resolves its model path.
func (c *Config) ResolveProfile(name string) (*ResolvedProfile, error) {
	profile, ok := c.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", name)
	}

	merged := mergeParams(c.Defaults, profile.ProfileParams)

	backendName := ""
	if merged.Server != nil {
		backendName = *merged.Server
	}
	if backendName == "" {
		return nil, fmt.Errorf("profile %q: no server specified (set server in defaults or profile)", name)
	}
	enabled, listed := c.Servers[backendName]
	if !listed {
		return nil, fmt.Errorf("profile %q: server %q not listed in servers section", name, backendName)
	}
	if !enabled {
		return nil, fmt.Errorf("profile %q: server %q is disabled", name, backendName)
	}

	b, err := GetBackend(backendName)
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}

	applyBackendFallbacks(&merged, c, backendName, b)

	modelPath, err := b.ResolveModel(c, profile.Model)
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}

	return &ResolvedProfile{
		Name:          name,
		Description:   profile.Description,
		ModelRef:      profile.Model,
		ModelPath:     modelPath,
		Backend:       backendName,
		ExtraArgs:     profile.ExtraArgs,
		ProfileParams: merged,
	}, nil
}

// ProfileNames returns profile names grouped by backend (default backend first,
// then remaining backends sorted), alphabetically within each group.
func (c *Config) ProfileNames() []string {
	groups := make(map[string][]string)
	for name, p := range c.Profiles {
		server := resolveProfileServer(c, &p)
		if !c.IsServerEnabled(server) {
			continue
		}
		groups[server] = append(groups[server], name)
	}
	for _, names := range groups {
		sort.Strings(names)
	}

	defaultServer := ""
	if c.Defaults.Server != nil {
		defaultServer = *c.Defaults.Server
	}

	backends := make([]string, 0, len(groups))
	for b := range groups {
		if b != defaultServer {
			backends = append(backends, b)
		}
	}
	sort.Strings(backends)
	if _, ok := groups[defaultServer]; ok {
		backends = append([]string{defaultServer}, backends...)
	}

	var result []string
	for _, b := range backends {
		result = append(result, groups[b]...)
	}
	return result
}

func resolveDefaultServer(cfg *Config) string {
	if cfg.Defaults.Server != nil {
		return *cfg.Defaults.Server
	}
	return ""
}

func resolveProfileServer(cfg *Config, p *Profile) string {
	if p.Server != nil {
		return *p.Server
	}
	return resolveDefaultServer(cfg)
}

func applyFallbacks(p *ProfileParams) {
	if p.Host == nil {
		h := defaultHost
		p.Host = &h
	}
	if p.Port == nil {
		pt := defaultPort
		p.Port = &pt
	}
}

func applyBackendFallbacks(p *ProfileParams, cfg *Config, backendName string, b Backend) {
	if p.Host != nil && p.Port != nil {
		return
	}
	addr := cfg.BackendAddr(backendName)
	if addr == "" {
		applyFallbacks(p)
		return
	}
	host, portStr, ok := strings.Cut(addr, ":")
	if !ok {
		applyFallbacks(p)
		return
	}
	if p.Host == nil {
		p.Host = &host
	}
	if p.Port == nil {
		if port, err := strconv.Atoi(portStr); err == nil {
			p.Port = &port
		} else {
			pt := defaultPort
			p.Port = &pt
		}
	}
}

func mergeParams(defaults, profile ProfileParams) ProfileParams {
	merged := defaults
	if profile.Server != nil {
		merged.Server = profile.Server
	}
	if profile.GPULayers != nil {
		merged.GPULayers = profile.GPULayers
	}
	if profile.Threads != nil {
		merged.Threads = profile.Threads
	}
	if profile.ThreadsBatch != nil {
		merged.ThreadsBatch = profile.ThreadsBatch
	}
	if profile.BatchSize != nil {
		merged.BatchSize = profile.BatchSize
	}
	if profile.ContextSize != nil {
		merged.ContextSize = profile.ContextSize
	}
	if profile.Host != nil {
		merged.Host = profile.Host
	}
	if profile.Port != nil {
		merged.Port = profile.Port
	}
	if profile.FlashAttn != nil {
		merged.FlashAttn = profile.FlashAttn
	}
	if profile.ContBatching != nil {
		merged.ContBatching = profile.ContBatching
	}
	if profile.Parallel != nil {
		merged.Parallel = profile.Parallel
	}
	if profile.Mlock != nil {
		merged.Mlock = profile.Mlock
	}
	if profile.NoMmap != nil {
		merged.NoMmap = profile.NoMmap
	}
	if profile.Embedding != nil {
		merged.Embedding = profile.Embedding
	}
	if profile.Jinja != nil {
		merged.Jinja = profile.Jinja
	}
	if profile.Temperature != nil {
		merged.Temperature = profile.Temperature
	}
	if profile.RepeatPenalty != nil {
		merged.RepeatPenalty = profile.RepeatPenalty
	}
	if profile.TopK != nil {
		merged.TopK = profile.TopK
	}
	if profile.TopP != nil {
		merged.TopP = profile.TopP
	}
	if profile.MinP != nil {
		merged.MinP = profile.MinP
	}
	return merged
}

// ExpandTilde replaces a leading ~ with the user's home directory.
func ExpandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// GenerateExampleConfig writes a documented example config to the given path.
func GenerateExampleConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	return os.WriteFile(path, []byte(defaults.ExampleConfig), 0o644)
}
