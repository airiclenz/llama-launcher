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
)

const (
	defaultHost      = "127.0.0.1"
	defaultPort      = 8080
	defaultConfigDir = ".config/llama-launcher"
	configFileName   = "config.yaml"
	stateFileName    = "state.json"
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
	DisplayCentered *bool              `yaml:"display_centered"`
	Defaults        ProfileParams      `yaml:"defaults"`
	Profiles        map[string]Profile `yaml:"profiles"`

	ConfigPath string `yaml:"-"`
}

func (c *Config) ShouldAutoClose() bool {
	return c.AutoClose == nil || *c.AutoClose
}

func (c *Config) ShouldDisplayCentered() bool {
	return c.DisplayCentered != nil && *c.DisplayCentered
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

// StatePath returns the path to the state file.
func StatePath() string {
	return filepath.Join(DefaultConfigDir(), stateFileName)
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
	if c.Defaults.Server == nil && len(c.Servers) == 1 {
		for name := range c.Servers {
			c.Defaults.Server = &name
		}
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
	if _, ok := c.Servers[backendName]; !ok {
		return nil, fmt.Errorf("profile %q: server %q not listed in servers section", name, backendName)
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

// ProfileNames returns sorted profile names for consistent display ordering.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
	return os.WriteFile(path, []byte(exampleConfig), 0o644)
}

const exampleConfig = `# llama-launcher configuration
#
# Three server types are supported:
#
#   llamacpp   — llama.cpp's llama-server binary. The launcher forks the
#                process, tracks PID, and restarts to switch models.
#
#   ollama     — Ollama. Connects to running instance or auto-starts
#                "ollama serve". Models loaded/unloaded via HTTP API.
#
#   lmstudio   — LM Studio. Connects to running instance or auto-starts
#                via "lms server start". Models loaded/unloaded via HTTP API.

# ──────────────────────────────────────────────────────────────
# Servers
# ──────────────────────────────────────────────────────────────
#
# Enable the servers available on your system.
# Binaries are auto-detected from PATH; default ports are
# per-backend (llamacpp: 8080, ollama: 11434, lmstudio: 1234).

servers:
  llamacpp: true
  # ollama: true
  # lmstudio: true

# ──────────────────────────────────────────────────────────────
# Paths
# ──────────────────────────────────────────────────────────────

# Base directory for model files (llamacpp only).
# Profile model paths are resolved relative to this directory
# unless they are absolute. Supports ~ expansion.
models_dir: ~/Models

# Directory for server log files.
log_dir: ~/.config/llama-launcher/logs

# ──────────────────────────────────────────────────────────────
# UI behaviour
# ──────────────────────────────────────────────────────────────

# Close the launcher after selecting a menu action (default: true).
# Set to false to keep the interactive menu open after each action.
auto_close: false

# Display the llama-launcher UI centered in the terminal (default: false).
display_centered: true

# ──────────────────────────────────────────────────────────────
# Default parameters
# ──────────────────────────────────────────────────────────────
#
# Shared by all profiles. Per-profile values override these.
# The "server" field selects which server to use by default.
#
# Not all parameters apply to all servers:
#
#   Parameter       llamacpp   ollama   lmstudio
#   ─────────────   ────────   ──────   ────────
#   gpu_layers      yes        -        yes (mapped: 99→"max", 0→"off")
#   threads         yes        -        -
#   threads_batch   yes        -        -
#   batch_size      yes        -        yes (mapped to eval_batch_size)
#   context_size    yes        -        yes
#   host / port     yes        yes      yes
#   flash_attn      yes        -        yes
#   cont_batching   yes        -        -
#   parallel        yes        -        -
#   mlock           yes        -        -
#   no_mmap         yes        -        -
#   embedding       yes        -        -
#   jinja           yes        -        -
#   temperature     yes        -        -
#   repeat_penalty  yes        -        -
#   top_k           yes        -        -
#   top_p           yes        -        -
#   min_p           yes        -        -

defaults:
  server: llamacpp
  gpu_layers: 99
  threads: 8
  threads_batch: 8
  batch_size: 512
  context_size: 4096
  host: "127.0.0.1"
  port: 8080
  flash_attn: true
  cont_batching: true
  parallel: 1
  mlock: false
  no_mmap: false
  embedding: false
  temperature: 0.7
  repeat_penalty: 1.1
  top_k: 40
  top_p: 0.95
  min_p: 0.05

# ──────────────────────────────────────────────────────────────
# Profiles
# ──────────────────────────────────────────────────────────────
#
# Each profile specifies a model to load. The "server" field
# selects which server to use (inherits from defaults if omitted).
# Profiles can override any parameter from the defaults block.
#
# Profile fields:
#   description   Human-readable label shown in the menu
#   model         Model reference (file path for llamacpp, name for ollama,
#                 publisher/repo/file for lmstudio)
#   server        Server to use (llamacpp, ollama, lmstudio)
#   extra_args    Additional CLI flags appended verbatim (llamacpp only)
#   <param>       Any parameter from the defaults block

profiles:

  # ── llama.cpp example ──────────────────────────────────────
  # Model is a file path, resolved relative to models_dir.
  example:
    description: "Example profile — edit with your model path"
    model: your-model-file.gguf
    context_size: 8192

  # ── Ollama examples ────────────────────────────────────────
  # Model is an Ollama model name (e.g. "llama3.1:8b").
  # Must be pulled first: ollama pull <model>
  # Uncomment ollama in the servers section above.
  #
  # ollama-llama3:
  #   description: "Llama 3.1 8B via Ollama"
  #   server: ollama
  #   model: llama3.1:8b
  #
  # ollama-codellama:
  #   description: "Code Llama 13B via Ollama"
  #   server: ollama
  #   model: codellama:13b

  # ── LM Studio examples ────────────────────────────────────
  # Model is an LM Studio model key (publisher/repo or full path
  # with quantization). Run "lms ls" to see available models.
  # Uncomment lmstudio in the servers section above.
  #
  # lmstudio-llama:
  #   description: "Llama 3.1 8B via LM Studio"
  #   server: lmstudio
  #   model: lmstudio-community/meta-llama-3.1-8b-instruct
  #   context_size: 16384
  #   flash_attn: true
  #   gpu_layers: 99
  #   batch_size: 512
  #
  # lmstudio-qwen:
  #   description: "Qwen 2.5 32B via LM Studio"
  #   server: lmstudio
  #   model: lmstudio-community/qwen2.5-32b-instruct
  #   context_size: 8192
  #   gpu_layers: 99
`
