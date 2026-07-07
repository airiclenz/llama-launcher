# llama-launcher — Technical Design Specification

The architectural source of truth for `llama-launcher` lives in two places:

- **[CONTEXT.md](CONTEXT.md)** — domain language (LLM Server, Model, Profile; Activate, Load/Unload, Start/Stop).
- **[docs/adr/](docs/adr/)** — numbered Architectural Decision Records (ADRs 0001–0007) that pin down behaviour.

This document explains how those decisions are realised in code. Where this document and an ADR appear to conflict, the ADR wins; please file a doc fix.

## 1. Overview

`llama-launcher` is a lightweight Go CLI tool for managing LLM Servers through named configuration Profiles. It starts an LLM Server as a detached background process (or connects to one already running), asks it to load a Model, and exits — consuming zero resident memory while the server runs. Subsequent invocations rediscover running instances by probing the addresses in `config.yaml` (no persisted state files); each command queries the live LLM Server's own API for the currently loaded Model and parameters.

`llama-launcher` is a *process manager*, not a request router (see [ADR-0002](docs/adr/0002-not-a-router.md)). It does not expose any HTTP endpoint of its own and does not proxy client traffic. Clients connect to LLM Servers directly using each server's native address.

The architecture is server-agnostic: an `LLMServer` interface (see [§5.3](#53-llmserver-interface)) abstracts server-specific logic, making it straightforward to add support for other LLM Servers (LM Studio, Ollama, etc.) alongside the initial llama.cpp implementation.

Target platform: macOS (Apple Silicon). The compiled binary is the only artifact — no runtime dependencies.

## 2. Goals and Non-Goals

### Goals

- Profile-based configuration with defaults and per-Profile overrides for all relevant server flags.
- One-shot execution model: the launcher process exits after dispatching work, consuming zero resident memory while the LLM Server runs.
- Restart-per-Profile for llamacpp (see [ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)): each Profile activation forks a fresh `llama-server` with the Model and hardware parameters baked into start arguments.
- Multiple concurrent LLM Server instances, keyed by `host:port` (see [ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)).
- Interactive Profile selection with arrow-key navigation when invoked without arguments.
- Non-interactive subcommands for scripting and automation.
- Idempotent Profile activation: `load <profile>` against an already-active Profile is a no-op (see [ADR-0007](docs/adr/0007-profile-activation-idempotency.md)); `--restart` forces re-activation.
- Single YAML config file, easy to read and version-control.
- Modular server architecture for supporting multiple LLM Server implementations.

### Non-Goals

- Persistent TUI or dashboard (contradicts the memory-freeing goal).
- Model downloading or GGUF management.
- HTTP server, API proxying, or request routing **in the `llama-launcher` binary** (see [ADR-0002](docs/adr/0002-not-a-router.md)). Remote *control* (not inference) is available through a separate, optional adapter — see [§15](#15-optional-mcp-control-plane-adapter).
- Cross-platform support beyond macOS (Linux would work but is not a design target).

## 3. User Experience

### 3.1 Interactive Mode

Running `llama-launcher` with no arguments enters a one-shot interactive menu with arrow-key navigation, colored output, and full-screen repainting. Falls back to numbered input when stdin is not a terminal. The config file is automatically reloaded before each menu display and on every 10-second header refresh, so changes made via "Edit config" or an external editor take effect without restarting. If the reloaded file is invalid, the last good config is silently preserved.

**When no server is running:**

```
    llama-launcher v1.0.0

    Status  ● stopped

    ▸ DeepSeek Coder V2 Lite
      Qwen 2.5 32B
      reasoning-phi
      ─
      Start server only

    ↑↓ select · enter start & load · q quit
```

Each Profile row shows the Profile's optional `title`, falling back to the Profile name (`reasoning-phi` above) when no title is set. The same rule applies everywhere a Profile is presented: the status header, the Profile lists, and the "Switch model" pop-up.

When more than one LLM Server is enabled in the config, every Profile row additionally carries a column-aligned `[server]` tag (e.g. `[LLaMA.cpp]`, `[LM-Studio]`) showing which server the Profile targets. The tag appears in all Profile lists — server stopped, running with no model, and the "Switch model" pop-up. With a single enabled server the tag is omitted; whether the configured Profiles currently use more than one server does not matter, so the column does not appear and disappear as profiles are edited.

After selecting a Profile, the launcher shows a step-by-step progress popup that updates in place as each lifecycle stage completes, then prints a confirmation and exits:

```
    ╭──────────────────────────────────╮
    │                                  │
    │   Loading code-deepseek...       │
    │   Starting server                │
    │   ▸ Waiting for server...        │
    │                                  │
    ╰──────────────────────────────────╯
```

Completed steps are shown dimmed; the current step has a `▸` prefix. In non-interactive mode (CLI subcommands, piped output), steps print as plain text lines. After completion:

```
  ● Server started (PID 41023)
  ● Loaded code-deepseek on 127.0.0.1:8080
    Log: ~/.config/llama-launcher/logs/llamacpp-20260519-171200.log
```

**When a server is running with a model loaded:**

```
    llama-launcher v1.0.0

    Status   ● running
    Model    DeepSeek Coder V2 Lite
    Server   127.0.0.1:8080  PID 41023  Uptime 2h 14m

    ▸ Switch model
      Unload model
      Stop server
      Show log
      Show model config
      Edit config

    ↑↓ select · enter confirm · q quit
```

**When a server is running with no model loaded:**

```
    llama-launcher v1.0.0

    Status   ● running (no model)
    Server   127.0.0.1:8080  PID 41023  Uptime 5m 30s

    ▸ DeepSeek Coder V2 Lite
      Qwen 2.5 32B
      reasoning-phi
      ─
      Stop server
      Show log
      Edit config

    ↑↓ select · enter load · q quit
```

Selecting "Switch model" presents the Profile list (excluding the currently loaded Profile), unloads the current Model via API (for LLM Servers that support it) or restarts the server (for llamacpp), loads the new Model, and exits. When only one Profile is configured, "Switch model" is omitted from the menu entirely.

When more than one LLM Server instance is running, the relevant menu actions ("Stop server", "Unload model", "Show log") present a sub-list disambiguated by `host:port`. There is no "primary" or "current" instance — see [CONTEXT.md flagged ambiguities](CONTEXT.md#flagged-ambiguities) and [ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md).

### 3.2 Subcommands

For scripting, automation, and quick access:

| Command | Behaviour |
|---|---|
| `llama-launcher load <profile> [--restart]` | Primary command. Activates the Profile: start the LLM Server if needed, then load the Model. If the same Profile is already active at that address, the command is a no-op (see [ADR-0007](docs/adr/0007-profile-activation-idempotency.md)); if the running parameters differ from the resolved Profile, a drift notice is printed to stderr. Pass `--restart` (or `-r` / `--force`) to force re-activation regardless. Respects `auto_unload` and `auto_stop_server` for any other running instances. |
| `llama-launcher unload [profile]` | Unload the Model from the matching running instance. For LLM Servers with a load/unload API (Ollama, LM Studio), this is an HTTP call; for llamacpp, it stops the server (Model is part of the server's args). Optional Profile argument disambiguates when multiple Models are loaded; auto-detects when only one is. |
| `llama-launcher start [--profile p]` | Start the LLM Server without loading a Model. With `--profile` (or `-p`), resolve the named Profile and activate it — equivalent to `load <profile>`. |
| `llama-launcher stop [target]` | Stop a running LLM Server instance. `[target]` may be an `host:port` (preferred when multiple instances of the same backend run) or a backend name; auto-detects when only one instance is running. Stop is unconditional (see [ADR-0001](docs/adr/0001-stop-is-unconditional.md)) regardless of whether the launcher started the process. |
| `llama-launcher status [--json]` | Print one row per running LLM Server instance (address, backend, active Profile/Model, PID if known, uptime). Exit code 0 if any running, 1 if all stopped. With `--json`, emits a JSON array with one entry per running instance (`backend`, `running`, `address`, `active_profile`, `active_model`, `pid`, `uptime_seconds`) — multiple instances of one backend each get their own entry — plus one `running: false` entry for every enabled backend with no running instance; same exit code semantics. |
| `llama-launcher list [--json]` | Print all configured Profiles with descriptions and target LLM Server. With `--json`, emits a JSON array with one entry per Profile (`name`, `backend`, `model`, plus `title`, `description`, `gpu_layers` and `context_size` when set). |
| `llama-launcher logs [target] [--follow]` | Tail an instance's log file. `[target]` is an `host:port` or backend name; auto-detects the active instance when only one applies. With `--follow`, behaves like `tail -f`. This is the one subcommand that remains running until interrupted. |
| `llama-launcher logs clean [--days N\|--all]` | Delete old log files. Default threshold is 7 days; `--days N` overrides. `--all` removes everything. Always skips logs belonging to running servers. Reports files removed and space freed. |
| `llama-launcher config validate` | Parse config and report all validation problems at once (deprecated fields, unknown/disabled servers, missing models, Profiles missing `server:` with no defensible fallback). Uses `parseConfig` + `validateAll` to collect errors without stopping at the first. Exit 0 if valid, 2 if invalid. |
| `llama-launcher config init [--force]` | Generate the example config file at the configured path. Refuses to overwrite an existing file unless `--force` (or `-f`) is passed. Exit 0 on success, 2 on error or if the file already exists. |
| `llama-launcher config reset` | Overwrite the config file with the example config unconditionally. Provides a quick way to return to a known-good starting point. Exit 0 on success, 2 on error. |

All subcommands except `logs --follow` exit immediately after completing their action.

### 3.3 Exit Codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Server not running (for `status`) or general expected condition |
| 2 | Configuration error (missing file, parse error, unknown profile) |
| 3 | Process management or API error (failed to start, failed to load, stale PID) |

## 4. Configuration

### 4.1 File Location

Default: `~/.config/llama-launcher/config.yaml`

Override with `--config <path>` flag or `LLAMA_LAUNCHER_CONFIG` environment variable (flag takes precedence).

On first run, if no config file exists, the launcher creates a documented example config and exits with a message pointing to it.

### 4.2 Schema

```yaml
# Servers available on this system (true = enabled, false = disabled).
# Disabled servers are hidden from status display and their profiles
# are excluded from menus and CLI output.
# Each entry is either a plain bool or a mapping with an optional
# api_key ("enabled" defaults to true in the mapping form).
servers:
  llamacpp:
    enabled: true
    api_key: "secret"
  ollama: false
  lmstudio: false

# Base directory for model files. Profile model paths are resolved
# relative to this directory unless they are absolute.
models_dir: ~/Models

# Directory for server log files.
log_dir: ~/.config/llama-launcher/logs

# Automatically delete log files older than N days on server start.
# Runs silently before each new log file is created. Unset = no cleanup.
# log_retention: 7

# Stop other running instances when activating a profile (default: true).
# Set to false to allow multiple instances to run simultaneously.
# auto_stop_server: true

# Unload models that are no longer in active use on any still-running
# instance (default: true). One rule covers both the same-server swap
# case and the cross-server case. See ADR-0004.
# auto_unload: true

# How often (seconds) the interactive menu polls the servers — drives
# the server / loaded-model status lines. The memory readout below
# refreshes on its own fixed 1-second tick, independent of this value.
# Minimum 1 second; values below 1 are clamped. Default: 10.
# refresh_duration: 10

# Show a memory/swap readout in the status header (default: true).
# Refreshes every second while the menu is open, independent of
# `refresh_duration`; the underlying `sysctl` / `vm_stat` / `ioreg`
# shell-outs are cached just below that tick.
# show_memory_status: true

# Template for the memory readout. Placeholders are substituted with
# humanised byte values (e.g. "12.4GB") or rounded integer percentages
# (e.g. "38%"). Unknown placeholders are passed through literally. Default:
#   "{bold}Free RAM:{reset} {yellow}{free_ram} {bright-blue}{free_ram_pct}{reset} {used_ram_pct:bar} ✦ {bold}Swap:{reset} {yellow}{swap_used}{reset} ✦ {bold}GPU:{reset} {gpu_util_pct:bar}"
# Available placeholders:
#   {free_ram}        — available memory (free + inactive + speculative + purgeable)
#   {used_ram}        — total_ram - free_ram
#   {total_ram}       — physical RAM reported by hw.memsize
#   {compressed_ram}  — bytes held by the kernel's memory compressor
#   {swap_used}       — swap currently in use
#   {swap_total}      — total swap allocated
#   {free_swap}       — swap_total - swap_used
#   {free_ram_pct}    — free_ram / total_ram as rounded integer percentage
#   {used_ram_pct}    — used_ram / total_ram as rounded integer percentage
#   {swap_used_pct}   — swap_used / swap_total as rounded integer percentage
#                       (0% when swap is disabled)
#   {gpu_util_pct}    — GPU "Device Utilization %" from ioreg (Apple Silicon only)
#   {gpu_used_ram}    — unified RAM currently held by the GPU (Apple Silicon only)
#   {gpu_alloc_ram}   — unified RAM allocated to the GPU (Apple Silicon only)
# GPU values read 0 on Intel Macs or when ioreg is unavailable.
#
# Style tags color any span of the line: the 8 standard ANSI color names
# ({red}, {green}, …), their {bright-*} variants, {gray} (the ANSI
# bright-black slot), 256-color palette indices ({0}–{255}), exact 24-bit
# hex colors ({#rrggbb} / {#rgb}), {bold}, {dim}, and {reset}. Named
# colors are resolved by the terminal theme; palette/hex colors render
# identically everywhere. A template without style tags or bars keeps the
# classic dim rendering; one that contains any styling is rendered as-is
# and {reset} returns to the terminal default.
#
# Percentage placeholders can render as value-less bar graphs:
#   {pct_name:bar[:width[:color[:bgcolor]]]}
# Trailing parts are optional and fall back to memory_status_bar; empty
# parts are allowed ({used_ram_pct:bar::red}). The fill uses full blocks
# plus an eighth-block partial cell (▏▎▍▌▋▊▉ — 8 levels per cell); the
# background color is painted as an ANSI background behind the partial
# cell and the empty remainder, one continuous strip. Widths clamp to 1–40;
# malformed tokens (bad width, unknown color, :bar on a non-percentage
# placeholder) pass through literally.
# Plain alternative (no styling — rendered all-dim like older versions):
# memory_status_format: "RAM: {free_ram} free · Swap: {swap_used} used"

# Default geometry and colors for {..._pct:bar} tokens; inline token parts
# override these per bar. Unknown color names fall back to the defaults
# with a load-time warning — cosmetic settings never fail config load.
# memory_status_bar:
#   width: 10        # cells, clamped to 1–40
#   color: green     # filled portion
#   background: gray # empty portion

# Default parameters applied at server start (shared by all models).
# Note: `defaults.server` is soft-deprecated (see ADR-0005). Each profile
# should set `server:` explicitly. Auto-detection still applies when only
# one server is enabled.
defaults:
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

  # Sampling defaults (used as server-side defaults for API requests)
  temperature: 0.7
  repeat_penalty: 1.1
  top_k: 40
  top_p: 0.95
  min_p: 0.05

# Named profiles (model configurations).
# Each profile names its target LLM Server via `server:`. Switching
# between profiles that target different servers stops the old
# instance (when auto_stop_server is true) and starts the new one.
# Set is_favourite: true to pin a profile to the top of menus.
profiles:
  code-deepseek:
    title: "DeepSeek Coder V2 Lite"
    description: "DeepSeek Coder V2 Lite — coding tasks"
    server: llamacpp
    model: deepseek-coder-v2-lite-instruct-Q4_K_M.gguf
    is_favourite: true
    context_size: 8192
    temperature: 0.3
    extra_args:
      - "--no-warmup"

  chat-qwen:
    description: "Qwen 2.5 32B — general conversation"
    server: llamacpp
    model: qwen2.5-32b-instruct-Q4_K_M.gguf
    context_size: 16384
    threads: 6
    parallel: 2

  ollama-llama3:
    description: "Llama 3.1 8B via Ollama"
    server: ollama
    model: llama3.1:8b

  lmstudio-codegen:
    description: "Code model via LM Studio"
    server: lmstudio
    model: lmstudio-community/meta-llama-3.1-8b-instruct
```

`title` and `description` are both optional. `title` is the label shown wherever a Profile is presented to the user (status header, Profile lists, "Switch model" pop-up); when unset, the Profile name is shown instead. `description` is longer free text displayed only in the "Show model config" pop-up.

The `servers` map lists available LLM Servers. Keys are server names (`llamacpp`, `ollama`, `lmstudio`). Each value is either a plain boolean (`true` enables the server, `false` disables it) or a mapping with the keys `enabled` (optional, defaults to `true`) and `api_key` (optional). Disabled servers are hidden from status display, and Profiles targeting a disabled server are excluded from menus, CLI output, and Profile resolution. At least one server must be enabled.

`api_key` is stored as plaintext (the config file is created with mode 0600) and is trimmed of surrounding whitespace, with a load-time warning when trimming changed the value. Its meaning is per-backend:

- **llamacpp** — passed as `--api-key` at launch, so llama-server rejects client requests lacking `Authorization: Bearer <key>` (`/health` stays exempt). The key is visible in the process argv (`ps`); llama-server's `LLAMA_ARG_API_KEY` env var would avoid that and is a possible future alternative.
- **lmstudio** — LM Studio owns its token ("Require API token" in its Server Settings); the configured key is only *sent* by the launcher so its health checks and model load/unload calls keep working when auth is enabled.
- **ollama** — Ollama has no native auth; a key is only meaningful when the instance sits behind an authenticating reverse proxy. The launcher sends it with its own requests.

Regardless of backend, the launcher attaches the key as a `Bearer` header to every HTTP call it makes to that server (health checks, model load/unload, model listing, live-params queries). The launcher never enforces auth itself — it is not a proxy ([ADR-0002](docs/adr/0002-not-a-router.md)).

### 4.3 Parameter Resolution

Parameters are resolved in order of precedence (highest first):

1. Profile-level value (including `server`)
2. `defaults` block value (`defaults.server` is honoured but emits a deprecation warning when used — see [ADR-0005](docs/adr/0005-profile-server-is-identity.md))
3. Server-specific fallback from `servers` map address or `LLMServer.DefaultAddr()` (e.g. Ollama defaults to `localhost:11434`, LM Studio to `localhost:1234`)
4. Built-in fallback (only for `host: 127.0.0.1` and `port: 8080`)

All numeric and boolean parameters use pointer types internally to distinguish "not set" from zero/false. A nil pointer means "inherit from the next level."

### 4.4 Model Path Resolution

The `model` field in a Profile is resolved as follows:

1. If the path is absolute, use it directly.
2. If relative, join with `models_dir` from config.
3. `models_dir` itself supports `~` expansion.
4. Validate that the resolved path exists and is a regular file before loading.

Model path resolution is delegated to the backend via `LLMServer.ResolveModel()`. llama.cpp validates that the file exists on disk. Ollama and LM Studio accept opaque Model identifiers (e.g. `llama3.1:8b`, `lmstudio-community/meta-llama-3.1-8b-instruct`) without file validation.

### 4.5 Extra Arguments

The `extra_args` list in a Profile is appended verbatim to the assembled server command line. This provides an escape hatch for flags not explicitly modelled in the config schema without requiring a launcher update.

### 4.6 LLM Server Selection

A Profile's LLM Server is part of the Profile's identity, not a tunable parameter — the Model identifier format, supported parameters, and lifecycle semantics all depend on it (see [ADR-0005](docs/adr/0005-profile-server-is-identity.md)).

Each Profile **must** set `server:` explicitly. The launcher tolerates a missing `server:` field in two cases only:

1. **Single-server config** — when exactly one entry in `servers:` is enabled, the missing `server:` is auto-resolved to that one. No warning.
2. **`defaults.server` fallback** — when more than one server is enabled and a Profile omits `server:`, the launcher falls back to `defaults.server` and emits a deprecation warning naming the Profile. `defaults.server` is soft-deprecated and will be removed in a later release.

`config validate` reports the warning explicitly; `Reload` and `LoadConfig` surface it on stderr at load time.

### 4.7 Favourite Profiles

Each Profile may set `is_favourite: true` to pin it to the top of the menu and `list` output. Profiles are sorted by three keys in order: favourite status first (favourites before non-favourites), then by server (alphabetically), then by Profile name alphabetically. Favourite Profiles display a `★` marker right-aligned at the end of the row, in the same column across the entire list (rows are padded so the marker column is consistent). When no Profile in the listing is starred, no marker column is rendered. This ordering and rendering are produced by `Config.ProfileNames()` together with `buildProfileItems`/`buildSimpleProfileLines`/`cmdList`, and apply to every UI surface that lists Profiles (TUI menu, non-terminal fallback, `llama-launcher list`).

The top-level boolean `sort_alphabetically` selects the ordering rule. The default (unset or `true`) is the favourites/server/name sort described above. Setting `sort_alphabetically: false` lists Profiles in the order they appear under `profiles:` in the YAML file; favourite status no longer affects position (the `★` marker still renders unchanged). YAML insertion order is captured by `parseConfig`: after the standard struct decode it re-parses the document into a `yaml.Node` and walks the top-level `profiles:` mapping to record the keys in document order on the unexported `Config.profileOrder` slice. Disabled-server filtering is applied in both modes.

## 5. Architecture

### 5.1 Component Overview

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  CLI / Menu  │────▶│    Config    │────▶│   Server     │
│  (main.go)   │     │ (config.go)  │     │ (server.go)  │
│  (menu.go)   │     └──────────────┘     └──────┬───────┘
│  (ui.go)     │                                 │
└──────────────┘     ┌──────────────┐            │ start / stop
                     │  LLMServer   │◀───────────┤ load / unload
                     │ (backend.go) │            │
                     └──────┬───────┘     ┌──────┴─────────────────┐
                            │             │   Runtime discovery     │
              ┌─────────────┼──────────┐  │ (discovery.go) — probe │
              │             │          │  │  addrs + query APIs    │
       ┌──────┴───────┐ ┌───┴───┐ ┌────┴─────┐  └────────────────┘
       │  LlamaCpp    │ │Ollama │ │ LMStudio │
       │ (backend_    │ │       │ │          │
       │  llamacpp.go)│ │       │ │          │
       └──────────────┘ └───────┘ └──────────┘
```

### 5.2 Source Files

| File | Responsibility |
|---|---|
| `main.go` | Entry point, `--config` flag parsing, subcommand dispatch, usage text. `status` and `list` accept `--json` for structured output (local marshalling structs in `cli.go`). |
| `config.go` | Config/Profile/ProfileParams struct definitions, YAML loading (`parseConfig` for parse-only, `LoadConfig` for parse+validate), `Reload` for in-place re-read, `~` expansion, parameter merging, validation (`validate` for fast-fail, `validateAll` for collecting all problems including non-fatal warnings such as `defaults.server` fallback usage), example config generation. Server enable/disable filtering via `IsServerEnabled()`. `ServerConfig` (bool-or-mapping YAML form per server entry) with `APIKeyFor()` accessor; `LoadConfig` pushes configured API keys onto the registered backends via `applyAPIKeys`. |
| `defaults/config.yaml` | Example config template, embedded at compile time via `go:embed`. |
| `defaults/embed.go` | Embeds `config.yaml` and exports it as `defaults.ExampleConfig`. |
| `backend.go` | `LLMServer` and `ManagedLLMServer` interface definitions (see [§5.3](#53-llmserver-interface)), `ResolvedProfile` struct, LLM Server registry (register/get), `applyAPIKeys` (pushes per-server API keys from the config onto backends implementing the package-private `apiKeyConfigurable`). |
| `backend_http.go` | Shared HTTP helpers for backend API calls: `authedGet` / `authedPostJSON` attach `Authorization: Bearer <key>` when a per-server API key is configured, `authFailedErr` maps 401/403 to an actionable "check api_key" error, `redactAPIKeyArgs` masks `--api-key` values for display surfaces. `readBodyLimited` / `decodeJSONLimited` bound every response-body read from a probed server via `io.LimitReader` (`maxStatusBodyBytes` = 8 KiB for health/discrimination probes, error bodies, and drained load/unload responses; `maxJSONBodyBytes` = 1 MiB for model lists and `/props`), so a process squatting on a configured port cannot stream unbounded data within the client timeout. |
| `backend_llamacpp.go` | llama.cpp implementation: server arg assembly, Model path resolution, restart-per-Profile semantics ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)) — `LoadModel`/`UnloadModel` are no-ops. Registers via `init()`. |
| `backend_ollama.go` | Ollama implementation: HTTP API Model load/unload, auto-start via `ollama serve`, stop via the address-scoped PID path (`TryStop` is a no-op). Registers via `init()`. |
| `backend_lmstudio.go` | LM Studio implementation: HTTP API Model load/unload (forwards `context_size`, `batch_size`, `flash_attn` as `context_length`, `eval_batch_size`, `flash_attention`; the REST load endpoint has no GPU-offload field, so `gpu_layers` is not sent), `lms` CLI for server start/stop. Registers via `init()`. |
| `server.go` | LLM Server lifecycle. Unified start path (fork-and-detach for llamacpp; backend-supplied `TryStart` for Ollama/LM Studio), unified stop path that always tries to stop the process whether or not the launcher started it ([ADR-0001](docs/adr/0001-stop-is-unconditional.md)). `LoadProfile` orchestration with live idempotency check + drift notice from `GET /props` ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)) and unified `auto_unload` rule across same-server and cross-server cases ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)). Instances are keyed by `host:port` and rediscovered live each invocation ([ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)). `createLogPath` triggers automatic cleanup when `log_retention` is set. Lifecycle functions accept an optional `ProgressFunc` callback to report step transitions. |
| `discovery.go` | `RunningInstance` type and `DiscoverRunningInstances(cfg)` — probes every (backend, address) pair derivable from the config in parallel, returns the reachable set with the loaded Model and (for llamacpp) the live parameters from `/props`. Optional runtime details (PID via `lsof`, start time via `ps -o lstart=`, log path via the deterministic naming convention) are populated lazily by `fillRuntimeDetails`. `instancesSignature` condenses a discovery result into a comparable string (backend, address, loaded model per instance) used by the menu to detect background state changes between refresh ticks. |
| `log_cleanup.go` | Log file cleanup: `cleanupLogs` enumerates and deletes old `.log` files by filename timestamp, skipping active server logs. `parseLogTimestamp` extracts creation time from the `{backend}-{YYYYMMDD}-{HHMMSS}.log` naming convention. `formatBytes` for human-readable sizes. `autoCleanupLogs` wrapper for silent on-start cleanup. |
| `progress.go` | Step-by-step progress feedback for lifecycle operations. `ProgressFunc` callback type, `progressTracker` (TUI popup that updates in place), `newCLIProgress` (plain text fallback). |
| `ui.go` | Low-level terminal operations: raw mode (via `golang.org/x/term`), ANSI escape codes, key reading, reusable `selectMenu()` component. `selectMenu`'s header callback returns `(lines, stale)`; a stale report makes it return the `idxMenuStale` sentinel so callers rebuild instead of acting on an outdated item list. |
| `menu.go` | Interactive menu logic. Enumerates running instances; presents an instance picker for actions that apply to a non-unique target (stop, unload, logs). The open menu runs on two timers: the render tick (`menuTickInterval` — 1 second via `statusTickInterval` when the memory readout is shown, otherwise `refresh_duration`) re-renders the header so the memory/GPU readout stays current, while the backend probe inside `liveStatusHeaderFn` is throttled to `refresh_duration` (config reload + `DiscoverRunningInstances`; the cached discovery result is reused between probes). Each probe compares `instancesSignature` against the signature the menu was built from — on mismatch the open menu returns `errMenuStale` and the loop rebuilds, so background changes (a model loaded via the CLI in another terminal, an external stop or unload) surface without user input and without any state file. Config is also reloaded at the top of each menu loop iteration. `serverStatusLines` appends the optional memory/swap readout (see `sysmem.go` / `memformat.go`) gated by `show_memory_status`, rendered via `Config.CompiledMemoryTemplate()`; templates that carry their own styling (`MemoryTemplate.Styled()`) are emitted as-is, plain templates keep the legacy dim wrap. |
| `sysmem.go` | macOS unified-memory, swap, and GPU snapshot for the status header. `ReadMemStats` shells out to `sysctl -n hw.memsize`, `sysctl -n vm.swapusage`, `vm_stat`, and `ioreg -r -c IOAccelerator`, with a 0.9-second mutex-guarded cache — just below the menu's 1-second status tick so every tick reads fresh values while per-keystroke re-renders stay cheap. Free RAM follows the Activity Monitor "available" definition; `Compressed` is sourced from `vm_stat`'s "Pages occupied by compressor" line. GPU fields come from the `AGXAccelerator…` entry's `PerformanceStatistics` dict (`Device Utilization %`, `In use system memory`, `Alloc system memory`) on Apple Silicon and degrade silently to `0` on Intel Macs or ioreg failure — `parseIOAccelerator` returns zero values rather than erroring so the rest of the readout still renders. `FormatMemoryLine` is a one-shot convenience wrapper over the compiled-template engine in `memformat.go`; `percentValue`/`percentString` provide the rounded integer percentage (0 on a zero denominator). |
| `memformat.go` | Compiled-template engine for `memory_status_format`. `CompileMemoryTemplate` scans the template once (hand-rolled `{…}` tokenizer, no regex) into a segment list — literals (including pre-resolved ANSI escapes for style tags), value placeholders, and bar specs — and `MemoryTemplate.Render` walks the segments against a `MemStats` snapshot each tick (~0.3 µs, independent of template complexity). Compilation never fails: unknown or malformed tokens pass through literally. Style tags cover the 16 named ANSI colors ({gray} aliasing {bright-black}), 256-color palette indices ({0}–{255}), and 24-bit hex colors ({#rrggbb} / {#rgb}), plus {bold}/{dim}/{reset}; `memColor` resolves any of the three color forms to its foreground and background escapes (named: SGR fg+10; palette: 38;5→48;5; hex: 38;2→48;2). `MemoryTemplate.Styled()` reports whether the template carries its own styling, which disables the menu's legacy dim wrap. Bars (`{pct_name:bar[:width[:color[:bgcolor]]]}`, colors in any of the three forms) render full blocks plus an eighth-block partial cell (8 levels per cell) in the fill color, with the background color painted as an ANSI background behind the partial cell and the empty remainder so the strip is continuous; any nonzero percentage shows at least a sliver, widths clamp to 1–40. `Config.CompiledMemoryTemplate()` (config.go) memoizes compilation keyed on the format string and resolved `memory_status_bar` defaults, recompiling at most once per config reload. |
| `config_test.go` | Tests for config loading, validation (deprecated fields, server enable/disable, auto-assignment, `defaults.server` deprecation warning), parameter merging, boolean accessors, `ExpandTilde` edge cases, `ConfiguredBackendAddr`, `memory_status_bar` resolution (partial blocks, clamping, unknown-color warnings), and `CompiledMemoryTemplate` memoization. |
| `backend_llamacpp_test.go` | Tests for llama.cpp arg assembly (including `--api-key` placement), Model resolution, httptest-based health check, and auth header propagation. |
| `backend_ollama_test.go` | httptest-based tests for Ollama health check (body discrimination), `LoadModel`, `UnloadModel`, `ListRunningModels`, and auth header propagation. |
| `backend_lmstudio_test.go` | httptest-based tests for LM Studio health check (cross-backend exclusion), `LoadModel`, `UnloadModel`, `extractLMStudioError`, and auth header propagation (including the discrimination probes). |
| `backend_test.go` | Tests for `GetLLMServer` with known and unknown LLM Server names. |
| `backend_http_test.go` | Tests for `authedGet`/`authedPostJSON` (header present/absent, JSON content type), `authFailedErr`, `redactAPIKeyArgs`, `applyAPIKeys` (apply and clear on reload), and the bounded body reads (`readBodyLimited`/`decodeJSONLimited` stop at the cap, asserted via a counting reader). |
| `log_cleanup_test.go` | Tests for `parseLogTimestamp`, `formatBytes`, and `cleanupLogs` (empty dir, nonexistent dir, old/new file filtering, `--all` mode, non-log file safety). |
| `server_test.go` | Tests for `IsProcessAlive` (including PID 0 guard), `readLastLines`, `RunningInstance` methods (`Addr`, `Uptime`), `paramDrift`, and `shouldCrossServerUnload`. |
| `discovery_test.go` | httptest-based tests for `DiscoverRunningInstances` (reachable / unreachable), `LlamaCpp.ListRunningModels`, `LlamaCpp.QueryLiveParams` (`/props` populated and 404 fallback), and `findManagedLogFile` (most-recent picker by lexicographic timestamp). |
| `menu_test.go` | Tests for `parseChoice`, `formatUptime`, `profileDisplayName`, and GPU layers display formatting (shown for llamacpp, suppressed for lmstudio). |
| `helpers_test.go` | Shared test helper `addrFromURL` for extracting `host:port` from httptest server URLs. |

### 5.3 LLMServer Interface

```go
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

type ManagedLLMServer interface {
    LLMServer
    ServerBinary(cfg *Config) string
    BuildServerArgs(cfg *Config, profile *ResolvedProfile) []string
    BuildServerEnv(cfg *Config, profile *ResolvedProfile) []string
}

type PIDTracker interface {
    LastStartedPID() int
    LastStartedLogFile() string
}

type ModelLister interface {
    ListRunningModels(addr string) ([]RunningModelInfo, error)
}

type LiveParamsQuerier interface {
    QueryLiveParams(addr string) (*ProfileParams, error)
}
```

The interface name `LLMServer` matches the domain language in [CONTEXT.md](CONTEXT.md).

The `LLMServer.TryStart` / `LLMServer.TryStop` pair drives the unified lifecycle: each LLM Server type encapsulates its own start mechanism (fork-and-detach for llamacpp; `ollama serve` for Ollama; `lms server start` for LM Studio) and stop mechanism (signal-to-PID for llamacpp and Ollama via a no-op `TryStop`; `lms server stop` for LM Studio). The launcher does not branch on "did we start this?" — see [ADR-0001](docs/adr/0001-stop-is-unconditional.md).

`ManagedLLMServer` extends `LLMServer` for LLM Server types where the launcher knows how to fork the server process directly (currently only llamacpp). The server lifecycle code uses `if mb, ok := b.(ManagedLLMServer)` to decide whether to assemble argv and fork, or to call `LLMServer.TryStart`.

`PIDTracker` is implemented by LLM Servers that auto-start a server process and can report the resulting PID (Ollama). The launcher uses it for `status` display only; liveness and stop decisions go through `HealthCheck` and `lsof`, not PID.

`ModelLister` is implemented by LLM Servers that report their currently-loaded Models: llamacpp via `/v1/models`, Ollama via `/api/ps`, LM Studio via `/v1/models`. Discovery uses it on every invocation to know what is actually loaded (instead of relying on a persisted snapshot that can drift from reality when a Model is loaded externally).

`LiveParamsQuerier` is implemented by LLM Servers that report their currently-active parameters: llamacpp via `/props` (n_ctx, total_slots, default generation settings). Used by ADR-0007 drift detection — the launcher compares live params against the freshly resolved Profile instead of a persisted snapshot. Ollama and LM Studio do not implement this; on those backends, model-name match alone is the idempotency signal.

Per-server API keys do not appear in any interface signature: several call paths (`IsServerAlive`, `identifyBackend`, `WaitForHealth`, instance stop/unload) have no `*Config` in scope. Instead each backend struct holds an unexported `apiKey` field, set by `applyAPIKeys` at the end of `LoadConfig` (and thus refreshed on every `Reload`), and attaches it as a `Bearer` header via the helpers in `backend_http.go`. Health-check discrimination is unaffected: a key-protected llama-server still answers its auth-exempt `/health`, and it 401s LM Studio's `/v1/models` probe (a correct rejection); LM Studio's own probes carry the key so they keep working when its token requirement is enabled.

Adding a new LLM Server requires:
1. Create `backend_<name>.go` implementing `LLMServer` (and optionally `ManagedLLMServer`).
2. Register via `init()` calling `RegisterLLMServer()`.
3. Add the LLM Server name to `servers:` in the config YAML.
4. Set `server:` on relevant Profiles.

#### Health Check Discrimination

All backends may share the same address so that a single client configuration works regardless of which backend is active. Each backend's `HealthCheck` must therefore identify whether the responding server is actually its own, not another backend running on the same port.

| Backend | Primary Endpoint | Discrimination |
|---|---|---|
| `llamacpp` | `GET /health` → 200 | Body must parse as JSON with a non-empty `"status"` field (e.g. `{"status":"ok"}`). LM Studio returns 200 for all paths but with `{"error":"..."}` — the missing `"status"` field rejects it. |
| `lmstudio` | `GET /v1/models` → 200 | Excludes llamacpp (rejects if `/health` body has a `"status"` field) and Ollama (rejects if `/api/tags` body parses as JSON with a `"models"` field). LM Studio returns `{"error":"..."}` for both paths. |
| `ollama` | `GET /` → 200 | Body must contain "Ollama" (positive identification). |

When adding a new backend that shares an endpoint with an existing one (e.g. `/v1/models`), add an exclusion entry to any existing backend whose health check could false-positive, and ensure the new backend either uses a unique endpoint or performs its own exclusion checks.

**Technical reasoning — why body-based discrimination:**

LM Studio's built-in HTTP server returns HTTP 200 for *every* path, including paths it does not implement (`/health`, `/slots`, `/api/tags`, etc.). The response body for unrecognized paths is always `{"error":"Unexpected endpoint or method. (GET /path)"}`. This means status-code-only checks cannot distinguish LM Studio from any other backend — every probe returns 200.

Observed responses from LM Studio on a shared port:

| Path | Status | Body |
|---|---|---|
| `GET /v1/models` | 200 | Valid Model list JSON (real endpoint) |
| `GET /health` | 200 | `{"error":"Unexpected endpoint or method. (GET /health)"}` |
| `GET /slots` | 200 | `{"error":"Unexpected endpoint or method. (GET /slots)"}` |
| `GET /api/tags` | 200 | `{"error":"Unexpected endpoint or method. (GET /api/tags)"}` |
| `GET /` | 200 | `{"error":"Unexpected endpoint or method. (GET /)"}` |

Consequences for each backend's health check:

- **llamacpp** originally checked `GET /health` → 200 (status only). LM Studio also returns 200, so llamacpp falsely claimed LM Studio's server. Fix: require the body to contain `{"status":"..."}` — a field only llama-server produces.
- **lmstudio** originally excluded Ollama by checking if `GET /api/tags` → 200 (status only). LM Studio itself returns 200 for that path, so the exclusion falsely triggered and LM Studio rejected its own server. Fix: parse the body and require a `"models"` JSON field — present only in Ollama's real response.
- **ollama** was already body-based (requires "Ollama" in `GET /` body), so it was unaffected.

The general rule: when backends share a port, **all discrimination must be body-based**. Status codes alone are insufficient because some servers (LM Studio) return 200 for every path.

### 5.4 External Dependencies

| Module | Purpose |
|---|---|
| `gopkg.in/yaml.v3` | YAML config parsing |
| `golang.org/x/term` | Raw terminal mode for arrow-key menu navigation |

Standard library only beyond that. No TUI framework; ANSI escape codes are used directly for colors and screen control.

## 6. Process and Model Management

The launcher operates on **LLM Server instances**, each identified by its `host:port` address ([ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)). Multiple instances of any LLM Server type may run concurrently as long as each binds a distinct address.

### 6.1 Activating a Profile

`load <profile>` is the canonical entry point. It performs (in order):

1. Resolve the Profile (merge defaults, validate parameters, resolve Model path).
2. Compute the target address from the resolved Profile.
3. Probe the target address (`LLMServer.HealthCheck`).
4. **Idempotency check** ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)): if the target is healthy, ask the LLM Server which Model is loaded (`ModelLister.ListRunningModels`). If it matches the resolved Profile's Model:
   - For `llamacpp`, query `LiveParamsQuerier.QueryLiveParams` (`GET /props`) and diff against the freshly resolved Profile.
   - If no drift (or backend doesn't expose live params), exit silently (no-op).
   - If drift is detected, print a notice to stderr naming the divergent fields and pointing the user to `--restart`. Exit silently otherwise (no-op).
   - If `--restart` is given, fall through to step 5.
5. **`auto_stop_server` / `auto_unload`** ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)): discover every other running instance via `DiscoverRunningInstances`.
   - If `auto_stop_server: true` (default), stop instances whose address differs from the target's.
   - If `auto_stop_server: false`, leave them running. Then, regardless of `auto_stop_server`, if `auto_unload: true` (default), unload any Model on any still-running instance that is not the one we are about to load.
6. Start the LLM Server at the target address if it isn't running (§6.2).
7. Load the Model (§6.3).

### 6.2 Starting an LLM Server

The path forks on whether the backend implements `ManagedLLMServer`:

**`ManagedLLMServer` (llama.cpp):**

1. Resolve the backend; verify binary exists via `exec.LookPath`.
2. Build server arguments via `ManagedLLMServer.BuildServerArgs()` and environment via `BuildServerEnv()`. For llamacpp the Model path is in `-m`, so the Model is baked into the server's start arguments ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)).
3. Open the log file for stdout/stderr redirection.
4. Create `exec.Cmd` with `SysProcAttr{Setsid: true}` to detach the child process.
5. Call `cmd.Start()` (non-blocking).
6. Wait 500ms to detect early exit (port conflict, binary not found, etc.).
7. Wait for backend health check to succeed (up to 15 seconds).
8. Print confirmation. The active Model and parameters are observable on subsequent invocations via the LLM Server's own API.

**Plain `LLMServer` (Ollama, LM Studio):**

1. Resolve the address from `servers` map (if host:port value) or `LLMServer.DefaultAddr()`.
2. Call `LLMServer.HealthCheck(addr)` to verify server is reachable.
3. If not reachable, call `LLMServer.TryStart()` (e.g. `lms server start`, `ollama serve`).
4. Poll health check until successful (up to 15 seconds) or fail with a user-friendly message.
5. Print confirmation.

### 6.3 Loading a Model

**For `ManagedLLMServer` (llama.cpp):** Loading is fused with server start — the Model is in the start arguments and there is no separate API call. If a different Profile is already active at the target address, that instance is stopped first (the stop is unconditional — [ADR-0001](docs/adr/0001-stop-is-unconditional.md)) and a new server is started with the new Model. `LLMServer.LoadModel`/`UnloadModel` are no-ops on llamacpp.

**For plain `LLMServer` (Ollama, LM Studio):** Call `LLMServer.LoadModel(addr, resolvedProfile)`. This is an HTTP request to the server's load endpoint. The currently-loaded Model is observable on subsequent invocations via the LLM Server's own API (`/api/ps`, `/v1/models`).

### 6.4 Unloading a Model

`unload [profile]` always means "the Model is no longer loaded after this returns successfully."

- **llamacpp:** stops the server (the Model is part of the server's args — there is no API-level unload).
- **Ollama / LM Studio:** calls `LLMServer.UnloadModel` via HTTP. The server stays running with no Model loaded — visible on the next `status` invocation as a healthy server with no `active_model`.

The `auto_unload` flag governs whether an unload is *implicit* during a Profile activation (§6.1 step 5); the user-invoked `unload` subcommand is always explicit.

### 6.5 Stopping an LLM Server

`stop [target]` is unconditional ([ADR-0001](docs/adr/0001-stop-is-unconditional.md)) — the launcher does not distinguish servers it started from servers that were already running.

The launcher attempts both available mechanisms, in order:

1. Discover the listening PID via `lsof -nP -iTCP@host:port -sTCP:LISTEN -t` (host-specific first, then a port-only fallback for servers bound to `0.0.0.0`). If found and alive, send `SIGTERM` to the process and its process group (`kill(-pid, SIGTERM)`). Poll for exit (100ms intervals, up to 15 seconds). If still alive, send `SIGKILL`, then wait up to 5 seconds for the process to die, then 500ms for the OS to release the TCP port.
2. Call `LLMServer.TryStop(addr)` so the backend can run its native shutdown command (LM Studio's `lms server stop`). This is best-effort and idempotent — errors are reported to stderr but do not block, so the PID path above stays authoritative. llamacpp and Ollama have no native stop hook (`TryStop` is a no-op): the PID path above is the whole stop. Ollama deliberately does **not** shell out to `ollama stop <model>` — that only unloads a model from a still-running `ollama serve`, so it would not free the listener, and it is absent on older ollama versions.

Then print confirmation. There is no state file to remove.

**Technical reasoning — process group signals:**

Forked servers are started with `SysProcAttr{Setsid: true}`, which gives the child its own session and process group (PGID = PID). Sending `SIGTERM` to just the PID signals only the main process. If the server has spawned child processes (e.g. worker threads for CUDA/Metal, Model loading), those children may keep the main process alive or hold resources. Sending the signal to the entire process group via `syscall.Kill(-pid, sig)` ensures all children also receive it. Errors from the group signal are ignored (the group may not exist if the process already exited).

**Technical reasoning — SIGKILL port release wait:**

After `SIGKILL`, the stop path must wait for the process to actually die before returning. Without this wait, the TCP port may still be held by the dying process when the next backend tries to start on the same port, causing a 15-second health check timeout ("server did not become healthy within 15s"). The implementation polls `IsProcessAlive` for up to 5 seconds after SIGKILL, then waits an additional 500 ms (`startupGracePeriod`) for the OS to release the TCP socket in the `TIME_WAIT` / cleanup phase.

### 6.6 Status Check

1. Call `DiscoverRunningInstances(cfg)` — probes every (backend, address) pair derivable from the config and returns the reachable set with the loaded Model and live params for each.
2. Print one row per live instance with: backend, address, active Profile (matched against config by backend + address + model), active Model. PID, uptime, and log file are populated lazily via `lsof`, `ps -o lstart=`, and a glob of `{log_dir}/{backend}-*.log`.

Exit 0 if any instance is running; exit 1 if all are stopped.

### 6.7 Stale State Handling

There is no state. Each invocation is a fresh look at the live LLM Servers. Health checks are discriminating — each backend identifies its own server and rejects responses from other backends sharing the same address (see [§5.3 Health Check Discrimination](#health-check-discrimination)). A server that crashed since the last invocation simply isn't in the discovered set; a Model loaded externally on Ollama or LM Studio is.

### 6.8 `auto_stop_server` and `auto_unload`

These two flags determine what happens to *other* running instances when a Profile is activated.

| `auto_stop_server` | `auto_unload` | Behaviour for instances *other than* the activation target |
|---|---|---|
| `true` (default) | (any) | All other instances are stopped. |
| `false` | `true` (default) | Other instances stay running. Any Model loaded on them is unloaded (`LLMServer.UnloadModel`) — same rule for same-server swap and cross-server case ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)). |
| `false` | `false` | Other instances stay running with their Models intact. |

For llamacpp, `auto_unload` is silently ignored on llamacpp instances (Model swap requires a server restart — [ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)).

## 7. Runtime Discovery

The launcher persists nothing between invocations. Every command reconstructs the set of running instances live, by probing the addresses derivable from the user's `config.yaml` and querying each reachable LLM Server's own API. This is what makes the tool resilient to external changes (a crashed server, a Model loaded by `ollama run` outside the launcher, a config edit between invocations) without a cache to drift out of sync.

### 7.1 The RunningInstance Record

```go
type RunningInstance struct {
    Backend       string
    Host          string
    Port          int
    PID           int               // optional, via lsof
    StartedAt     time.Time         // optional, via ps -o lstart=
    LogFile       string            // optional, via log-dir glob
    ActiveProfile string            // matched against config
    ActiveModel   string            // from backend's ModelLister
    ResolvedParams ProfileParams    // from backend's LiveParamsQuerier
}
```

The struct is transient — built fresh in memory on each invocation, never serialised. `PID`, `StartedAt`, and `LogFile` are best-effort fields populated by `fillRuntimeDetails` only when a command needs them (status display, log tailing).

### 7.2 DiscoverRunningInstances

`DiscoverRunningInstances(cfg)` enumerates every (backend, address) pair derivable from the config:

- Each enabled backend's configured address (`cfg.ConfiguredBackendAddr(name)`).
- Each Profile's resolved address (`host:port` from the merged Profile params), so Profiles that bind a backend to a non-default port are still discovered.

It probes them all in parallel with `LLMServer.HealthCheck`. For every reachable address it then asks the backend:

- `ModelLister.ListRunningModels` → the currently loaded Model (`/v1/models` for llamacpp / LM Studio, `/api/ps` for Ollama).
- `LiveParamsQuerier.QueryLiveParams` → the active server parameters (llamacpp `/props`; not implemented elsewhere).

The result is matched back to the config by `matchProfileName` — among Profiles whose backend and address equal the discovered instance, an exact resolved-model-path match wins; failing that, a basename match (`modelNamesMatch`). The fallback exists because servers started outside the launcher report the model as whatever path or alias they were launched with — llama-server defaults the id to the model file's basename — so the full resolved path rarely equals the reported name. The same helper drives the `LoadProfile` idempotency check (ADR-0007). Ambiguity (several equally good matches) yields no match. When no Profile matches, the field is empty; the launcher still shows the running Model and address.

### 7.3 Legacy Cleanup

On first run after upgrade, `CleanupLegacyStateFiles` (called once at CLI startup, behind `sync.Once`) deletes any leftover `state-*.json` and `state.json` files in `~/.config/llama-launcher/`. The cleanup is silent and best-effort — these files are no longer read or written, so failure to remove them has no functional impact.

## 8. Server Argument Assembly

The launcher builds the `llama-server` command line from the merged default parameters. The Model is baked into the start arguments via `-m`, so each Profile activation produces a fresh server with that Profile's hardware parameters honoured ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)).

| Config Field | Flag |
|---|---|
| `model` | `-m` |
| `gpu_layers` | `-ngl` |
| `threads` | `-t` |
| `threads_batch` | `-tb` |
| `batch_size` | `-b` |
| `context_size` | `-c` |
| `host` | `--host` |
| `port` | `--port` |
| `flash_attn` (true) | `-fa` |
| `mlock` (true) | `--mlock` |
| `no_mmap` (true) | `--no-mmap` |
| `cont_batching` (true) | `-cb` |
| `parallel` | `-np` |
| `embedding` (true) | `--embedding` |
| `temperature` | `--temp` |
| `repeat_penalty` | `--repeat-penalty` |
| `top_k` | `--top-k` |
| `top_p` | `--top-p` |
| `min_p` | `--min-p` |
| `models_dir` | `--models-dir` |

Boolean flags are only appended when the resolved value is `true`. Numeric flags are only appended when explicitly set (not nil after merge).

Argument assembly is delegated to the backend via `ManagedLLMServer.BuildServerArgs()`, allowing each `ManagedLLMServer` to map config fields to its own CLI flags.

## 9. Log Management

Server stdout and stderr are redirected to a log file at:

```
<log_dir>/<backend>-<YYYYMMDD>-<HHMMSS>.log
```

Example: `~/.config/llama-launcher/logs/llamacpp-20260519-171200.log`

The `logs` subcommand tails the log file of a launcher-managed running instance. The path is reconstructed deterministically by globbing `{log_dir}/{backend}-*.log` and picking the most recent — log filenames embed the start timestamp so lexicographic order is chronological. Externally-started servers log to wherever they were started; `llml logs` prints a clear message in that case rather than guessing. With `--follow`, the launcher uses `tail -f` and is the only mode where it remains running.

### 9.1 Log Cleanup

Old log files can be cleaned up manually or automatically:

- **Manual:** `logs clean` deletes files older than 7 days (default). `--days N` overrides the threshold; `--all` removes everything. Reports files removed and space freed.
- **Automatic:** Setting `log_retention: N` in config causes `createLogPath` to silently delete files older than N days before each new log is created. No output during automatic cleanup.

Both paths use `cleanupLogs()`, which determines file age from the filename timestamp (not mtime) and always skips log files belonging to running servers (checked via `DiscoverRunningInstances` + `fillRuntimeDetails` so the live log path of each instance is known and protected).

## 10. Error Handling

| Scenario | Behaviour |
|---|---|
| Config file missing (first run) | Generate example config, print path, exit 2. |
| Config file parse error | Print error with line number (from yaml.v3), exit 2. |
| Profile missing `server:` with no defensible fallback | Print warning (deprecation notice) or error (if no fallback is defensible). See [§4.6](#46-llm-server-selection). |
| Unknown Profile name | Print error, exit 2. |
| Model file not found | Print resolved path, exit 2. |
| Server binary not found | Print configured path, exit 3. |
| Server already running, same Profile, no drift | No-op. Exit 0. |
| Server already running, same Profile name, parameters drifted | Print drift notice to stderr; no-op unless `--restart`. Exit 0. |
| No server running (on `stop`/`unload`) | Print message, exit 1. |
| Failed to start process | Print OS error, exit 3. |
| Server health timeout | Print timeout message, exit 3. |
| Model load/unload API error | Print server response, exit 3. |
| SIGTERM timeout (on `stop`) | Escalate to SIGKILL, warn, exit 0 (server is stopped). |
| `lsof` not on PATH (stop path) | Print message that the listening PID could not be determined, exit 3. |
| Port already in use | Detected via early server exit — the launcher checks if the process is still alive ~500 ms after start and reports the log tail if it died. |

## 11. Future Considerations

These are explicitly out of scope for v1 but noted as natural extensions:

- **Per-instance log file naming**: `{backend}-{port}-{timestamp}.log` rather than `{backend}-{timestamp}.log`. Today multiple instances of the same backend coexist because timestamps differ at second resolution; revisit only if collisions become real.
- **Shell completions**: Generate bash/zsh/fish completions for subcommands and Profile names.
- **Config reload subcommand**: A `reload` subcommand that restarts the matching instance with the same Profile using updated config values. (Note: automatic config reload in the interactive menu is already implemented — this item covers the CLI subcommand.)
- **Additional LLM Servers**: vLLM and others — each as a new `backend_<name>.go` file implementing the `LLMServer` interface.
- **Homebrew formula**: Package for `brew install llama-launcher`.
- **Launchd integration**: Generate a launchd plist for auto-start on login.
- **Per-Profile log retention**: Today log retention is global; a per-Profile `log_retention` would let chatty debug Profiles keep more history without inflating storage for everything.

## 12. Testing

### 12.1 Unit Tests (httptest)

Backend methods are tested using `net/http/httptest` mock servers. These tests run as part of `go test ./...` with no external dependencies.

| Test | What it covers |
|---|---|
| `TestLlamaCppHealthCheck` | 200 on `/health` with `{"status":"ok"}` body → success; non-llamacpp body (missing `status` field) → rejects; non-200 → error; unreachable → error; a body larger than the read cap → rejected (the bounded read truncates it). |
| `TestOllamaHealthCheck` | 200 with "Ollama" body → success; empty body → error; non-Ollama body → error; non-200 → error; "Ollama" marker sitting past the read cap → error (the bounded read never sees it). |
| `TestLMStudioHealthCheck` | 200 on `/v1/models` → success when `/health` body lacks `status` field; healthy when LM Studio returns `{"error":"..."}` for `/health` and `/api/tags`; detects llamacpp via `/health` body containing `{"status":"ok"}`; detects Ollama via `/api/tags` body containing `{"models":[...]}`; non-200 → error; unreachable → error. |
| `TestLMStudioLoadModel` | Success, context_length inclusion, batch_size/flash_attn mapped to `eval_batch_size`/`flash_attention`, unset params and gpu_layers omitted from the payload, error with message, error without message. |
| `TestLMStudioUnloadModel` | Success, non-200 with error message, non-200 with empty body returns error. |
| `TestExtractLMStudioError` | Valid JSON, empty body, malformed JSON, missing message field. |
| `TestOllamaLoadModel` | Success (verifies keep_alive payload), error status. |
| `TestOllamaUnloadModel` | Success (verifies keep_alive=0), error status. |
| `TestOllamaListRunningModels` | Success with models, empty list, malformed JSON. |
| `TestLMStudioListRunningModels_OversizedBody` | A `/v1/models` response larger than the read cap fails to parse instead of being consumed in full. |
| `TestLlamaCppListRunningModels` | `/v1/models` parsing — single-entry `data` array with `id` populated. |
| `TestLlamaCppQueryLiveParams` | `/props` parsing populates `ContextSize`, `Parallel`, generation settings; `404` returns `(nil, nil)` so paramDrift treats it as "no drift". |

### 12.2 Server & Config Tests

| Test | What it covers |
|---|---|
| `TestIsProcessAlive` | Current PID → true; PID 0 → false; negative PID → false; invalid PID → false. |
| `TestReadLastLines` | More lines than requested; fewer lines; nonexistent file. |
| `TestRunningInstance_Addr` / `_Uptime` / `_Uptime_ZeroStart` | Instance helper methods including the zero-StartedAt fallback. |
| `TestDiscoverRunningInstances_*` | Discovery returns the empty set when nothing listens; an httptest llama-server stand-in is found with `ActiveModel` and `ResolvedParams.ContextSize` populated from `/v1/models` and `/props`. |
| `TestFindManagedLogFile` | Most-recent file picked by lexicographic timestamp; filters by backend prefix; returns empty when no matching file exists. |
| `TestParamDrift` | Identical params, bool/float comparisons, slot-identity fields skipped; nil on either side (a field the backend does not report) is skipped, not drift — a live set carrying only the `/props` subset against a fully populated profile yields no drift, while a changed shared field still does. |
| `TestShouldCrossServerUnload` | Decides whether to issue an unload on a discovered instance during cross-server `auto_unload`. |
| `TestGetLLMServer` | Known LLM Server names return correct instance; unknown returns error. |
| `TestExpandTilde` | `~/path`, bare `~`, `~username` (unchanged), absolute path, empty. |
| `TestLoadConfig` | Missing file, valid config, no-profiles validation. |
| `TestValidate_*` | Deprecated fields, no servers enabled, auto-assign default server, `defaults.server` deprecation warning. |
| `TestShouldAutoClose` / `TestShouldDisplayCentered` | Nil-defaults-to-true/false asymmetry. |
| `TestConfiguredBackendAddr` | Returns merged address with colon separator. |

### 12.3 Menu Helper Tests

| Test | What it covers |
|---|---|
| `TestParseChoice` | Valid, zero, negative, exceeds max, non-numeric, empty. |
| `TestFormatUptime` | Hours, minutes, seconds-only branches. |
| `TestProfileDisplayName` | With title, fallback to Profile name, unknown Profile. |
| `TestFormatProfileParams_GPULayers_LMStudio` | lmstudio profiles render no GPU line (the REST load endpoint has no GPU-offload field). |
| `TestFormatProfileParams_GPULayers_LlamaCpp` | llamacpp profiles show the configured GPU layers value. |

### 12.4 Test Helpers

`helpers_test.go` provides `addrFromURL(t, rawURL) string`, which parses an `httptest.NewServer` URL and returns the `host:port` portion for passing to backend methods that expect an `addr` string.

## 13. Build and Installation

The version number lives in the root `VERSION` file and is injected at build time via `ldflags` into `launcher.Version`.

```bash
make build          # builds ./llama-launcher binary (version injected from VERSION file)
make build-mcp      # builds ./llama-launcher-mcp, the optional control-plane adapter (§15)
make install        # builds + copies to ~/.local/bin, adds to PATH if needed
make clean          # removes binaries

go test ./...       # run all tests
go test ./internal/launcher/ -run TestMergeParams  # run a single test
go vet ./...        # static analysis
```

The binary is statically linked (default for Go on macOS with CGO_ENABLED=0) and has no external dependencies at runtime.

### Homebrew

The published formula (`airiclenz/tap/llama-launcher`) builds from the release source tarball and installs **both** binaries: the `llama-launcher` CLI and the optional `llama-launcher-mcp` control-plane adapter (§15). The two builds inject the version with different `ldflags` targets — `internal/launcher.Version` for the CLI and `main.Version` for the adapter — because the adapter lives in `package main` under `cmd/llama-launcher-mcp/`. Packaging-only changes that don't bump `VERSION` (e.g. starting to ship the adapter from an already-tagged release) are released as a formula `revision` bump rather than a new tag.

## 14. Coding Standards

Follow `skills/coding-standards/SKILL.md` when writing or modifying code. Read the base references and the Go-specific extensions before making changes.

### After Changing Code

1. Update the documents `llama-launcher.TDD.md`, `README.md`, `CHANGELOG.md`, and `TODO.md` if the change affects behavior, configuration schema, subcommands, error handling, or any other aspect covered here.
2. If the change touches one of the architectural decisions in [docs/adr/](docs/adr/), update or supersede the relevant ADR in the same change.
3. Run `make install` to build and install the updated binary.

## 15. Optional MCP Control-Plane Adapter

`llama-launcher` itself never opens a socket — that property is load-bearing for [ADR-0002](docs/adr/0002-not-a-router.md). Remote control (e.g. a coding agent in a container deciding which Model the host runs) is provided by a **separate, optional binary**, `llama-launcher-mcp`, living under `cmd/llama-launcher-mcp/`. The rationale and trust model are pinned in [ADR-0008](docs/adr/0008-mcp-control-plane-adapter.md); this section describes the implementation.

### 15.1 Shape

The adapter is a thin shim: it runs on the host, exposes an MCP server over Streamable HTTP (via `github.com/modelcontextprotocol/go-sdk`), and implements every tool by **shelling out to the installed `llama-launcher` CLI** and returning its output. It holds no Models and parses no inference requests — it forwards the same control commands a human or the `manage-llm-server` skill drives. The new dependency is scoped to this binary; the core CLI build does not import it.

It ships with the CLI: `make build-mcp` builds it locally, and the Homebrew formula installs it alongside `llama-launcher` (§13). It is inert until started — installing it adds no resident process.

The HTTP listener sets connection timeouts so a stuck or hostile client cannot hold it open indefinitely: `ReadHeaderTimeout` 10 s, `IdleTimeout` 2 min, and `WriteTimeout` 10 min — the write window is generous because it must outlast the slowest tool call (`load_profile` waits up to 5 minutes for a model load, plus health-check and stop grace periods).

### 15.2 Tool surface

Each tool maps 1:1 to an existing subcommand (`internal/launcher/cli.go`):

| Tool | CLI invocation | Kind |
|------|----------------|------|
| `list_profiles` | `list --json` | read |
| `server_status` | `status --json` | read |
| `tail_log {target?}` | `logs [target]` | read |
| `load_profile {name, restart?}` | `load <name> [--restart]` | mutate |
| `unload_model {profile?}` | `unload [profile]` | mutate |
| `start_server {profile?}` | `start [--profile p]` | mutate |
| `stop_server {target?}` | `stop [target]` | mutate |

The mutating tools are registered only when `--read-only` is not set. Judgment that needs context (e.g. "never swap mid-simulation") stays with the agent via the skill; the adapter exposes the tools plainly.

**Result mapping.** stdout is returned as the tool's text content. A non-zero exit with empty stdout is flagged as a tool error carrying stderr. A non-zero exit that still printed stdout (e.g. `status --json` reports exit 1 when nothing is running but still emits the JSON array, per [§3.3](#33-exit-codes)) is returned as normal content so the caller keeps the data. Each captured stream (stdout and stderr) is capped at 1 MiB: content past the cap is dropped and a `[output truncated: 1MiB cap reached]` notice is appended, so a runaway subprocess cannot grow the adapter's memory or the MCP response without bound.

### 15.3 Access control

The driving constraint is that the remote client may be a cloud LLM agent that must not be handed credentials. The adapter therefore uses a **source-IP allowlist**, not a token:

- `--listen host:port` — bind the container-facing bridge interface, **not** `0.0.0.0`.
- `--allow ip|cidr|host` — repeatable; a request whose source IP is not matched gets `403`. A hostname is resolved to its addresses **once at startup** (each becomes an exact-IP matcher, so the request-time check stays a numeric comparison); restart the adapter if the container's IP changes, or allow its subnet as a CIDR. Note a hostname may resolve to a *public* address (e.g. `devbox.dev` is a real domain, not the local container) — prefer a private CIDR or `--allow-interface`.
- `--allow-interface name` — repeatable; allow the network of every address bound to a local interface (e.g. `bridge100`, the container-facing bridge). Each address's CIDR (`ip.Mask(mask)` + mask) becomes a subnet matcher, so any IP the bridge assigns the container is covered without the operator knowing or pinning it, and a private bridge subnet can never collide with a public hostname. The interface is read once at startup (`interfaceAddrs`, a package var so tests can stub it); an unknown interface is a fatal startup error.
- `--llama-launcher-bin path` / `--config path` — which CLI binary and config the adapter drives (config is forwarded as `--config` on every call).
- `--read-only` — register only the read tools.

`resolveAllowlist` combines the `--allow` specs and `--allow-interface` networks; the loopback default applies **only when neither is given**, so naming an interface (or any `--allow`) drops the implicit loopback. The IP check (`allowlistMiddleware`) is defense-in-depth on top of the narrow bind, not a substitute for it.
