package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/airiclenz/llama-launcher/internal/launcher/defaults"
)

const (
	defaultHost      = "127.0.0.1"
	defaultPort      = 8080
	defaultConfigDir = ".config/llama-launcher"
	configFileName   = "config.yaml"
)

// ErrConfigNotFound indicates the configuration file does not exist.
var ErrConfigNotFound = errors.New("config file not found")

// Config represents the top-level YAML configuration file.
type Config struct {
	Servers            map[string]ServerConfig `yaml:"servers"`
	DefaultBackend     string                  `yaml:"default_backend"` // deprecated: use defaults.server
	Endpoints          map[string]string       `yaml:"endpoints"`       // deprecated: merge into servers
	ModelsDir          string                  `yaml:"models_dir"`
	LogDir             string                  `yaml:"log_dir"`
	LogRetention       *int                    `yaml:"log_retention"`
	AutoClose          *bool                   `yaml:"auto_close"`
	AutoStopServer     *bool                   `yaml:"auto_stop_server"`
	AutoUnload         *bool                   `yaml:"auto_unload"`
	DisplayCentered    *bool                   `yaml:"display_centered"`
	SortAlphabetically *bool                   `yaml:"sort_alphabetically"`
	ShowMemoryStatus   *bool                   `yaml:"show_memory_status"`
	MemoryStatusFormat *string                 `yaml:"memory_status_format"`
	MemoryStatusBar    *MemoryStatusBar        `yaml:"memory_status_bar"`
	RefreshDuration    *int                    `yaml:"refresh_duration"`
	Defaults           ProfileParams           `yaml:"defaults"`
	Profiles           map[string]Profile      `yaml:"profiles"`

	ConfigPath   string          `yaml:"-"`
	Warnings     []string        `yaml:"-"`
	profileOrder []string        `yaml:"-"`
	memTpl       *MemoryTemplate `yaml:"-"`
	memTplKey    string          `yaml:"-"`
}

// ServerConfig holds the per-server settings from the servers section.
type ServerConfig struct {
	Enabled bool
	APIKey  string
}

// UnmarshalYAML accepts both forms of a servers entry: the plain bool form
// ("llamacpp: true") and the mapping form ("llamacpp: {enabled: true,
// api_key: ...}"). In the mapping form, enabled defaults to true.
func (s *ServerConfig) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var enabled bool
		if err := node.Decode(&enabled); err != nil {
			return fmt.Errorf("server entry must be a bool or a mapping: %w", err)
		}
		*s = ServerConfig{Enabled: enabled}
		return nil
	case yaml.MappingNode:
		var raw struct {
			Enabled *bool  `yaml:"enabled"`
			APIKey  string `yaml:"api_key"`
		}
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("server entry: %w", err)
		}
		s.Enabled = raw.Enabled == nil || *raw.Enabled
		s.APIKey = raw.APIKey
		return nil
	default:
		return fmt.Errorf("server entry must be a bool or a mapping")
	}
}

// MemoryStatusBar configures the default geometry and colors for
// {..._pct:bar} tokens in memory_status_format. Inline token parts
// override these per bar. All keys are optional.
type MemoryStatusBar struct {
	Width      *int    `yaml:"width"`
	Color      *string `yaml:"color"`
	Background *string `yaml:"background"`
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

func (c *Config) ShouldSortAlphabetically() bool {
	return c.SortAlphabetically == nil || *c.SortAlphabetically
}

func (c *Config) ShouldShowMemoryStatus() bool {
	return c.ShowMemoryStatus == nil || *c.ShowMemoryStatus
}

func (c *Config) MemoryStatusTemplate() string {
	if c.MemoryStatusFormat == nil || *c.MemoryStatusFormat == "" {
		return DefaultMemoryStatusTemplate
	}
	return *c.MemoryStatusFormat
}

// memoryBarDefaults resolves the memory_status_bar block against the
// built-in defaults (width 10, green fill, gray background). Widths are
// clamped to the valid range; unknown color names fall back to the built-in
// (validate reports them as warnings).
func (c *Config) memoryBarDefaults() BarDefaults {
	d := builtinBarDefaults()
	b := c.MemoryStatusBar
	if b == nil {
		return d
	}
	if b.Width != nil {
		d.Width = clampBarWidth(*b.Width)
	}
	if b.Color != nil {
		if fg, _, ok := memColor(*b.Color); ok {
			d.Fg = fg
		}
	}
	if b.Background != nil {
		if _, bg, ok := memColor(*b.Background); ok {
			d.Bg = bg
		}
	}
	return d
}

// memoryBarWarnings reports unknown color names in the memory_status_bar
// block. Cosmetic settings never fail config load; the bar falls back to
// the built-in colors instead.
func (c *Config) memoryBarWarnings() []string {
	b := c.MemoryStatusBar
	if b == nil {
		return nil
	}
	var warnings []string
	if b.Color != nil {
		if _, _, ok := memColor(*b.Color); !ok {
			warnings = append(warnings, fmt.Sprintf("memory_status_bar: unknown color %q — using the default", *b.Color))
		}
	}
	if b.Background != nil {
		if _, _, ok := memColor(*b.Background); !ok {
			warnings = append(warnings, fmt.Sprintf("memory_status_bar: unknown background color %q — using the default", *b.Background))
		}
	}
	return warnings
}

// CompiledMemoryTemplate returns the compiled memory_status_format,
// recompiling only when the format string or bar defaults changed —
// including after Reload, which replaces the struct and clears the cache.
// The interactive menu probes, reloads, and renders on one goroutine, so
// the memoization needs no locking.
func (c *Config) CompiledMemoryTemplate() *MemoryTemplate {
	bar := c.memoryBarDefaults()
	key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", c.MemoryStatusTemplate(), bar.Width, bar.Fg, bar.Bg)
	if c.memTpl == nil || c.memTplKey != key {
		c.memTpl = CompileMemoryTemplate(c.MemoryStatusTemplate(), bar)
		c.memTplKey = key
	}
	return c.memTpl
}

// MenuRefreshInterval returns how often the interactive menu re-probes the
// backends for the status header (server running / loaded model) and for
// stale-menu detection. The memory readout refreshes on its own fixed
// 1-second tick (see menuTickInterval). The value is clamped to a minimum
// of 1 second so a misconfigured 0 cannot spin the render loop.
// Default: 10 seconds.
func (c *Config) MenuRefreshInterval() time.Duration {
	if c.RefreshDuration == nil {
		return 10 * time.Second
	}
	if *c.RefreshDuration < 1 {
		return 1 * time.Second
	}
	return time.Duration(*c.RefreshDuration) * time.Second
}

func (c *Config) IsServerEnabled(name string) bool {
	return c.Servers[name].Enabled
}

// APIKeyFor returns the configured API key for the named server with
// surrounding whitespace trimmed, or "" when no key is set.
func (c *Config) APIKeyFor(name string) string {
	return strings.TrimSpace(c.Servers[name].APIKey)
}

// Profile represents a named model configuration within the YAML config.
type Profile struct {
	Title         string   `yaml:"title,omitempty"`
	Description   string   `yaml:"description,omitempty"`
	Model         string   `yaml:"model"`
	Backend       string   `yaml:"backend"`
	IsFavourite   bool     `yaml:"is_favourite,omitempty"`
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

// parseConfig reads and unmarshals the YAML config without running validation.
func parseConfig(path string) (*Config, error) {
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

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err == nil {
		cfg.profileOrder = extractProfileOrder(&root)
	}

	cfg.ConfigPath = path
	cfg.ModelsDir = ExpandTilde(cfg.ModelsDir)
	cfg.LogDir = ExpandTilde(cfg.LogDir)

	return &cfg, nil
}

// extractProfileOrder walks the YAML document and returns the keys of the
// top-level "profiles" mapping in the order they appear in the source file.
func extractProfileOrder(root *yaml.Node) []string {
	if root == nil || len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i]
		if key.Value != "profiles" {
			continue
		}
		val := doc.Content[i+1]
		if val.Kind != yaml.MappingNode {
			return nil
		}
		names := make([]string, 0, len(val.Content)/2)
		for j := 0; j+1 < len(val.Content); j += 2 {
			names = append(names, val.Content[j].Value)
		}
		return names
	}
	return nil
}

// LoadConfig reads, parses, and validates the YAML configuration at the given path.
// Non-fatal deprecation warnings are written to stderr after a successful validation.
func LoadConfig(path string) (*Config, error) {
	cfg, err := parseConfig(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	applyAPIKeys(cfg)
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	return cfg, nil
}

// Reload re-reads and validates the config file, updating the receiver in place.
// If the file is unreadable or invalid, the receiver is left unchanged.
func (c *Config) Reload() {
	newCfg, err := LoadConfig(c.ConfigPath)
	if err != nil {
		return
	}
	*c = *newCfg
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
		if _, err := GetLLMServer(name); err != nil {
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
	if c.LogRetention != nil && *c.LogRetention < 0 {
		return fmt.Errorf("config: log_retention must be 0 or positive")
	}
	if c.LogDir == "" {
		c.LogDir = filepath.Join(DefaultConfigDir(), "logs")
	}
	var enabledServers []string
	for name, sc := range c.Servers {
		if sc.Enabled {
			enabledServers = append(enabledServers, name)
		}
	}
	if len(enabledServers) == 0 {
		return fmt.Errorf("config: no servers enabled")
	}
	if c.Defaults.Server == nil && len(enabledServers) == 1 {
		c.Defaults.Server = &enabledServers[0]
	}
	c.Warnings = c.defaultsServerFallbackWarnings(enabledServers)
	c.Warnings = append(c.Warnings, c.apiKeyWarnings()...)
	c.Warnings = append(c.Warnings, c.memoryBarWarnings()...)
	return nil
}

// apiKeyWarnings reports api_key values carrying leading or trailing
// whitespace; APIKeyFor uses the trimmed value.
func (c *Config) apiKeyWarnings() []string {
	var names []string
	for name, sc := range c.Servers {
		if sc.APIKey != strings.TrimSpace(sc.APIKey) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	warnings := make([]string, 0, len(names))
	for _, name := range names {
		warnings = append(warnings, fmt.Sprintf(
			"servers.%s: api_key has leading/trailing whitespace — using the trimmed value", name))
	}
	return warnings
}

// defaultsServerFallbackWarnings returns one deprecation warning per profile
// that relies on the soft-deprecated defaults.server fallback (multiple enabled
// servers, profile omits server:). See ADR-0005.
func (c *Config) defaultsServerFallbackWarnings(enabledServers []string) []string {
	if len(enabledServers) <= 1 || c.Defaults.Server == nil {
		return nil
	}
	fallback := *c.Defaults.Server
	var names []string
	for name, p := range c.Profiles {
		if p.Server == nil {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	warnings := make([]string, 0, len(names))
	for _, name := range names {
		warnings = append(warnings, fmt.Sprintf(
			"profile %q: missing 'server:' — falling back to defaults.server (%q). defaults.server is deprecated and will be removed; set server: explicitly on the profile",
			name, fallback))
	}
	return warnings
}

func (c *Config) validateAll() []string {
	var problems []string

	if c.DefaultBackend != "" {
		problems = append(problems, fmt.Sprintf(
			"'default_backend' is no longer supported — use 'server' in the defaults section instead\n     Move to:\n       defaults:\n         server: %s", c.DefaultBackend))
	}
	if len(c.Endpoints) > 0 {
		problems = append(problems, "'endpoints' has been merged into 'servers' — move entries to the servers section")
	}

	if len(c.Servers) == 0 {
		problems = append(problems, "no servers defined")
	} else {
		for name := range c.Servers {
			if _, err := GetLLMServer(name); err != nil {
				problems = append(problems, fmt.Sprintf("unknown server %q in servers section", name))
			}
		}
		var enabledServers []string
		for name, sc := range c.Servers {
			if sc.Enabled {
				enabledServers = append(enabledServers, name)
			}
		}
		if len(enabledServers) == 0 {
			problems = append(problems, "no servers enabled")
		}
		if c.Defaults.Server == nil && len(enabledServers) == 1 {
			c.Defaults.Server = &enabledServers[0]
		}
		problems = append(problems, c.defaultsServerFallbackWarnings(enabledServers)...)
		problems = append(problems, c.apiKeyWarnings()...)
	}

	if c.LogRetention != nil && *c.LogRetention < 0 {
		problems = append(problems, "log_retention must be 0 or positive")
	}
	if c.LogDir == "" {
		c.LogDir = filepath.Join(DefaultConfigDir(), "logs")
	}
	problems = append(problems, c.memoryBarWarnings()...)

	if len(c.Profiles) == 0 {
		problems = append(problems, "no profiles defined")
	} else {
		for name, p := range c.Profiles {
			if p.Backend != "" {
				problems = append(problems, fmt.Sprintf(
					"profile %q uses 'backend' which has been renamed to 'server'\n     Change to: server: %s", name, p.Backend))
			}
		}
		for name := range c.Profiles {
			if _, err := c.ResolveProfile(name); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}

	return problems
}

func (c *Config) backendAddr(backendName string) string {
	b, err := GetLLMServer(backendName)
	if err != nil {
		return ""
	}
	return b.DefaultAddr()
}

// ConfiguredBackendAddr returns the address for a backend as resolved from
// config defaults and backend fallbacks. This is the address the launcher
// would use when connecting to the backend without a profile-specific override.
func (c *Config) ConfiguredBackendAddr(backendName string) string {
	b, err := GetLLMServer(backendName)
	if err != nil {
		return ""
	}
	params := c.Defaults
	applyBackendFallbacks(&params, c, backendName, b)
	return fmt.Sprintf("%s:%d", *params.Host, *params.Port)
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
	sc, listed := c.Servers[backendName]
	if !listed {
		return nil, fmt.Errorf("profile %q: server %q not listed in servers section", name, backendName)
	}
	if !sc.Enabled {
		return nil, fmt.Errorf("profile %q: server %q is disabled", name, backendName)
	}

	b, err := GetLLMServer(backendName)
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
		Title:         profile.Title,
		Description:   profile.Description,
		ModelRef:      profile.Model,
		ModelPath:     modelPath,
		Backend:       backendName,
		ExtraArgs:     profile.ExtraArgs,
		ProfileParams: merged,
	}, nil
}

// ProfileNames returns profile names in the order they should appear in any
// listing UI. By default (or when sort_alphabetically is true) profiles are
// sorted by favourite status first (favourites before non-favourites), then by
// server name alphabetically, then alphabetically within each group. When
// sort_alphabetically is false, profiles are returned in the order they appear
// in the YAML config file. Profiles tied to a disabled server are filtered out
// in both modes.
func (c *Config) ProfileNames() []string {
	if !c.ShouldSortAlphabetically() {
		var names []string
		for _, name := range c.profileOrder {
			p, ok := c.Profiles[name]
			if !ok {
				continue
			}
			if !c.IsServerEnabled(resolveProfileServer(c, &p)) {
				continue
			}
			names = append(names, name)
		}
		return names
	}

	type entry struct {
		name   string
		fav    bool
		server string
	}

	var entries []entry
	for name, p := range c.Profiles {
		server := resolveProfileServer(c, &p)
		if !c.IsServerEnabled(server) {
			continue
		}
		entries = append(entries, entry{name: name, fav: p.IsFavourite, server: server})
	}

	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.fav != b.fav {
			return a.fav
		}
		if a.server != b.server {
			return a.server < b.server
		}
		return a.name < b.name
	})

	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.name
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

func applyBackendFallbacks(p *ProfileParams, cfg *Config, backendName string, b LLMServer) {
	if p.Host != nil && p.Port != nil {
		return
	}
	addr := cfg.backendAddr(backendName)
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

// ExpandTilde replaces a leading ~ or ~/ with the user's home directory.
func ExpandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// GenerateExampleConfig writes a documented example config to the given path.
func GenerateExampleConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	return os.WriteFile(path, []byte(defaults.ExampleConfig), 0o600)
}
