# llama-launcher — Technical Design Specification

The architectural source of truth for `llama-launcher` lives in two places:

- **[CONTEXT.md](CONTEXT.md)** — domain language (LLM Server, Model, Profile; Activate, Load/Unload, Start/Stop).
- **[docs/adr/](docs/adr/)** — numbered Architectural Decision Records (ADRs 0001–0007) that pin down behaviour.

This document explains how those decisions are realised in code. Where this document and an ADR appear to conflict, the ADR wins; please file a doc fix.

## 1. Overview

`llama-launcher` is a lightweight Go CLI tool for managing LLM Servers through named configuration Profiles. It starts an LLM Server as a detached background process (or connects to one already running), asks it to load a Model, persists per-instance state, and exits — consuming zero resident memory while the server runs. Subsequent invocations read the state files to find running instances and operate on them.

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
- HTTP server, API proxying, or request routing (see [ADR-0002](docs/adr/0002-not-a-router.md)).
- Cross-platform support beyond macOS (Linux would work but is not a design target).

## 3. User Experience

### 3.1 Interactive Mode

Running `llama-launcher` with no arguments enters a one-shot interactive menu with arrow-key navigation, colored output, and full-screen repainting. Falls back to numbered input when stdin is not a terminal. The config file is automatically reloaded before each menu display and on every 10-second header refresh, so changes made via "Edit config" or an external editor take effect without restarting. If the reloaded file is invalid, the last good config is silently preserved.

**When no server is running:**

```
    llama-launcher v1.0.0

    Status  ● stopped

    ▸ code-deepseek     DeepSeek Coder V2 Lite — coding tasks
      chat-qwen         Qwen 2.5 32B — general conversation
      reasoning-phi     Phi-4 — structured reasoning
      ─
      Start server only

    ↑↓ select · enter start & load · q quit
```

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
    Model    code-deepseek (deepseek-coder-v2-lite-Q4_K_M.gguf)
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

    ▸ code-deepseek     DeepSeek Coder V2 Lite — coding tasks
      chat-qwen         Qwen 2.5 32B — general conversation
      reasoning-phi     Phi-4 — structured reasoning
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
| `llama-launcher status [--json]` | Print one row per running LLM Server instance (address, backend, active Profile/Model, PID if known, uptime). Exit code 0 if any running, 1 if all stopped. With `--json`, emits a JSON array with one entry per enabled configured backend (`backend`, `running`, `address`, `active_profile`, `active_model`, `pid`, `uptime_seconds`); same exit code semantics. |
| `llama-launcher list [--json]` | Print all configured Profiles with descriptions and target LLM Server. With `--json`, emits a JSON array with one entry per Profile (`name`, `description`, `backend`, `model`, plus `gpu_layers` and `context_size` when set). |
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
servers:
  llamacpp: true
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

The `servers` map lists available LLM Servers. Keys are server names (`llamacpp`, `ollama`, `lmstudio`). Values are booleans: `true` enables the server, `false` disables it. Disabled servers are hidden from status display, and Profiles targeting a disabled server are excluded from menus, CLI output, and Profile resolution. At least one server must be enabled.

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

Each Profile may set `is_favourite: true` to pin it to the top of the menu and `list` output. Profiles are sorted by three keys in order: favourite status first (favourites before non-favourites), then by server (alphabetically), then by Profile name alphabetically. Favourite Profiles display a `★` marker right-aligned at the end of the row, in the same column across the entire list (descriptions are padded so the marker column is consistent). When no Profile in the listing is starred, no marker column is rendered. This ordering and rendering are produced by `Config.ProfileNames()` together with `buildProfileItems`/`buildSimpleProfileLines`/`cmdList`, and apply to every UI surface that lists Profiles (TUI menu, non-terminal fallback, `llama-launcher list`).

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
                     └──────┬───────┘     ┌──────┴────────────────┐
                            │             │  Per-instance state    │
              ┌─────────────┼──────────┐  │ state-{backend}-       │
              │             │          │  │   {port}.json          │
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
| `config.go` | Config/Profile/ProfileParams struct definitions, YAML loading (`parseConfig` for parse-only, `LoadConfig` for parse+validate), `Reload` for in-place re-read, `~` expansion, parameter merging, validation (`validate` for fast-fail, `validateAll` for collecting all problems including non-fatal warnings such as `defaults.server` fallback usage), example config generation. Server enable/disable filtering via `IsServerEnabled()`. |
| `defaults/config.yaml` | Example config template, embedded at compile time via `go:embed`. |
| `defaults/embed.go` | Embeds `config.yaml` and exports it as `defaults.ExampleConfig`. |
| `backend.go` | `LLMServer` and `ManagedLLMServer` interface definitions (see [§5.3](#53-llmserver-interface)), `ResolvedProfile` struct, LLM Server registry (register/get). |
| `backend_llamacpp.go` | llama.cpp implementation: server arg assembly, Model path resolution, restart-per-Profile semantics ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)) — `LoadModel`/`UnloadModel` are no-ops. Registers via `init()`. |
| `backend_ollama.go` | Ollama implementation: HTTP API Model load/unload, auto-start via `ollama serve`, stop via `ollama stop`. Registers via `init()`. |
| `backend_lmstudio.go` | LM Studio implementation: HTTP API Model load/unload, `lms` CLI for server start/stop. Registers via `init()`. |
| `server.go` | LLM Server lifecycle. Unified start path (fork-and-detach for llamacpp; backend-supplied `TryStart` for Ollama/LM Studio), unified stop path that always tries to stop the process whether or not the launcher started it ([ADR-0001](docs/adr/0001-stop-is-unconditional.md)). `LoadProfile` orchestration with idempotency check + drift notice ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)) and unified `auto_unload` rule across same-server and cross-server cases ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)). Per-instance state files keyed by `host:port` ([ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)). `createLogPath` triggers automatic cleanup when `log_retention` is set. Lifecycle functions accept an optional `ProgressFunc` callback to report step transitions. |
| `log_cleanup.go` | Log file cleanup: `cleanupLogs` enumerates and deletes old `.log` files by filename timestamp, skipping active server logs. `parseLogTimestamp` extracts creation time from the `{backend}-{YYYYMMDD}-{HHMMSS}.log` naming convention. `formatBytes` for human-readable sizes. `autoCleanupLogs` wrapper for silent on-start cleanup. |
| `progress.go` | Step-by-step progress feedback for lifecycle operations. `ProgressFunc` callback type, `progressTracker` (TUI popup that updates in place), `newCLIProgress` (plain text fallback). |
| `ui.go` | Low-level terminal operations: raw mode (via `golang.org/x/term`), ANSI escape codes, key reading, reusable `selectMenu()` component. |
| `menu.go` | Interactive menu logic. Enumerates running instances; presents an instance picker for actions that apply to a non-unique target (stop, unload, logs). Config is reloaded at the top of each menu loop iteration and on every 10-second header refresh. |
| `config_test.go` | Tests for config loading, validation (deprecated fields, server enable/disable, auto-assignment, `defaults.server` deprecation warning), parameter merging, boolean accessors, `ExpandTilde` edge cases, and `ConfiguredBackendAddr`. |
| `backend_llamacpp_test.go` | Tests for llama.cpp arg assembly, Model resolution, and httptest-based health check. |
| `backend_ollama_test.go` | httptest-based tests for Ollama health check (body discrimination), `LoadModel`, `UnloadModel`, and `ListRunningModels`. |
| `backend_lmstudio_test.go` | httptest-based tests for LM Studio health check (cross-backend exclusion), `LoadModel`, `UnloadModel`, and `extractLMStudioError`. |
| `backend_test.go` | Tests for `GetLLMServer` with known and unknown LLM Server names. |
| `log_cleanup_test.go` | Tests for `parseLogTimestamp`, `formatBytes`, and `cleanupLogs` (empty dir, nonexistent dir, old/new file filtering, `--all` mode, non-log file safety). |
| `server_test.go` | Tests for `IsProcessAlive` (including PID 0 guard), `readLastLines`, instance state path construction, `ServerState` methods, state migration, and state file permissions. |
| `menu_test.go` | Tests for `parseChoice`, `formatUptime`, `profileDisplayName`, and GPU offload display formatting. |
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
```

The interface name `LLMServer` matches the domain language in [CONTEXT.md](CONTEXT.md). String fields on persisted records (`ServerState.Backend`, `Profile.Backend` in the legacy-config detector) keep their existing names so on-disk state files and YAML migration paths stay compatible.

The `LLMServer.TryStart` / `LLMServer.TryStop` pair drives the unified lifecycle: each LLM Server type encapsulates its own start mechanism (fork-and-detach for llamacpp; `ollama serve` for Ollama; `lms server start` for LM Studio) and stop mechanism (signal-to-PID, `ollama stop`, `lms server stop`). The launcher does not branch on "did we start this?" — see [ADR-0001](docs/adr/0001-stop-is-unconditional.md).

`ManagedLLMServer` extends `LLMServer` for LLM Server types where the launcher knows how to fork the server process directly (currently only llamacpp). The server lifecycle code uses `if mb, ok := b.(ManagedLLMServer)` to decide whether to assemble argv and fork, or to call `LLMServer.TryStart`.

`PIDTracker` is implemented by LLM Servers that auto-start a server process and can report the resulting PID (Ollama). The launcher records the PID and log file on the per-instance state record; PID is informational (used for `status` display and log file association) and is **not** used to decide whether `stop` should kill the process.

`ModelLister` is implemented by LLM Servers that can enumerate loaded Models (Ollama via `/api/ps`), used by the status display.

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
3. Look up any existing instance state for that address.
4. **Idempotency check** ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)): if the same Profile name is already active at that address:
   - If the recorded resolved parameters match the freshly resolved Profile, exit silently (no-op).
   - If parameters have drifted, print a notice to stderr naming the divergent fields and pointing the user to `--restart`. Exit silently otherwise (no-op).
   - If `--restart` is given, fall through to step 5.
5. **`auto_stop_server` / `auto_unload`** ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)): iterate every other running instance.
   - If `auto_stop_server: true` (default), stop instances whose address differs from the target's.
   - If `auto_stop_server: false`, leave them running. Then, regardless of `auto_stop_server`, if `auto_unload: true` (default), unload any Model on any still-running instance that is not the one we are about to load.
6. Start the LLM Server at the target address if it isn't running (§6.2).
7. Load the Model (§6.3).
8. Update the per-instance state file (§7).

### 6.2 Starting an LLM Server

The path forks on whether the backend implements `ManagedLLMServer`:

**`ManagedLLMServer` (llama.cpp):**

1. Resolve the backend; verify binary exists via `exec.LookPath`.
2. Build server arguments via `ManagedLLMServer.BuildServerArgs()` and environment via `BuildServerEnv()`. For llamacpp the Model path is in `-m`, so the Model is baked into the server's start arguments ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)).
3. Open the log file for stdout/stderr redirection.
4. Create `exec.Cmd` with `SysProcAttr{Setsid: true}` to detach the child process.
5. Call `cmd.Start()` (non-blocking).
6. Write the per-instance state file with PID, backend, host, port, and timestamp. Model fields (`active_profile`, `active_model`, resolved-params snapshot) are not written yet.
7. Wait 500ms to detect early exit (port conflict, binary not found, etc.).
8. Wait for backend health check to succeed (up to 15 seconds).
9. Write Model fields and resolved-params snapshot to the state file only after the health check succeeds.
10. Print confirmation.

**Plain `LLMServer` (Ollama, LM Studio):**

1. Resolve the address from `servers` map (if host:port value) or `LLMServer.DefaultAddr()`.
2. Call `LLMServer.HealthCheck(addr)` to verify server is reachable.
3. If not reachable, call `LLMServer.TryStart()` (e.g. `lms server start`, `ollama serve`).
4. Poll health check until successful (up to 15 seconds) or fail with a user-friendly message.
5. Write the per-instance state file. If the backend implements `PIDTracker` and reported a PID, record it; otherwise PID is 0. PID is informational only.
6. Print confirmation.

### 6.3 Loading a Model

**For `ManagedLLMServer` (llama.cpp):** Loading is fused with server start — the Model is in the start arguments and there is no separate API call. If a different Profile is already active at the target address, that instance is stopped first (the stop is unconditional — [ADR-0001](docs/adr/0001-stop-is-unconditional.md)) and a new server is started with the new Model. `LLMServer.LoadModel`/`UnloadModel` are no-ops on llamacpp.

**For plain `LLMServer` (Ollama, LM Studio):** Call `LLMServer.LoadModel(addr, resolvedProfile)`. This is an HTTP request to the server's load endpoint. Update Model fields in the per-instance state file only after `LoadModel` returns success.

### 6.4 Unloading a Model

`unload [profile]` always means "the Model is no longer loaded after this returns successfully."

- **llamacpp:** stops the server (the Model is part of the server's args — there is no API-level unload).
- **Ollama / LM Studio:** calls `LLMServer.UnloadModel` via HTTP. The server stays running with no Model loaded; the per-instance state file's `active_profile` and `active_model` are cleared.

The `auto_unload` flag governs whether an unload is *implicit* during a Profile activation (§6.1 step 5); the user-invoked `unload` subcommand is always explicit.

### 6.5 Stopping an LLM Server

`stop [target]` is unconditional ([ADR-0001](docs/adr/0001-stop-is-unconditional.md)) — the launcher does not distinguish servers it started from servers that were already running.

The launcher attempts both available mechanisms, in order:

1. If a PID is recorded and alive, send `SIGTERM` to the process and its process group (`kill(-pid, SIGTERM)`). Poll for exit (100ms intervals, up to 15 seconds). If still alive, send `SIGKILL`, then wait up to 5 seconds for the process to die, then 500ms for the OS to release the TCP port.
2. Call `LLMServer.TryStop(addr)` so the backend can run its native shutdown command (`ollama stop`, `lms server stop`). This is best-effort and idempotent — errors are reported but do not block.

Then remove the per-instance state file and print confirmation.

**Technical reasoning — process group signals:**

Forked servers are started with `SysProcAttr{Setsid: true}`, which gives the child its own session and process group (PGID = PID). Sending `SIGTERM` to just the PID signals only the main process. If the server has spawned child processes (e.g. worker threads for CUDA/Metal, Model loading), those children may keep the main process alive or hold resources. Sending the signal to the entire process group via `syscall.Kill(-pid, sig)` ensures all children also receive it. Errors from the group signal are ignored (the group may not exist if the process already exited).

**Technical reasoning — SIGKILL port release wait:**

After `SIGKILL`, the stop path must wait for the process to actually die before returning. Without this wait, the TCP port may still be held by the dying process when the next backend tries to start on the same port, causing a 15-second health check timeout ("server did not become healthy within 15s"). The implementation polls `IsProcessAlive` for up to 5 seconds after SIGKILL, then waits an additional 500 ms (`startupGracePeriod`) for the OS to release the TCP socket in the `TIME_WAIT` / cleanup phase.

### 6.6 Status Check

1. Glob the state directory for per-instance state files (§7).
2. For each, call the backend's `HealthCheck(addr)` to verify the server is alive and is the kind of server we recorded.
3. Print one row per live instance with: backend, address, active Profile, active Model, PID (if known), uptime, log file (if known).
4. Stale state files (server gone or different backend on the address) are removed and skipped.

Exit 0 if any instance is running; exit 1 if all are stopped.

### 6.7 Stale State Handling

On every operation that reads state, the launcher verifies the recorded server is alive *and* is the right kind of server, via the backend's `HealthCheck`. Health checks are discriminating — each backend identifies its own server and rejects responses from other backends sharing the same address (see [§5.3 Health Check Discrimination](#health-check-discrimination)). If the server is gone or belongs to a different backend, the per-instance state file is removed and the launcher proceeds as if that instance is not running.

### 6.8 `auto_stop_server` and `auto_unload`

These two flags determine what happens to *other* running instances when a Profile is activated.

| `auto_stop_server` | `auto_unload` | Behaviour for instances *other than* the activation target |
|---|---|---|
| `true` (default) | (any) | All other instances are stopped. |
| `false` | `true` (default) | Other instances stay running. Any Model loaded on them is unloaded (`LLMServer.UnloadModel`) — same rule for same-server swap and cross-server case ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)). |
| `false` | `false` | Other instances stay running with their Models intact. |

For llamacpp, `auto_unload` is silently ignored on llamacpp instances (Model swap requires a server restart — [ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)).

## 7. State Files

### 7.1 Location and Naming

Per-instance state files live in `~/.config/llama-launcher/`. Each running instance has one file, named after its address ([ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)):

```
state-{backend}-{port}.json                  # for loopback (host == 127.0.0.1)
state-{backend}-{host}-{port}.json           # otherwise
```

Examples:

```
state-llamacpp-8080.json
state-llamacpp-8081.json
state-ollama-11434.json
state-lmstudio-1234.json
state-llamacpp-192.168.1.50-8080.json
```

`ReadInstanceState(addr)` reads the record for a given address; `ReadInstancesForBackend(backend)` returns all instances of a given backend type; `ReadAllStates()` globs `state-*.json` and returns every record.

**Migration:** On first access, legacy `state.json` and legacy per-backend `state-{backend}.json` files are removed. If the recorded process is alive, the user re-activates the relevant Profile to recreate the per-instance state record.

### 7.2 Schema

```json
{
  "pid": 41023,
  "backend": "llamacpp",
  "host": "127.0.0.1",
  "port": 8080,
  "started_at": "2026-05-19T17:12:00+02:00",
  "log_file": "/Users/airic/.config/llama-launcher/logs/llamacpp-20260519-171200.log",
  "active_profile": "code-deepseek",
  "active_model": "/Users/airic/Models/deepseek-coder-v2-lite-instruct-Q4_K_M.gguf",
  "resolved_params": {
    "context_size": 8192,
    "gpu_layers": 99,
    "threads": 8,
    "...": "..."
  }
}
```

There is no `managed` field — `stop` is unconditional ([ADR-0001](docs/adr/0001-stop-is-unconditional.md)). PID is recorded when the launcher has it (always for llamacpp; for Ollama when the launcher auto-started via `ollama serve` and the backend implements `PIDTracker`; otherwise 0) and is used for `status` display and to associate log files with instances. Liveness is decided by `LLMServer.HealthCheck`, not by PID.

`active_profile` and `active_model` are empty strings when the server is running but no Model is loaded. `resolved_params` is the snapshot of the Profile's resolved parameters at activation time; the drift check in §6.1 step 4 compares this snapshot against the freshly resolved Profile to decide whether to print a drift notice ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)). `log_file` is omitted for instances the launcher did not start.

### 7.3 Atomicity

The state file is written atomically: write to a temporary file in the same directory, then `os.Rename` to the final path. This prevents corruption from a crash or power loss during the write.

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
| `models_dir` | `--models-dir` |

Boolean flags are only appended when the resolved value is `true`. Numeric flags are only appended when explicitly set (not nil after merge).

Argument assembly is delegated to the backend via `ManagedLLMServer.BuildServerArgs()`, allowing each `ManagedLLMServer` to map config fields to its own CLI flags.

## 9. Log Management

Server stdout and stderr are redirected to a log file at:

```
<log_dir>/<backend>-<YYYYMMDD>-<HHMMSS>.log
```

Example: `~/.config/llama-launcher/logs/llamacpp-20260519-171200.log`

The `logs` subcommand tails the log file of a running instance (path from the per-instance state file). With `--follow`, it uses `tail -f` and is the only mode where the launcher remains running.

### 9.1 Log Cleanup

Old log files can be cleaned up manually or automatically:

- **Manual:** `logs clean` deletes files older than 7 days (default). `--days N` overrides the threshold; `--all` removes everything. Reports files removed and space freed.
- **Automatic:** Setting `log_retention: N` in config causes `createLogPath` to silently delete files older than N days before each new log is created. No output during automatic cleanup.

Both paths use `cleanupLogs()`, which determines file age from the filename timestamp (not mtime) and always skips log files belonging to running servers (checked via `ReadAllStates` + `HealthCheck`).

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
| State file corrupt or unreadable | Delete state file, treat as stopped, warn. |
| Port already in use | Detected via early server exit — the launcher checks if the process is still alive ~500 ms after start and reports the log tail if it died. |

## 11. Future Considerations

These are explicitly out of scope for v1 but noted as natural extensions:

- **Per-instance log file naming**: `{backend}-{port}-{timestamp}.log` rather than `{backend}-{timestamp}.log`. Today multiple instances of the same backend coexist because timestamps differ at second resolution; revisit only if collisions become real.
- **Shell completions**: Generate bash/zsh/fish completions for subcommands and Profile names.
- **Config reload subcommand**: A `reload` subcommand that restarts the matching instance with the same Profile using updated config values. (Note: automatic config reload in the interactive menu is already implemented — this item covers the CLI subcommand.)
- **Additional LLM Servers**: vLLM and others — each as a new `backend_<name>.go` file implementing the `LLMServer` interface.
- **Homebrew formula**: Package for `brew install llama-launcher`.
- **Launchd integration**: Generate a launchd plist for auto-start on login.
- **Health-based Model status**: Query each LLM Server's "list loaded models" endpoint to verify actual loaded state rather than relying solely on state file.

## 12. Testing

### 12.1 Unit Tests (httptest)

Backend methods are tested using `net/http/httptest` mock servers. These tests run as part of `go test ./...` with no external dependencies.

| Test | What it covers |
|---|---|
| `TestLlamaCppHealthCheck` | 200 on `/health` with `{"status":"ok"}` body → success; non-llamacpp body (missing `status` field) → rejects; non-200 → error; unreachable → error. |
| `TestOllamaHealthCheck` | 200 with "Ollama" body → success; empty body → error; non-Ollama body → error; non-200 → error. |
| `TestLMStudioHealthCheck` | 200 on `/v1/models` → success when `/health` body lacks `status` field; healthy when LM Studio returns `{"error":"..."}` for `/health` and `/api/tags`; detects llamacpp via `/health` body containing `{"status":"ok"}`; detects Ollama via `/api/tags` body containing `{"models":[...]}`; non-200 → error; unreachable → error. |
| `TestLMStudioLoadModel` | Success, context_length inclusion, error with message, error without message. |
| `TestLMStudioUnloadModel` | Success, non-200 with error message, non-200 with empty body returns error. |
| `TestExtractLMStudioError` | Valid JSON, empty body, malformed JSON, missing message field. |
| `TestOllamaLoadModel` | Success (verifies keep_alive payload), error status. |
| `TestOllamaUnloadModel` | Success (verifies keep_alive=0), error status. |
| `TestOllamaListRunningModels` | Success with models, empty list, malformed JSON. |

### 12.2 Server & Config Tests

| Test | What it covers |
|---|---|
| `TestIsProcessAlive` | Current PID → true; PID 0 → false; negative PID → false; invalid PID → false. |
| `TestReadLastLines` | More lines than requested; fewer lines; nonexistent file. |
| `TestInstanceStatePath` | Loopback-host omission, non-loopback inclusion, filename format. |
| `TestServerState_Addr` / `_Uptime` | State helper methods. |
| `TestGetLLMServer` | Known LLM Server names return correct instance; unknown returns error. |
| `TestExpandTilde` | `~/path`, bare `~`, `~username` (unchanged), absolute path, empty. |
| `TestLoadConfig` | Missing file, valid config, no-profiles validation. |
| `TestValidate_*` | Deprecated fields, no servers enabled, auto-assign default server, `defaults.server` deprecation warning. |
| `TestShouldAutoClose` / `TestShouldDisplayCentered` | Nil-defaults-to-true/false asymmetry. |
| `TestConfiguredBackendAddr` | Returns merged address with colon separator. |
| `TestStateMigration` | Legacy `state.json` and `state-{backend}.json` files are removed on first access. |

### 12.3 Menu Helper Tests

| Test | What it covers |
|---|---|
| `TestParseChoice` | Valid, zero, negative, exceeds max, non-numeric, empty. |
| `TestFormatUptime` | Hours, minutes, seconds-only branches. |
| `TestProfileDisplayName` | With description, without, unknown Profile. |
| `TestFormatProfileParams_GPULayers_LMStudio` | Intermediate value shows number; 99 shows max; 0 shows off. |

### 12.4 Test Helpers

`helpers_test.go` provides `addrFromURL(t, rawURL) string`, which parses an `httptest.NewServer` URL and returns the `host:port` portion for passing to backend methods that expect an `addr` string.

## 13. Build and Installation

The version number lives in the root `VERSION` file and is injected at build time via `ldflags` into `launcher.Version`.

```bash
make build          # builds ./llama-launcher binary (version injected from VERSION file)
make install        # builds + copies to ~/.local/bin, adds to PATH if needed
make clean          # removes binary

go test ./...       # run all tests
go test ./internal/launcher/ -run TestMergeParams  # run a single test
go vet ./...        # static analysis
```

The binary is statically linked (default for Go on macOS with CGO_ENABLED=0) and has no external dependencies at runtime.

## 14. Coding Standards

Follow `skills/coding-standards/SKILL.md` when writing or modifying code. Read the base references and the Go-specific extensions before making changes.

### After Changing Code

1. Update the documents `llama-launcher.TDD.md`, `README.md`, `CHANGELOG.md`, and `TODO.md` if the change affects behavior, configuration schema, subcommands, error handling, or any other aspect covered here.
2. If the change touches one of the architectural decisions in [docs/adr/](docs/adr/), update or supersede the relevant ADR in the same change.
3. Run `make install` to build and install the updated binary.
