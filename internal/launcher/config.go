package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	Endpoints          map[string]string       `yaml:"endpoints"`       // deprecated: addresses now come from host/port in defaults or per profile
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
//
// A server's API key comes from one of two sources, never both: the literal
// api_key written in the file, or the standard output of api_key_cmd — a
// command that prints the key, which is how the key can live in a secret store
// instead of in the config file. plaintext_key_ok is the user's answer to the
// offer to move a literal key into such a store: set, it means "this one stays
// in the file, stop asking".
type ServerConfig struct {
	Enabled        bool
	APIKey         string
	APIKeyCmd      string
	PlaintextKeyOK bool

	// resolvedKey is what api_key_cmd printed. It is filled in once per load
	// (resolveKeyCommands) rather than parsed, so it is deliberately not a
	// YAML field: nothing in the file can set it, and a Config built by hand
	// in a test carries a key only where it put one.
	resolvedKey string
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
			Enabled        *bool  `yaml:"enabled"`
			APIKey         string `yaml:"api_key"`
			APIKeyCmd      string `yaml:"api_key_cmd"`
			PlaintextKeyOK bool   `yaml:"plaintext_key_ok"`
		}
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("server entry: %w", err)
		}
		s.Enabled = raw.Enabled == nil || *raw.Enabled
		s.APIKey = raw.APIKey
		s.APIKeyCmd = raw.APIKeyCmd
		s.PlaintextKeyOK = raw.PlaintextKeyOK
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

// APIKeyFor returns the configured API key for the named server: the literal
// api_key with surrounding whitespace trimmed when the file names one,
// otherwise what the entry's api_key_cmd printed at load. "" when the entry
// names no key source at all.
//
// A literal winning over a command is not a precedence rule the user can rely
// on — validate refuses an entry that sets both — it is the order that needs no
// subprocess to answer.
func (c *Config) APIKeyFor(name string) string {
	sc := c.Servers[name]
	if key := strings.TrimSpace(sc.APIKey); key != "" {
		return key
	}
	return sc.resolvedKey
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
		return nil, fmt.Errorf("parsing config %s: %w", path, redactYAMLError(err))
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

// redactedFileText stands in for every span of the parsed file that a yaml.v3
// error message would otherwise quote back.
const redactedFileText = "…"

// yamlFileEchoes are the yaml.v3 message templates that quote the parsed file,
// each rewritten to keep the template's words — the line, the kind of mismatch,
// the Go type — and drop the file's text. The value patterns are lazy up to the
// template word that follows the value, so a value holding a backtick or a
// newline is still cut whole.
var yamlFileEchoes = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile("(?s)(cannot unmarshal \\S+ )`.*?`( into )"), "${1}`" + redactedFileText + "`${2}"},
	{regexp.MustCompile("(?s)(cannot decode \\S+ )`.*?`( as a )"), "${1}`" + redactedFileText + "`${2}"},
	{regexp.MustCompile("`[^`]*`"), "`" + redactedFileText + "`"},
	{regexp.MustCompile(`anchor '\S*'`), "anchor '" + redactedFileText + "'"},
	{regexp.MustCompile(`mapping key ".*" already defined`), `mapping key "` + redactedFileText + `" already defined`},
	{regexp.MustCompile(`invalid map key: .*`), "invalid map key: " + redactedFileText},
	{regexp.MustCompile(`(?s)field .*? (not found|already set) in type`), "field " + redactedFileText + " ${1} in type"},
}

// yamlTagMention matches a node tag where yaml.v3 names the kind of mismatch.
// A core tag (!!str, !!int, ...) is the mismatch itself; any other tag is text
// the file wrote.
var yamlTagMention = regexp.MustCompile(`(cannot unmarshal |cannot decode |as a )(!\S*)`)

// yamlCoreTags are the tags yaml.v3 resolves on its own; naming one quotes the
// YAML spec, not the file.
var yamlCoreTags = map[string]bool{
	"!!null": true, "!!bool": true, "!!str": true, "!!int": true, "!!float": true,
	"!!timestamp": true, "!!seq": true, "!!map": true, "!!binary": true, "!!merge": true,
}

// redactYAMLError returns err's message with every fragment of the parsed file
// replaced by "…", keeping the line numbers and the kind of mismatch. It
// rewrites the whole message string rather than switching on the error type,
// because ServerConfig.UnmarshalYAML wraps a *yaml.TypeError with %w and the
// echo then sits inside a plain wrapping error. The result wraps nothing: the
// original chain still carries the file's text.
func redactYAMLError(err error) error {
	message := err.Error()
	for _, echo := range yamlFileEchoes {
		message = echo.pattern.ReplaceAllString(message, echo.replacement)
	}
	message = yamlTagMention.ReplaceAllStringFunc(message, func(mention string) string {
		tagStart := strings.Index(mention, "!")
		if yamlCoreTags[mention[tagStart:]] {
			return mention
		}
		return mention[:tagStart] + "!" + redactedFileText
	})
	return errors.New(message)
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
	return LoadConfigNotify(path, func(w string) { fmt.Fprintf(os.Stderr, "warning: %s\n", w) })
}

// LoadConfigNotify reads, parses, and validates the YAML configuration at the
// given path, delivering each non-fatal deprecation warning to notify as raw
// text — one call per warning, no "warning: " prefix. A nil sink discards them.
func LoadConfigNotify(path string, notify NoticeFunc) (*Config, error) {
	cfg, err := parseConfig(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if err := cfg.resolveKeyCommands(); err != nil {
		return nil, err
	}
	applyAPIKeys(cfg)
	for _, w := range cfg.Warnings {
		reportNotice(notify, w)
	}
	return cfg, nil
}

// Reload re-reads and validates the config file, updating the receiver in place.
// If the file is unreadable or invalid, the receiver is left unchanged.
// It goes through LoadConfig, so it writes non-fatal config warnings to
// stderr: it is for the CLI only. Library clients re-read a changed config
// by calling the facade's LoadConfig again, which takes a notice sink.
func (c *Config) Reload() {
	newCfg, err := LoadConfig(c.ConfigPath)
	if err != nil {
		return
	}
	*c = *newCfg
}

// validate is the load-time check: it fails on the first error configChecks
// finds, as "config: <error>", and on success stores the warnings in
// c.Warnings for LoadConfigNotify to deliver.
func (c *Config) validate() error {
	var warnings []string
	for _, f := range c.configChecks() {
		if f.warning {
			warnings = append(warnings, f.load)
			continue
		}
		return fmt.Errorf("config: %s", f.load)
	}
	c.Warnings = warnings
	return nil
}

// validateAll is `config validate`'s check: every error and warning
// configChecks finds, in its order, followed by each profile's ResolveProfile
// error — the one check that looks past the file (missing models).
func (c *Config) validateAll() []string {
	var problems []string
	for _, f := range c.configChecks() {
		problems = append(problems, f.report)
	}
	for name := range c.Profiles {
		if _, err := c.ResolveProfile(name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return problems
}

// configFinding is one thing configChecks found. An error carries its text for
// each surface: load is validate's, which LoadConfig prefixes with "config: "
// and whose migration hints indent by two; report is validateAll's, printed as
// a numbered `config validate` line, so its hints indent under the number. A
// warning reads the same on both surfaces.
type configFinding struct {
	warning bool
	load    string
	report  string
}

// Hint indents for the two surfaces (see configFinding).
const (
	loadHintIndent   = "  "
	reportHintIndent = "     "
)

// configError builds an error finding from text rendered once per surface with
// that surface's hint indent.
func configError(text func(indent string) string) configFinding {
	return configFinding{load: text(loadHintIndent), report: text(reportHintIndent)}
}

// configErrors builds one error finding per line, each reading the same on
// both surfaces.
func configErrors(lines ...string) []configFinding {
	findings := make([]configFinding, 0, len(lines))
	for _, line := range lines {
		findings = append(findings, configFinding{load: line, report: line})
	}
	return findings
}

// configWarnings builds one warning finding per line.
func configWarnings(lines []string) []configFinding {
	findings := make([]configFinding, 0, len(lines))
	for _, line := range lines {
		findings = append(findings, configFinding{warning: true, load: line, report: line})
	}
	return findings
}

// configChecks is the one check list behind both validators: validate fails on
// the first error it returns, validateAll reports all of it. A new check goes
// here and nowhere else. It returns findings in `config validate`'s report
// order.
//
// It also fills the two defaults the rest of the program relies on:
// defaults.server when exactly one server is enabled (the fallback warnings
// read it), and log_dir.
func (c *Config) configChecks() []configFinding {
	var findings []configFinding

	if c.DefaultBackend != "" {
		findings = append(findings, configError(func(in string) string {
			return fmt.Sprintf("'default_backend' is no longer supported — use 'server' in the defaults section instead\n%[1]sMove to:\n%[1]s  defaults:\n%[1]s    server: %[2]s", in, c.DefaultBackend)
		}))
	}
	if len(c.Endpoints) > 0 {
		findings = append(findings, configError(func(in string) string {
			return fmt.Sprintf("'endpoints' is no longer supported — the servers section only enables servers; set a non-default address via 'host'/'port' in the defaults section or on a profile\n%[1]sMove to:\n%[1]s  defaults:\n%[1]s    host: <host>\n%[1]s    port: <port>", in)
		}))
	}

	if len(c.Servers) == 0 {
		findings = append(findings, configErrors("no servers defined")...)
	} else {
		for name := range c.Servers {
			if _, err := GetLLMServer(name); err != nil {
				findings = append(findings, configFinding{
					load:   err.Error(),
					report: fmt.Sprintf("unknown server %q in servers section", name),
				})
			}
		}
		var enabledServers []string
		for name, sc := range c.Servers {
			if sc.Enabled {
				enabledServers = append(enabledServers, name)
			}
		}
		if len(enabledServers) == 0 {
			findings = append(findings, configErrors("no servers enabled")...)
		}
		if c.Defaults.Server == nil && len(enabledServers) == 1 {
			c.Defaults.Server = &enabledServers[0]
		}
		findings = append(findings, configWarnings(c.defaultsServerFallbackWarnings(enabledServers))...)
		findings = append(findings, configErrors(c.apiKeySourceErrors()...)...)
		findings = append(findings, configWarnings(c.apiKeyWarnings())...)
	}

	if c.LogRetention != nil && *c.LogRetention < 0 {
		findings = append(findings, configErrors("log_retention must be 0 or positive")...)
	}
	if c.LogDir == "" {
		c.LogDir = filepath.Join(DefaultConfigDir(), "logs")
	}
	findings = append(findings, configWarnings(c.memoryBarWarnings())...)

	if len(c.Profiles) == 0 {
		findings = append(findings, configErrors("no profiles defined")...)
	}
	for name, p := range c.Profiles {
		if p.Backend != "" {
			findings = append(findings, configError(func(in string) string {
				return fmt.Sprintf("profile %q uses 'backend' which has been renamed to 'server'\n%sChange to: server: %s", name, in, p.Backend)
			}))
		}
	}

	return findings
}

// apiKeySourceErrors reports the key-source mistakes a config must not load
// with, one line per offending entry, in server-name order (the servers section
// is a map, so sorted order is the stable one).
//
// Both are refusals rather than warnings because neither has a safe reading. An
// entry naming a literal key AND a command says two different things about
// where its key lives, and picking one silently would send requests with a key
// the user believes they replaced. A blank api_key_cmd names no program at all,
// and treating it as "no key" would quietly turn an authenticated server into
// an unauthenticated one — the keyless state is spelled by writing neither key.
func (c *Config) apiKeySourceErrors() []string {
	names := make([]string, 0, len(c.Servers))
	for name := range c.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var problems []string
	for _, name := range names {
		sc := c.Servers[name]
		switch {
		case sc.APIKey != "" && sc.APIKeyCmd != "":
			problems = append(problems, fmt.Sprintf(
				"servers.%s: api_key and api_key_cmd are both set — a server's key comes from one source; keep the one that should answer for it and delete the other",
				name))
		case sc.APIKeyCmd != "" && strings.TrimSpace(sc.APIKeyCmd) == "":
			problems = append(problems, fmt.Sprintf(
				"servers.%s: api_key_cmd is blank — give it the command whose output IS the key, or remove the line to leave the server keyless",
				name))
		}
	}
	return problems
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

// The api_key_cmd resolver: where a server entry's key actually comes from when
// the file holds a command instead of the key.
//
// It runs at LOAD, once per process, for every ENABLED entry naming a command.
// The alternative — resolving at first use — buys a saved subprocess for a
// server this session never talks to, and pays for it with a keychain dialog
// popping up in the middle of a model switch and a broken command reported by
// whatever the user was doing rather than by the load that read the file. The
// launcher loads its config exactly once per run (cli.go), so this is one
// subprocess per configured entry per run; the interactive menu's Reload runs
// them again, which is the point — the store may have changed underneath it.
//
// A command that fails, times out or prints nothing FAILS THE LOAD. It is never
// read as "this server takes no key": a keyless server is spelled by naming no
// key source at all, so a source answering with nothing is a broken source, and
// degrading to unauthenticated requests would send them to a server the file
// said to authenticate against.

// keyCommandTimeout bounds one api_key_cmd run. It is generous because the
// command in front of a locked store is often a GUI unlock prompt, and a human
// reaching for a password takes tens of seconds. What it stops is the command
// that never answers at all, which would otherwise hang startup with no message.
const keyCommandTimeout = 60 * time.Second

// keyCommandWaitGrace bounds the wait AFTER the timeout fired. Killing the
// process ends the process, but a wrapper-shaped command — a shell re-execing
// the real tool — can leave a grandchild holding the stdout pipe it inherited,
// and cmd.Run would then block on the copy forever.
const keyCommandWaitGrace = 2 * time.Second

// maxKeyCommandOutput and maxKeyCommandStderr bound what one command may make
// the launcher hold in memory. An API key is a short line; a command printing
// more than 64 KiB of it is a misconfigured command (`cat /dev/urandom`, the
// wrong program on the path), and reading it to the end is how a typo in a
// config file becomes an out-of-memory kill. Stderr is kept only to quote back
// in the refusal, so it is bounded far tighter.
const (
	maxKeyCommandOutput = 64 << 10
	maxKeyCommandStderr = 4 << 10
)

// maxKeyErrorStderr is how much of what the command said survives into the
// refusal. The message is read on one line of a terminal, and a tool's own first
// sentence ("The specified item could not be found in the keychain.") is almost
// always the part that names the fix.
const maxKeyErrorStderr = 240

// resolveKeyCommands runs the api_key_cmd of every enabled server and stores
// what it printed as that entry's key. Disabled entries are skipped: the
// launcher never talks to their server, so asking their store for a secret
// would be a dialog the user cannot connect to anything they did.
//
// Before any command runs, the file they came from must pass configTrusted:
// on unix, a config the current user does not own, or that its group or others
// can write, is refused naming the file, and no command runs. A config with no
// enabled api_key_cmd is never judged — it runs nothing.
//
// The first failure stops the load and is returned as-is — it already names the
// entry and quotes the command.
func (c *Config) resolveKeyCommands() error {
	names := make([]string, 0, len(c.Servers))
	for name, sc := range c.Servers {
		if !sc.Enabled || strings.TrimSpace(sc.APIKeyCmd) == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	if err := configTrusted(c.ConfigPath); err != nil {
		return err
	}

	for _, name := range names {
		sc := c.Servers[name]
		key, err := runKeyCommand(name, sc.APIKeyCmd, keyCommandTimeout)
		if err != nil {
			return err
		}
		sc.resolvedKey = key
		c.Servers[name] = sc
	}
	return nil
}

// keyCommandArgv is how one api_key_cmd line is executed: handed WHOLE to the
// platform's shell, which splits it.
//
// A shell is the right call here and a wrong one elsewhere. This line is the
// user's own, written into a file only they can write (on unix
// resolveKeyCommands enforces that premise through configTrusted before any
// line runs, ADR-0016), and it is routinely a pipeline — `pass show llamacpp |
// head -1`, `op read op://…` — so splitting it in Go would force every such
// user into a wrapper script of their own. It is also the exact line the migration offer persists and reads back,
// so both halves of that round trip must go through the same door.
func keyCommandArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", command}
	}
	return []string{"sh", "-c", command}
}

// runKeyCommand runs one entry's api_key_cmd and returns what it printed as the
// key, with surrounding whitespace trimmed — a command that prints its key with
// a trailing newline is every command.
//
// The child gets no stdin and neither of the launcher's standard streams: it may
// be running under the interactive menu, which owns the terminal, so a tool that
// tried to prompt there would draw over the frame and read the keystrokes meant
// for the menu — a store that must ask the human to unlock has to do it through
// its own GUI agent. It inherits the environment whole, deliberately: `pass`,
// `op` and `security` need HOME, DISPLAY and their agents' sockets.
func runKeyCommand(entry, command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	argv := keyCommandArgv(command)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = nil
	stdout := &cappedWriter{limit: maxKeyCommandOutput}
	stderr := &cappedWriter{limit: maxKeyCommandStderr}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = keyCommandWaitGrace

	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("config: servers.%s: api_key_cmd %q did not answer within %s — a store that has to "+
			"ask you to unlock must prompt through its own GUI agent, since this command runs with no terminal of "+
			"its own", entry, command, timeout)
	}
	if runErr != nil {
		return "", fmt.Errorf("config: servers.%s: api_key_cmd %q failed: %w%s",
			entry, command, runErr, saidOnStderr(stderr.String()))
	}
	if stdout.over {
		return "", fmt.Errorf("config: servers.%s: api_key_cmd %q printed more than %d bytes — that is not an API "+
			"key; check that the command is the one that PRINTS the key and nothing else",
			entry, command, maxKeyCommandOutput)
	}

	key := strings.TrimSpace(stdout.String())
	if key == "" {
		return "", fmt.Errorf("config: servers.%s: api_key_cmd %q printed nothing — a key source that answers with "+
			"nothing is a broken source, not a keyless server; remove the line altogether to send no key%s",
			entry, command, saidOnStderr(stderr.String()))
	}
	return key, nil
}

// saidOnStderr renders what the command complained about as a tail for the
// refusal, or nothing at all when it stayed quiet. The text is folded onto one
// line and cut short because the refusal is read on one line of a terminal, and
// a page of a tool's output would push the launcher's own words off the screen.
func saidOnStderr(text string) string {
	folded := strings.Join(strings.Fields(text), " ")
	if folded == "" {
		return ""
	}
	if runes := []rune(folded); len(runes) > maxKeyErrorStderr {
		folded = strings.TrimSpace(string(runes[:maxKeyErrorStderr])) + "…"
	}
	return " — it said: " + folded
}

// cappedWriter is the bounded sink a key command's streams are read into: it
// keeps the first limit bytes, remembers that there were more, and never fails
// the write. Failing one would kill the command with a broken pipe and report
// THAT instead of the oversized output, which is the one fact the user needs.
type cappedWriter struct {
	limit int
	buf   []byte
	over  bool
}

// Write keeps what still fits and notes anything beyond it, always reporting a
// full write.
func (w *cappedWriter) Write(p []byte) (int, error) {
	switch room := w.limit - len(w.buf); {
	case room <= 0:
		w.over = w.over || len(p) > 0
	case len(p) > room:
		w.buf = append(w.buf, p[:room]...)
		w.over = true
	default:
		w.buf = append(w.buf, p...)
	}
	return len(p), nil
}

// String is what was kept.
func (w *cappedWriter) String() string {
	return string(w.buf)
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
