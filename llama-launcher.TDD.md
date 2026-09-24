# llama-launcher — Technical Design Specification

The architectural source of truth for `llama-launcher` lives in two places:

- **[CONTEXT.md](CONTEXT.md)** — domain language (LLM Server, Model, Profile; Activate, Load/Unload, Start/Stop).
- **[docs/adr/](docs/adr/)** — numbered Architectural Decision Records (ADRs 0001–0012) that pin down behaviour.

This document explains how those decisions are realised in code. Where this document and an ADR appear to conflict, the ADR wins; please file a doc fix.

## 1. Overview

`llama-launcher` is a lightweight Go CLI tool for managing LLM Servers through named configuration Profiles. It starts an LLM Server as a detached background process (or connects to one already running), asks it to load a Model, and exits — consuming zero resident memory while the server runs. Subsequent invocations rediscover running instances by probing the addresses in `config.yaml` (no persisted state files); each command queries the live LLM Server's own API for the currently loaded Model and parameters.

`llama-launcher` is a *process manager*, not a request router (see [ADR-0002](docs/adr/0002-not-a-router.md)). It does not expose any HTTP endpoint of its own and does not proxy client traffic. Clients connect to LLM Servers directly using each server's native address.

The architecture is server-agnostic: an `LLMServer` interface (see [§5.3](#53-llmserver-interface)) abstracts server-specific logic, making it straightforward to add support for other LLM Servers alongside the initial llama.cpp implementation. Four are implemented today: llama.cpp (`llamacpp`), Ollama (`ollama`), LM Studio (`lmstudio`) and Splash (`splash`, an MLX-based OpenAI-compatible server for Apple Silicon that, like llama.cpp, is restarted per Profile).

The CLI is not the only front end: since v1.6.0 the same core is importable as a Go library through the curated `launcher/` facade (`github.com/airiclenz/llama-launcher/launcher`), so another program can load a Profile, discover what is running, and stop or unload it in-process instead of shelling out — see [ADR-0011](docs/adr/0011-public-library-facade.md) and [§16](#16-public-library-facade).

Design target: macOS (Apple Silicon) — the only platform the Homebrew formula ships and the only one the menu's memory/GPU readout can read. Since v1.6.1 that is no longer the same thing as the only platform the code *builds* on: the package compiles on darwin, linux and windows, and each verb works wherever its mechanism exists ([ADR-0012](docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md), [§16.6](#166-platform-contract)) — importing the library pulls in the whole core, so portability became the library's obligation rather than each client's. The compiled binary is the only artifact — no runtime dependencies.

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
- Importable as a Go library: a curated public facade (`launcher/`) exports the actuation verbs for in-process clients, with the documented symbols as the SemVer surface from v1.6.0 on (see [ADR-0011](docs/adr/0011-public-library-facade.md) and [§16](#16-public-library-facade)).

### Non-Goals

- Persistent TUI or dashboard (contradicts the memory-freeing goal).
- Model downloading or GGUF management.
- HTTP server, API proxying, or request routing **in the `llama-launcher` binary** (see [ADR-0002](docs/adr/0002-not-a-router.md)). Remote *control* (not inference) is available through a separate, optional adapter — see [§15](#15-optional-mcp-control-plane-adapter).
- Feature parity outside macOS. [ADR-0012](docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md) puts *compilation* on darwin, linux and windows under contract and makes every verb work where its mechanism exists ([§16.6](#166-platform-contract)) — but Linux and Windows are not design targets: the memory/GPU readout is macOS-only and omits itself elsewhere, Homebrew remains the only distribution, and Windows gets no process control and no interactive menu.

## 3. User Experience

### 3.1 Interactive Mode

Running `llama-launcher` with no arguments enters a one-shot interactive menu with arrow-key navigation, colored output, and full-screen repainting. Falls back to numbered input when stdin is not a terminal. The config file is automatically reloaded before each menu display and on every 10-second header refresh, so changes made via "Edit config" or an external editor take effect without restarting. If the reloaded file is invalid, the last good config is silently preserved.

**When no server is running:**

```
    llama-launcher v1.0.0

    Status  ● stopped

    ▸ DeepSeek Coder V2 Lite    65K
      Qwen 2.5 32B             131K
      reasoning-phi             32K
      ─
      Start server only

    ↑↓ select · enter start & load · q quit
```

Each Profile row shows the Profile's optional `title`, falling back to the Profile name (`reasoning-phi` above) when no title is set. The same rule applies everywhere a Profile is presented: the status header, the Profile lists, and the "Switch model" pop-up.

Where a Model name is shown rather than a Profile title — the status header, the "Active model" pop-up, the unload picker, and the same surfaces in the subcommands ([§3.2](#32-subcommands)) — the server-reported id is rendered as a file name: a path-shaped id (absolute, or with a `.gguf` base name) collapses to that base name, so `/Users/me/LL-Models/Qwen/qwen3.6-35B-A3B-Q4_K_M.gguf` reads as `qwen3.6-35B-A3B-Q4_K_M.gguf`. Ids that are names rather than paths render verbatim — LM Studio's `qwen/qwen3-8b` and Ollama's `llama3:8b` would lose their publisher prefix if they were cut at the separator. The rule is render-time only (`modelDisplayName`, [§5.2](#52-source-files)): `RunningInstance.ActiveModel` keeps the raw id, which is what Profile matching ([§7.2](#72-discoverrunninginstances)) and `status --json` ([§3.2](#32-subcommands)) use.

When more than one LLM Server is enabled in the config, every Profile row additionally carries a column-aligned `[server]` tag (e.g. `[LLaMA.cpp]`, `[LM-Studio]`) showing which server the Profile targets. The tag appears in all Profile lists — server stopped, running with no model, and the "Switch model" pop-up. With a single enabled server the tag is omitted; whether the configured Profiles currently use more than one server does not matter, so the column does not appear and disappear as profiles are edited.

Between the Profile title and that `[server]` tag sits a right-aligned **context-size column** showing the Profile's *effective* context size — `defaults` merged with the Profile's own `context_size`, the same merge `list --json` reports — in compact form: values below a thousand verbatim, then integer floor division into `K` and `M` (`512`, `4K`, `65K`, `131K`, `1M`). The column is shown only when at least one Profile in the listing has a value to show, the same presence gating as the `★` marker ([§4.7](#47-favourite-profiles)); Profiles without one get a blank cell of the column width, so the `[server]` tags and the `★` markers keep their columns. With a single enabled server (no tag column) the cell is the whole row description. Two spaces separate the cell from the `[server]` tag that follows it, and from the padded title column before it — except in the arrow-key menu, which pads its title column one space wider, putting three there.

Which rows can show a value is owned by the backend, not by a name check: a Profile is displayable only when its resolved LLM Server's `ParamSpecs` ([§5.3](#53-llmserver-interface)) carries the context-size spec — the rule that a parameter the server never receives is never displayed, the same one the "Show model config" pop-up follows. Ollama's spec list is empty (its load request carries only the model name and a keep-alive), so Ollama rows render a blank cell; a future Ollama context-size feature would light the column up with no change to the list renderers. A `-c` / `--ctx-size` value passed through a Profile's `extra_args` is deliberately not parsed and never appears — only the `context_size` key is read.

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

    ▸ DeepSeek Coder V2 Lite    65K
      Qwen 2.5 32B             131K
      reasoning-phi             32K
      ─
      Stop server
      Show log
      Edit config

    ↑↓ select · enter load · q quit
```

"Edit config" appears in every menu variant, the numbered fallbacks included, only where an editor can open the file. On darwin it runs `open <config>`, which hands the file to the default app and returns at once. Elsewhere it runs `$VISUAL`, else `$EDITOR` (an empty value counts as unset), split on whitespace with the config path appended and the terminal attached, and the menu waits for it to exit. With neither set the item is not offered: the TUI list leaves it out, and the numbered fallbacks drop both its line and its key from the `Select [...]` prompt.

Selecting "Switch model" presents the Profile list (excluding the currently loaded Profile), unloads the current Model via API (for LLM Servers that support it) or restarts the server (for llamacpp), loads the new Model, and exits. When only one Profile is configured, "Switch model" is omitted from the menu entirely.

When more than one LLM Server instance is running, the relevant menu actions ("Stop server", "Unload model", "Show log") present a sub-list disambiguated by `host:port`. There is no "primary" or "current" instance — see [CONTEXT.md flagged ambiguities](CONTEXT.md#flagged-ambiguities) and [ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md).

A **Starting** instance — server up, address bound, still loading its Model ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)) — appears wherever the menu lists instances, labelled `starting…`: in the status header, in the "Stop server" instance picker, and in the no-model menu's `Status:` line. It names no Profile or Model (the server cannot answer a Model list yet). "Stop server" works on it like on any other instance; selecting a Profile that targets its address goes through `LoadProfile` and hits the same refusal as the CLI (§6.2). The header's refresh signature includes the Starting flag, so the periodic repaint notices the Starting→healthy transition. An **AuthFailed** instance — a server refusing the configured api_key, which no backend identified — renders wherever the menu lists instances (the status header, the "Stop server" picker, the no-model menu's `Status:` line) as `auth failed at <addr> — check api_key in the servers section`, through the one helper every listing shares (`authFailedLabel`), never as a model, as stopped or as a bare address.

**The plaintext-key offer.** Once per launch, after the platform gate and before the menu is drawn, the launcher looks for enabled `servers:` entries still keeping their `api_key` as a literal in the config file without `plaintext_key_ok: true` ([§4.2](#42-schema)). Finding none, it does nothing at all — not even a store probe, which costs a subprocess. Finding some, it probes for a secret store, and with one available raises a single offer naming those entries and the file they live in:

```
    llama-launcher v1.6.3

    The api_key for llamacpp is stored in plain text in
    ~/.config/llama-launcher/config.yaml

    ▸ Move it into Keychain            the entry reads the key with api_key_cmd afterwards
      Not now                          asked again next launch
      Never for these entries          records plaintext_key_ok: true

    ↑↓ select · enter confirm · q not now
```

Moving writes each key into the store, reads it back, and rewrites that entry's `api_key` line into an `api_key_cmd` line ([§4.2](#42-schema)); "never" records `plaintext_key_ok: true` on each entry, so the offer never returns for them; "not now" — and cancelling with `q` — writes nothing and leaves the offer for the next launch. What changed, and the failure that stopped the rest if there was one, is shown in a popup before the menu opens; the menu's own config reload picks up the rewritten file. The numbered fallback presents the same three answers when stdin is not a terminal. On a machine with no secret store there is nothing to offer the key, so the launcher prints the stderr notice described in [§4.2](#42-schema) and opens the menu as usual — a key in the file is something to say, never a reason to refuse to run.

### 3.2 Subcommands

For scripting, automation, and quick access:

| Command | Behaviour |
|---|---|
| `llama-launcher load <profile> [--restart]` | Primary command. Activates the Profile: start the LLM Server if needed, then load the Model. If the same Profile is already active at that address, the command is a no-op (see [ADR-0007](docs/adr/0007-profile-activation-idempotency.md)); if the running parameters differ from the resolved Profile, a drift notice is printed to stderr. Pass `--restart` (or `-r` / `--force`) to force re-activation regardless. A plain `load` targeting an address where a managed server is still starting up (Starting, [ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)) refuses with guidance pointing at `llama-launcher stop` / `--restart`; with `--restart` the Starting occupant is stopped and replaced. Respects `auto_unload` and `auto_stop_server` for any other running instances — the `auto_stop_server` sweep includes Starting instances at other addresses. |
| `llama-launcher unload [profile]` | Unload the Model from the matching running instance. For LLM Servers with a load/unload API (Ollama, LM Studio), this is an HTTP call; for llamacpp and Splash, it stops the server (Model is part of the server's args). A Starting managed instance — still loading its Model — is a valid target; its unload reduces to the same stop ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)), and it is labelled `(starting…)` in the ambiguous-target listing. An auth-refusing server is a candidate too, listed as `auth failed at <addr> — check api_key in the servers section`; unloading it fails with the `authFailedErr` message (exit 3), not "No model loaded". Optional Profile argument disambiguates when multiple Models are loaded; auto-detects when only one candidate (loaded or Starting) exists. |
| `llama-launcher start [--profile p]` | Start the LLM Server without loading a Model. With `--profile` (or `-p`), resolve the named Profile and activate it — equivalent to `load <profile>`. For a managed backend (llamacpp, Splash) a bare `start` fails fast with a configuration error (exit 2) instead of forking: the Model is part of the server start arguments ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)), so there is nothing to start without a Profile. |
| `llama-launcher stop [target]` | Stop a running LLM Server instance. `[target]` may be an `host:port` (preferred when multiple instances of the same backend run) or a backend name; auto-detects when only one instance is discovered. A Starting instance is a first-class stop target — an explicit stop kills an in-flight Model load ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)). So is an auth-refusing server (every configured backend answers 401/403): its listener is signalled and the report reads `Stopped server at <addr> (PID n)`, naming no backend. Stop is unconditional (see [ADR-0001](docs/adr/0001-stop-is-unconditional.md)) regardless of whether the launcher started the process. |
| `llama-launcher status [--json]` | Print one row per discovered LLM Server instance (address, backend, active Profile/Model, PID if known, uptime); a Starting instance shows `starting…` instead of `running` and names no Profile/Model ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)); an auth-refusing server's row is `auth failed at <addr> — check api_key in the servers section`, with no backend or state column. Exit code 0 if anything is discovered (healthy, Starting or auth-refusing), 1 if all stopped. With `--json`, emits a JSON array with one entry per discovered instance plus one `running: false` entry per enabled backend with no discovered instance (`backend`, `running`, `starting`, `address`, `active_profile`, `active_model`, `pid`, `uptime_seconds`, `auth_failed`); `running` keeps meaning healthy — a Starting instance reports `running: false`, `starting: true`, and an auth-refusing server follows the backend entries as one object per address with `backend: ""`, `running: false`, `auth_failed: true` (every other entry carries `auth_failed: false`). Same exit code semantics. The human-readable rows and the `Active:` details line render the Model as a file name ([§3.1](#31-interactive-mode)); `active_model` in the `--json` output stays the raw server-reported id. |
| `llama-launcher list [--json]` | Print all configured Profiles with the context-size column ([§3.1](#31-interactive-mode)), target LLM Server, and description. With `--json`, emits a JSON array with one entry per Profile (`name`, `backend`, `model`, plus `title`, `description`, `gpu_layers` and `context_size` when set). |
| `llama-launcher logs [target] [--follow]` | Tail an instance's log file. `[target]` is an `host:port` or backend name; auto-detects the active instance when only one applies. With `--follow`, behaves like `tail -f`. This is the one subcommand that remains running until interrupted. |
| `llama-launcher logs clean [--days N\|--all]` | Delete old log files. Default threshold is 7 days; `--days N` overrides (N must be at least 1 — `--days 0` is refused). `--all` removes everything. Always skips logs belonging to running servers. Reports files removed and space freed. |
| `llama-launcher config validate` | Parse config and report all validation problems at once (deprecated fields, unknown/disabled servers, missing models, Profiles missing `server:` with no defensible fallback). Uses `parseConfig` + `validateAll` to collect errors without stopping at the first. Exit 0 if valid, 2 if invalid. |
| `llama-launcher config init [--force]` | Generate the example config file at the configured path. Refuses to overwrite an existing file unless `--force` (or `-f`) is passed. Exit 0 on success, 2 on error or if the file already exists. |
| `llama-launcher config reset` | Overwrite the config file with the example config unconditionally. Provides a quick way to return to a known-good starting point. Exit 0 on success, 2 on error. |

All subcommands except `logs --follow` exit immediately after completing their action.

### 3.3 Exit Codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Server not running (for `status`) or general expected condition |
| 2 | Configuration error (parse error, unknown profile, example config cannot be written; first-run missing file continues — §4.1) |
| 3 | Process management or API error (failed to start, failed to load, stale PID) |

## 4. Configuration

### 4.1 File Location

Default: `~/.config/llama-launcher/config.yaml`

Override with `--config <path>` flag or `LLAMA_LAUNCHER_CONFIG` environment variable (flag takes precedence).

On first run, if no config file exists, the launcher creates a documented example config, reloads it, and continues — into the interactive menu for a bare invocation, or into the requested subcommand.

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
  splash: false

# Base directory for model files. Profile model paths are resolved
# relative to this directory unless they are absolute.
models_dir: ~/Models

# Directory for server log files.
log_dir: ~/.config/llama-launcher/logs

# Automatically delete log files older than N days on server start.
# Runs silently before each new log file is created. Unset or 0 = no cleanup.
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

  # Sampling defaults — passed to llama-server as launch flags (--temp,
  # --repeat-penalty, --top-k, --top-p, --min-p); they set the server-side
  # defaults for API requests, per-request parameters still override them
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

  splash-qwen:
    description: "Qwen3.8 27B via Splash"
    server: splash
    model: incoai/Qwen3.8-27B-Splash   # Hugging Face owner/repo, installed once
    context_size: 32768               # passed as --max-context
    extra_args: ["--default-reasoning-effort", "high"]
```

`title` and `description` are both optional. `title` is the label shown wherever a Profile is presented to the user (status header, Profile lists, "Switch model" pop-up); when unset, the Profile name is shown instead. `description` is longer free text displayed only in the "Show model config" pop-up.

The `servers` map lists available LLM Servers. Keys are server names (`llamacpp`, `ollama`, `lmstudio`, `splash`). Each value is either a plain boolean (`true` enables the server, `false` disables it) or a mapping with the keys `enabled` (optional, defaults to `true`), `api_key`, `api_key_cmd` and `plaintext_key_ok` (all optional). Disabled servers are hidden from status display, and Profiles targeting a disabled server are excluded from menus, CLI output, and Profile resolution. At least one server must be enabled.

The two managed servers are launched from a binary found on `PATH`: `llama-server` for llamacpp and `splash` for Splash (§5.4). No config key names Splash's binary. The `splash` command may be a wrapper script — Splash's installer writes one, and a source checkout needs one because its `splash` script derives its root from `dirname $0`, which a plain symlink breaks (e.g. `printf '#!/bin/sh\nexec "$HOME/Repos/splash/splash" "$@"\n' > ~/.local/bin/splash && chmod +x ~/.local/bin/splash`). A launchd-started launcher or MCP adapter sees launchd's `PATH`, not the login shell's, so that `PATH` must include the wrapper's directory (e.g. `~/.local/bin`). Only starting a Splash server needs the command; resolving a Splash Profile never looks it up (§4.4).

A server's key comes from **one** of two sources, never both: the literal `api_key` written in the file, or `api_key_cmd`, a command whose standard output *is* the key. An entry setting both, and an `api_key_cmd` holding nothing but whitespace, each fail config load naming the entry — neither has a safe reading, and silently picking one would send requests with a key the user believes they replaced or drop authentication the file asked for.

`api_key` is stored as plaintext (the config file is created with mode 0600) and is trimmed of surrounding whitespace, with a load-time warning when trimming changed the value. `plaintext_key_ok: true` records that an entry's literal key is meant to stay in the file.

Every run that finds an **enabled** entry holding a literal key without that marker acts on it. The interactive menu raises the migration offer ([§3.1](#31-interactive-mode)) — it is the one surface that can ask a question. A subcommand runs and exits and can never prompt, so it says what it found instead: a one-line `warning:` notice on stderr naming those entries, the config file this run actually read (`--config` and `LLAMA_LAUNCHER_CONFIG` both move it), why no offer is coming, and the three ways out by hand — point the entry at `api_key_cmd`, `chmod 600` the file, or answer for good with `plaintext_key_ok: true`. A subcommand deliberately does **not** probe for a secret store: the probe is a subprocess, and its answer would only feed a question that run will never put. Moving a key is one ordered move per entry: write the secret into the store, read it back by running the exact `api_key_cmd` line about to be persisted, and rewrite the entry only if what comes back matches what went in — a failed read-back or a mismatch leaves the config file byte-identical and says why, so the run keeps working off the literal it already has. On macOS the write goes through `security -i`, which parses the command line it reads with its own quote-and-escape grammar, so a key or entry name containing a double quote, a backslash or a control character is refused before any `security` process starts — the migration reports the refusal and the literal stays; every other value is written inside double quotes, so base64 keys (`=`, `+`, `/`) and spaced entry names still move. The Secret Service write takes the key on stdin, where no parser reads it, so only the line-break refusal both stores share applies there.

`api_key_cmd` is resolved once at config load, for **enabled** servers only (the launcher never talks to a disabled server, so asking its store for a secret would be a dialog the user cannot connect to anything they did), and its output is trimmed the same way. Config is loaded once per run, so this is one subprocess per configured entry per run; the interactive menu's `Reload` re-runs them, which is deliberate — the store may have changed. The line is handed whole to a shell (`sh -c`; `cmd /C` on Windows) so a pipeline works without a wrapper script: it is the user's own line, in a file only they can write. On unix that premise is enforced before any command runs ([ADR-0016](docs/adr/0016-api-key-cmd-runs-only-from-an-owned-config.md)): when an enabled entry carries `api_key_cmd`, `configTrusted` `os.Stat`s the config file the load read (a symlink is judged by its target) and refuses the load — naming the file and the fix, `chmod 600 <path>` — unless the current uid owns it and its mode has no bit of `0o022`; no command runs. A config with no enabled `api_key_cmd` is never judged, and Windows is not gated. `config validate` runs no command and does not report the gate, so a file that fails it validates clean and is refused only by the next load. The command gets no stdin and neither of the launcher's standard streams — it may be running under the menu, which owns the terminal, so a store that must ask the human to unlock has to prompt through its own GUI agent — and is bounded by a 60-second timeout with its output capped at 64 KiB. A command that fails, times out, overruns the cap or prints nothing fails the load naming the entry and quoting what it said on stderr: a key source that answers with nothing is a broken source, not a keyless server, so the launcher never degrades to sending no key. `APIKeyFor` returns the trimmed literal when one is set and the resolved command output otherwise.

Whichever source answers, the key's meaning is per-backend:

- **llamacpp** — exported into the launched server's environment as `LLAMA_API_KEY`, so llama-server rejects client requests lacking `Authorization: Bearer <key>` (`/health` stays exempt). The key is deliberately kept off the process argv, where any local user could read it out of `ps`. llama-server applies the env var first and then appends every `--api-key` flag to the same key list (llama.cpp `common/arg.cpp`; observed on b10851 by the integration test `TestLlamaServerAPIKey`), so an `extra_args` `--api-key` adds a second valid key rather than replacing the configured one — and such a literal key *is* argv-visible, which is why display surfaces still redact it and every log surface masks it (`RedactLogText`, `redact.go`). The same probe found neither key echoed into llama-server's own log file on b10851.
- **lmstudio** — LM Studio owns its token ("Require API token" in its Server Settings); the configured key is only *sent* by the launcher so its health checks and model load/unload calls keep working when auth is enabled.
- **ollama** — Ollama has no native auth; a key is only meaningful when the instance sits behind an authenticating reverse proxy. The launcher sends it with its own requests.
- **splash** — exported into the launched server's environment as `SPLASH_API_KEY`, never on argv, so Splash rejects client requests lacking `Authorization: Bearer <key>`. The launcher's own `/ready` and `/v1/models` probes carry the key.

Regardless of backend, the launcher attaches the key as a `Bearer` header to every HTTP call it makes to that server (health checks, model load/unload, model listing, live-params queries). The launcher never enforces auth itself — it is not a proxy ([ADR-0002](docs/adr/0002-not-a-router.md)).

### 4.3 Parameter Resolution

Parameters are resolved in order of precedence (highest first):

1. Profile-level value (including `server`)
2. `defaults` block value (`defaults.server` is honoured but emits a deprecation warning when used — see [ADR-0005](docs/adr/0005-profile-server-is-identity.md))
3. Server-specific fallback from `servers` map address or `LLMServer.DefaultAddr()` (e.g. Ollama defaults to `localhost:11434`, LM Studio to `localhost:1234`, Splash to `127.0.0.1:8000`)
4. Built-in fallback (only for `host: 127.0.0.1` and `port: 8080`)

All numeric and boolean parameters use pointer types internally to distinguish "not set" from zero/false. A nil pointer means "inherit from the next level."

### 4.4 Model Path Resolution

The `model` field in a Profile is resolved as follows:

1. If the path is absolute, use it directly.
2. If relative, join with `models_dir` from config.
3. `models_dir` itself supports `~` expansion.
4. Validate that the resolved path exists and is a regular file before loading.

Model path resolution is delegated to the backend via `LLMServer.ResolveModel()`. llama.cpp validates that the file exists on disk. Ollama and LM Studio accept opaque Model identifiers (e.g. `llama3.1:8b`, `lmstudio-community/meta-llama-3.1-8b-instruct`) without file validation.

Splash takes a Hugging Face `owner/repo` id and refuses one that is not installed ([ADR-0014](docs/adr/0014-splash-models-must-be-installed.md)). `ResolveModel` first validates the id against Splash's own repo-id rule (exactly one `/`, parts from `[A-Za-z0-9._-]` with alphanumeric-or-`_` ends, a repo of at most 96 characters, no `--`, `..` or trailing `.git`), so a ref like `../..` cannot escape the cache path. It then checks the Hugging Face cache: the hub is `$HF_HUB_CACHE`, else `$HF_HOME/hub`, else `$XDG_CACHE_HOME/huggingface/hub`, else `~/.cache/huggingface/hub` (an empty variable counts as unset), and the Model is installed when some `<hub>/models--<owner>--<repo>/refs/splash/<installation>/<rev>` pin exists and `snapshots/<rev>/manifest.json` is a regular file — a manifest without a pin can be an interrupted download. A Model that is not installed fails resolution with the one-time install command (`splash serve --model <owner/repo>`, run once in a terminal); the launcher never triggers that download, which can take around twenty minutes and happens before Splash binds its address, where no probe or stop could reach it. The check reads only the cache and never looks up `splash` on `PATH`, so `config validate`, discovery and unload work without the command.

### 4.5 Extra Arguments

The `extra_args` list in a Profile is appended verbatim to the assembled server command line. This provides an escape hatch for flags not explicitly modelled in the config schema without requiring a launcher update.

### 4.6 LLM Server Selection

A Profile's LLM Server is part of the Profile's identity, not a tunable parameter — the Model identifier format, supported parameters, and lifecycle semantics all depend on it (see [ADR-0005](docs/adr/0005-profile-server-is-identity.md)).

Each Profile **must** set `server:` explicitly. The launcher tolerates a missing `server:` field in two cases only:

1. **Single-server config** — when exactly one entry in `servers:` is enabled, the missing `server:` is auto-resolved to that one. No warning.
2. **`defaults.server` fallback** — when more than one server is enabled and a Profile omits `server:`, the launcher falls back to `defaults.server` and emits a deprecation warning naming the Profile. `defaults.server` is soft-deprecated and will be removed in a later release.

`config validate` reports the warning explicitly; `Reload` and `LoadConfig` surface it on stderr at load time.

### 4.7 Favourite Profiles

Each Profile may set `is_favourite: true` to pin it to the top of the menu and `list` output. Profiles are sorted by three keys in order: favourite status first (favourites before non-favourites), then by server (alphabetically), then by Profile name alphabetically. Favourite Profiles display a `★` marker right-aligned at the end of the row, in the same column across the entire list (rows are padded so the marker column is consistent). When no Profile in the listing is starred, no marker column is rendered. This ordering and rendering are produced by `Config.ProfileNames()` together with `buildProfileItems`/`buildSimpleProfileLines`/`cmdList`, and apply to every UI surface that lists Profiles (TUI menu, non-terminal fallback, `llama-launcher list`). The same three functions render the context-size column ([§3.1](#31-interactive-mode)) under that one contract, over a single shared cell computation (`profileContextCells`): presence gating, compact formatting, right-alignment and the blank-cell padding are decided once, so a Profile row carries the same columns on all three surfaces.

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
                            │             │   Runtime discovery    │
                            │             │ (discovery.go) — probe │
                            │             │  addrs + query APIs    │
                            │             └────────────────────────┘
       ┌──────────────┬─────┴──────┬──────────────┐
┌──────┴───────┐ ┌────┴─────┐ ┌────┴─────┐ ┌──────┴───────┐
│   LlamaCpp   │ │  Ollama  │ │ LMStudio │ │    Splash    │
│  (backend_   │ │          │ │          │ │  (backend_   │
│ llamacpp.go) │ │          │ │          │ │  splash.go)  │
└──────────────┘ └──────────┘ └──────────┘ └──────────────┘
```

### 5.2 Source Files

| File | Responsibility |
|---|---|
| `main.go` | Entry point, `--config` flag parsing, subcommand dispatch, usage text. `status` and `list` accept `--json` for structured output (local marshalling structs in `cli.go`). The `unload`/`stop` subcommands are target selection plus after-the-fact `StopResult` formatting over the unified entry points in `server.go`. The subcommand bodies live in `cli.go`, where `cmdList` renders the Profile table — name, the context-size column, `[server]` tag, description, `★` — padding every column by `visibleWidth` and taking its context cells from the same `profileContextCells` helper the menu uses, which is what keeps the three Profile-list surfaces identical ([§3.1](#31-interactive-mode), [§4.7](#47-favourite-profiles)). Between the zero-argument menu branch and the subcommand switch sits the plaintext-key notice: a command that runs and exits prints it on the same `warning:` stderr sink as the config warnings and probes no secret store, because the offer belongs to the menu ([§4.2](#42-schema)). |
| `config.go` | Config/Profile/ProfileParams struct definitions, YAML loading (`parseConfig` for parse-only, `LoadConfig` for parse+validate), `Reload` for in-place re-read, `~` expansion, parameter merging, validation (`validate` for fast-fail, `validateAll` for collecting all problems including non-fatal warnings such as `defaults.server` fallback usage), example config generation. Server enable/disable filtering via `IsServerEnabled()`. `ServerConfig` (bool-or-mapping YAML form per server entry) with `APIKeyFor()` accessor; the `api_key`/`api_key_cmd` key sources live here too — `apiKeySourceErrors` refuses an entry naming both or a blank command, and `resolveKeyCommands`/`runKeyCommand` run each enabled entry's command through the platform shell at load — only after `configTrusted` (`config_trust_*.go`) passes the file — bounded and capped, storing the output in the unexported `resolvedKey` (§4.2). `LoadConfig` pushes configured API keys onto the registered backends via `applyAPIKeys`. The parse+validate body lives in `LoadConfigNotify`, which delivers each non-fatal warning to a `NoticeFunc` sink as raw text (§16.3); `LoadConfig` is a one-line delegation binding the CLI's `warning: %s` stderr printer, and `Reload` goes through `LoadConfig`, which is why library clients re-read config by calling the facade's `LoadConfig` again. |
| `config_trust_unix.go` | `configTrusted(path)` behind `//go:build unix`: the gate `resolveKeyCommands` runs before any `api_key_cmd` — `os.Stat` of the config file must show the current uid as owner and no bit of `0o022` in its mode, else an error naming the file and `chmod 600 <path>` ([ADR-0016](docs/adr/0016-api-key-cmd-runs-only-from-an-owned-config.md), §4.2). |
| `config_trust_windows.go` | The same signature under `//go:build windows`, returning nil: windows access is ACLs, which no uid or mode word describes, so `api_key_cmd` is not gated there. |
| `configwrite.go` | The surgical writer for one `servers:` entry's key source: `SaveServerKeyCommand` turns that entry's `api_key:` line into an `api_key_cmd: <command>` line, `SaveServerPlaintextKeyOK` records `plaintext_key_ok: true` on it. Both splice text rather than re-marshal the document, so comments, blank lines, key order and every other entry come back byte-identical; anything non-surgical (entry absent, the scalar `llamacpp: true` form, a flow-style mapping, an `api_key` value spanning more than its own line) is refused with an edit-by-hand error naming the path and the file is left untouched. The spliced bytes are re-parsed and checked to declare the intended source before an atomic temp-file-plus-rename write at mode 0600; an entry already in the target state is a no-op, so a re-offer cannot churn the file. |
| `keymigrate.go` | The key migration as policy, with no user interface in it ([§3.1](#31-interactive-mode), [§4.2](#42-schema)): `plaintextKeyServers` names the enabled entries still holding a literal key without `plaintext_key_ok` (sorted — the servers section is a map), `decideKeyOffer` turns that plus an injected store probe into offer / notice / silence without touching a terminal (and never probes when there is nothing to offer), `migrateKey` performs the write → read-back-by-running-the-persisted-line → rewrite order for one entry, `migrateKeys` and `keepPlaintextKeys` carry an answer over a whole run of entries and stop at the first failure while still reporting what already changed, and `plaintextKeyNotice` is the text for a run that cannot raise the offer. The store itself is `internal/keystore` (vendored from apogee: `Probe`, `Store.Write`, `Store.ReadCmd`), reached through the local `secretStore` interface so the sequence can be exercised against a fake. |
| `launcher/launcher.go` | The public library facade ([§16](#16-public-library-facade), [ADR-0011](docs/adr/0011-public-library-facade.md)) — the only package outside `internal/` another module can import. Twenty documented symbols ([§16.1](#161-exported-surface)): eight type aliases (`Config`, `Profile`, `ProfileParams`, `ResolvedProfile`, `RunningInstance`, `StopResult`, `ProgressFunc`, `NoticeFunc`), five sentinel errors re-exported by value so `errors.Is` matches across the boundary (`ErrConfigNotFound`, `ErrNotRunning`, the v1.6.1 pair `ErrStartupTimeout` and `ErrUnsupported`, and `ErrLoadCanceled`), and seven one-line wrapper functions. Wrappers contain zero logic (the ADR-0009 `realOps` discipline applied to the facade): `LoadConfig` and `LoadProfile` delegate to the notice-taking `LoadConfigNotify` / `LoadProfileNotify`, the rest delegate to their internal namesake. |
| `launcher/doc.go` | Package documentation carrying the facade's contract (§16.2): the documented surface is the API, notices are callbacks, verbs block and cancellation is `Stop(addr)` (the cancelled load returns `ErrLoadCanceled`; the one carve-out from per-address serialization), one `Config` per process — plus the ADR-0012 platform contract in prose, naming which verbs act on windows and how each refusal surfaces there ([§16.6](#166-platform-contract)), the `ErrStartupTimeout` sentence (the server was left running, so keep observing), the scope note (local machine only, remote control belongs to the MCP adapter) and a godoc usage example. |
| `launcher/launcher_internal_test.go` | The one facade test that must live *inside* `package launcher`: it holds the two ADR-0012 sentinels (`ErrStartupTimeout`, `ErrUnsupported`) and `ErrLoadCanceled` against the `internal/launcher` values they alias, which an external test package cannot import. None is reachable through a facade verb in a test, so identity is the boundary proof ([§16.5](#165-tests)); the older two sentinels are covered from the outside, through the verbs that return them. |
| `launcher/launcher_test.go` | Facade tests in an external test package (`package launcher_test`), so they reach the launcher only through its exported surface, exactly as a client module does: config load with the warning sink (and merged `context_size` off the resolved profile), the two sentinel-error paths, httptest-backed discovery, and the drift notice threaded end-to-end through a plain `LoadProfile` on the ADR-0007 idempotent path with nothing reaching stderr. httptest and temp directories only — no real processes. |
| `defaults/config.yaml` | Example config template, embedded at compile time via `go:embed`. |
| `defaults/embed.go` | Embeds `config.yaml` and exports it as `defaults.ExampleConfig`. |
| `backend.go` | `LLMServer` and `ManagedLLMServer` interface definitions (see [§5.3](#53-llmserver-interface)), `ResolvedProfile` struct, `ProfileParamSpec` display specs (the shared `spec*` values plus the `intParamSpec`/`boolParamSpec`/`floatParamSpec` constructors), LLM Server registry (register/get), `applyAPIKeys` (pushes per-server API keys from the config onto backends implementing the package-private `apiKeyConfigurable`), `apiKeyHolder` (the RWMutex-guarded key store each backend embeds, so a config reload can replace keys while parallel discovery probes read them), and the package-private `binaryInstallHinter` (a managed server's setup hint appended to the "server binary not found" error — Splash implements it). |
| `backend_http.go` | Shared HTTP helpers for backend API calls: `authedGet` / `authedPostJSON` attach `Authorization: Bearer <key>` when a per-server API key is configured, `authFailedErr` maps 401/403 to an actionable "check api_key" error wrapping the exported sentinel `ErrAuthFailed` (every backend's `HealthCheck` routes 401/403 through it, so callers test `errors.Is(err, ErrAuthFailed)` rather than message text; `StartingUp` stays a bool and is false on 401/403), `redactAPIKeyArgs` masks `--api-key` values for display surfaces. The shared response skeletons live here too, so a fix cannot miss one adapter's copy: `expectOK` consumes a load/unload response (bounded body read, auth mapping, non-200 → an adapter-supplied `statusErr` so error extraction stays adapter-owned) and `openAIModelList` reads the OpenAI-style `/v1/models` list that llamacpp, LM Studio and Splash all expose. `probeAddr` maps a wildcard host (empty, `0.0.0.0` or `::`) to the loopback of its family for the dial, since a wildcard is a bind address, not a destination; only Splash's probes call it, and the configured address stays the instance's identity (ADR-0006). Whatever answers on a configured port is untrusted, so `boundedBody` caps every backend response-body read at 512 KB (a squatter streaming gigabytes over loopback must not OOM the launcher — the probe timeout bounds duration, not size) and `sanitizeServerString` strips control characters (C0 incl. ESC, DEL, C1) from server-reported strings before they can reach the terminal. |
| `backend_llamacpp.go` | llama.cpp implementation: server arg assembly, the launched server's environment (`BuildServerEnv` carries the `api_key` as `LLAMA_API_KEY` so it stays out of `ps`), Model path resolution, restart-per-Profile semantics ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)) — `LoadModel`/`UnloadModel` are no-ops. `HealthCheck` and `StartingUp` reject a response carrying the `Server: Splash` header, because Splash's `/health` answers with llama-server's `{"status":"ok"}` body. Registers via `init()`. |
| `backend_ollama.go` | Ollama implementation: HTTP API Model load/unload, auto-start via `ollama serve`, stop via the address-scoped PID path (`TryStop` is a no-op — Ollama's CLI has no server-stop command, `ollama stop MODEL` only unloads a model). `TryStart` asks `requireProcessControl()` before forking, so a platform that could not stop the daemon again never starts one ([§16.6](#166-platform-contract)). Registers via `init()`. |
| `backend_lmstudio.go` | LM Studio implementation: HTTP API Model load/unload, `lms` CLI for server start/stop. Registers via `init()`. |
| `backend_splash.go` | Splash implementation, a second `ManagedLLMServer` restarted per Profile like llamacpp ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)) — `LoadModel`/`UnloadModel`/`TryStart`/`TryStop` are no-ops. `BuildServerArgs` assembles `serve --model <owner/repo>` plus `--host`/`--port`/`--max-context`, and on a non-loopback `host` an `--allowed-host` for the machine hostname and its short form (`machineHostNames`, `isLoopbackHost`; §8.1); `BuildServerEnv` carries the `api_key` as `SPLASH_API_KEY`; `ServerBinary` is `splash`, looked up on `PATH`, with `BinaryInstallHint` naming the wrapper-script setup. `HealthCheck` and the `StartupProber` probe read `/ready` and require the `Server: Splash` header (§5.3); they and `ListRunningModels` dial loopback for a wildcard host (`probeAddr`). `LoadingPID` implements `LoadingProcessFinder`: it finds a Splash that is loading but has not bound its address in the process table (`splashLoadingPID`, `isSplashServeCommand`, `lastFlagValue`; [ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)). `ResolveModel` validates the Hugging Face repo id and refuses a Model not installed in the Hugging Face cache ([§4.4](#44-model-path-resolution), [ADR-0014](docs/adr/0014-splash-models-must-be-installed.md)). `ListRunningModels` reads `/v1/models`. Registers via `init()`. |
| `server.go` | LLM Server lifecycle. Unified start path (fork-and-detach for the managed llamacpp and Splash; backend-supplied `TryStart` for Ollama/LM Studio; a binary missing from `PATH` fails with the backend's `binaryInstallHinter` text when it has one), unified stop path that always tries to stop the process whether or not the launcher started it ([ADR-0001](docs/adr/0001-stop-is-unconditional.md)). `LoadProfile` orchestration with live idempotency check + drift notice from `GET /props` ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)) and unified `auto_unload` rule across same-server and cross-server cases ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)). The orchestration drives the package-private `activationOps` seam ([ADR-0009](docs/adr/0009-activation-operations-seam.md)): the exported `LoadProfile` binds the production adapter `realOps` (one-line delegations to exec/lsof/signal/HTTP implementations), while tests substitute a fake, and the target address is derived from the resolved profile once and carried through the fan-out. The unified `Stop`/`Unload` entry points live here too, driven through the same seam: they return a `StopResult` (instance acted on, server-stopped vs model-unloaded outcome, steps taken) that the CLI and menu format after the fact — the single home of the "unload on a managed backend means stop the server" rule ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md), [ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)). Instances are keyed by `host:port` and rediscovered live each invocation ([ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)). `createLogPath` triggers automatic cleanup when `log_retention` is set to a positive number of days. Lifecycle functions accept an optional `ProgressFunc` callback to report step transitions. Every unix-only primitive the lifecycle needs goes through the `process_*.go` seam — `startManagedServer` asks `requireProcessControl()` before it forks, the stop escalation and `IsProcessAlive` signal through `signalGroup`/`signalPID` ([§16.6](#166-platform-contract)) — and the exported sentinels `ErrStartupTimeout`, `ErrLoadCanceled` and `ErrUnsupported` are declared here; the managed activation's health wait (`waitForHealth`) takes the spawned child's reaped exit as a liveness probe, so a server stopped mid-load ends the load with `ErrLoadCanceled` (§16.2). The drift notice is built by the pure `driftNotice` text builder and delivered to a `NoticeFunc` sink (§16.3): the orchestration takes the sink as its trailing parameter, `LoadProfileNotify` passes the caller's, and the exported `LoadProfile` keeps its signature by binding the CLI's stderr printer. |
| `legacy_state_unix.go` | `ownedByCurrentUser(info)` behind `//go:build unix`: the owner check of the legacy state cleanup — the file's `syscall.Stat_t` uid must equal the current uid (§7.3). |
| `legacy_state_windows.go` | The same signature under `//go:build windows`, accepting every file: a windows owner is an ACL descriptor, not a uid; the name, size and content checks still apply. |
| `redact.go` | Log-text redaction: `RedactLogText(text, keys)` masks every non-blank key it is given and, unconditionally, the value after any `--api-key` flag (`--api-key V`, `--api-key=V`, quoted and list-element spellings) with `[redacted]` — the marker `internal/keystore` uses, re-implemented here because its `redactKey` is unexported. Every path that shows log text goes through it: `TailLog` (the `logs` command, which the MCP `tail_log` tool shells to, and the menus' Show log via `showInstanceLog`) streams `tail`'s output through it line by line, and `readLastLines` (the start-crash tail) masks its result. Both take a trailing `keys` parameter that callers fill from the instance's own backend (`cfg.APIKeyFor(inst.Backend)`), and carry a `// redaction-required` marker so a new caller finds the rule. |
| `process_unix.go` | Process control on darwin and linux, behind `//go:build unix` — the five functions that hold every unix-only primitive the server lifecycle needs: `listProcesses()` (the `ps -A -ww -o pid=,pgid=,command=` process table behind the loading-Splash match, with each session leader's command line replaced by its true argv from `procArgv`, [ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)), `detachedSysProcAttr()` (`Setsid`, so a spawned server outlives the launcher and is stoppable as a process group), `signalPID`/`signalGroup` (today's `syscall.Kill(pid, …)` / `syscall.Kill(-pid, …)` verbatim), and `requireProcessControl()`, which returns nil here ([§16.6](#166-platform-contract)). |
| `process_windows.go` | The same five signatures under `//go:build windows`, each refusing instead of acting: `detachedSysProcAttr` returns nil (no spawn ever reaches it), and the other four return an error wrapping `ErrUnsupported` — so a loading Splash is never found on windows. Windows has no session to detach into and no process group to signal, so the file is what lets the package build — and actuate over HTTP — there ([§16.6](#166-platform-contract)). A real implementation (Job objects, `taskkill`) would land here additively. |
| `proc_argv.go` | The platform-neutral half of reading a process's true argv, untagged so both CI runners test it: `withTrueArgv(entries, argvFn)` replaces the ps-split `Args` of every session leader (PID == PGID) with the argv `argvFn` reads, keeping the ps-split form when the read fails; `parseProcArgs2` decodes darwin's `kern.procargs2` buffer (argc, executable path, NUL padding, NUL-terminated argv) and `parseProcCmdline` linux's NUL-separated `/proc/<pid>/cmdline`. ps joins arguments with spaces, so only the true argv keeps a spaced install path, `--host` or `--port` whole ([ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)). |
| `proc_argv_darwin.go` / `proc_argv_linux.go` / `proc_argv_other.go` | `procArgv(pid)`: the `kern.procargs2` sysctl (`golang.org/x/sys/unix`) on darwin, `/proc/<pid>/cmdline` on linux, an `ErrUnsupported` refusal elsewhere. The read fails for a process that exited or belongs to another user; `withTrueArgv` then keeps that row's ps-split command line. |
| `proc_identity.go` | The platform-neutral half of a process's identity, untagged so both CI runners test it: `sameProcess(pid, identity)` — true only while `processIdentity(pid)` still reads the recorded start time, the check `terminatePID` makes before each stop signal so a reused PID is never signalled (§6.5) — and `parseProcStatStartTime`, which reads starttime (field 22) of linux's `/proc/<pid>/stat` after the last `)`, since `comm` may contain spaces and parentheses. |
| `proc_identity_darwin.go` / `proc_identity_linux.go` / `proc_identity_windows.go` | `processIdentity(pid)`: the process start time from the `kern.proc.pid` sysctl (`golang.org/x/sys/unix`, nanoseconds since the epoch) on darwin, `/proc/<pid>/stat` (clock ticks since boot) on linux, an `ErrUnsupported` refusal on windows. The read fails for a process that has exited. |
| `discovery.go` | `RunningInstance` type and `DiscoverRunningInstances(cfg)` — probes every (backend, address) pair derivable from the config in parallel, returns the reachable set with the loaded Model. Optional runtime details (PID via `lsof`, start time via `ps -o lstart=`, log path via the deterministic naming convention) are populated lazily by `fillRuntimeDetails`. An address where every probing backend answers 401/403 becomes one `AuthFailed` row (`authFailedRows`); `authRefusalAt` applies the same rule to one address for `load`. `instancesSignature` condenses a discovery result into a comparable string (backend, address, loaded model, Starting and AuthFailed per instance) used by the menu to detect background state changes between refresh ticks. |
| `log_cleanup.go` | Log file cleanup: `cleanupLogs` enumerates and deletes old `.log` files by filename timestamp, skipping active server logs. `parseLogTimestamp` extracts creation time from the `{backend}-{YYYYMMDD}-{HHMMSS}.{mmm}.log` naming convention, and from the second-precision `{backend}-{YYYYMMDD}-{HHMMSS}.log` names of older logs. `formatBytes` for human-readable sizes. `autoCleanupLogs` wrapper for silent on-start cleanup. |
| `progress.go` | Step-by-step progress feedback for lifecycle operations. `ProgressFunc` callback type, `progressTracker` (TUI popup that updates in place), `newCLIProgress` (plain text fallback), `printStopSteps` (after-the-fact CLI rendering of the steps a unified `Stop`/`Unload` returns in its `StopResult`). Home of the sibling `NoticeFunc` sink and its `reportNotice` helper (§16.3) — user-facing notices (config warnings, the ADR-0007 drift notice) go to the UI layer through it instead of straight to stderr; a nil sink discards, exactly like `reportStep`. |
| `ui.go` | Low-level terminal operations: raw mode (via `golang.org/x/term`), ANSI escape codes, key reading, reusable `selectMenu()` component. `selectMenu`'s header callback returns `(lines, stale)`; a stale report makes it return the `idxMenuStale` sentinel so callers rebuild instead of acting on an outdated item list. The stdin poll `readKeyTimeout` waits on is per-OS (`ui_poll_*.go`), the file's one platform-specific line before ADR-0012. |
| `ui_poll_darwin.go` | `pollStdin(timeout) bool` — the menu's `select(2)` wait for a keystroke, in darwin's one-value `syscall.Select` form (unchanged from the pre-ADR-0012 `ui.go`), plus `requireInteractiveMenu()` returning nil. A select failure counts as "nothing ready", so the wait degrades to a refresh tick. |
| `ui_poll_linux.go` | The same two functions for linux, whose `syscall.Select` also returns the ready-descriptor count — the single signature difference that used to make the package unbuildable on Linux. The count is discarded; the descriptor set carries the same answer. |
| `ui_poll_windows.go` | The same two functions for windows, which has no `select(2)` over a console handle: `requireInteractiveMenu()` returns `the interactive menu is not supported on windows` wrapping `ErrUnsupported`, and `pollStdin` (never reached) reports nothing ready. The menu never worked here — ADR-0012 turns that from a build failure into a refused verb ([§16.6](#166-platform-contract)). |
| `menu.go` | Interactive menu logic. `RunInteractiveMenu` first asks `requireInteractiveMenu()` (`ui_poll_*.go`) whether this build can poll stdin at all and returns its error if not, which the CLI's zero-arg path prints and exits 3 with ([§3.3](#33-exit-codes), [§16.6](#166-platform-contract)). Enumerates running instances; presents an instance picker for actions that apply to a non-unique target (stop, unload, logs). The open menu runs on two timers: the render tick (`menuTickInterval` — 1 second via `statusTickInterval` when the memory readout is shown, otherwise `refresh_duration`) re-renders the header so the memory/GPU readout stays current, while the backend probe inside `liveStatusHeaderFn` is throttled to `refresh_duration` (config reload + `DiscoverRunningInstances`; the cached discovery result is reused between probes). Each probe compares `instancesSignature` against the signature the menu was built from — on mismatch the open menu returns `errMenuStale` and the loop rebuilds, so background changes (a model loaded via the CLI in another terminal, an external stop or unload) surface without user input and without any state file. Config is also reloaded at the top of each menu loop iteration. The unload/stop actions reduce to target selection plus formatting: they call the unified `Stop`/`Unload` entry points (`server.go`) and render the returned `StopResult` after the fact (`renderStopSteps` — dimmed step lines in terminal mode, CLI style in the non-terminal fallback). `serverStatusLines` appends the optional memory/swap readout (see `sysmem.go` / `memformat.go`) gated by `show_memory_status`, rendered via `Config.CompiledMemoryTemplate()`; templates that carry their own styling (`MemoryTemplate.Styled()`) are emitted as-is, plain templates keep the legacy dim wrap. Profile rows are composed here for every surface: `formatContextSize` produces the compact `65K`/`131K` form, `profileContextCells` turns a Profile list into one right-aligned cell per row — merged value per Profile, displayability gated on `serverShowsContextSize` (`backend.go`), blank cells for the rest, and `nil` when no Profile qualifies so callers render the exact pre-column layout — and `buildProfileItems` (TUI) / `buildSimpleProfileLines` (non-terminal numbered fallback) join that cell with the `[server]` tag and the `favouriteSuffix` `★` marker ([§3.1](#31-interactive-mode), [§4.7](#47-favourite-profiles)). The merged value is read directly rather than through `ResolveProfile`, which stats model files and would run on every render tick. `modelDisplayName` sits beside those helpers for the rows that name a Model rather than a Profile: it collapses a path-shaped server-reported id to its base name and passes name-shaped ids through verbatim ([§3.1](#31-interactive-mode)). Every human-facing read of `RunningInstance.ActiveModel` goes through it — the ones here and the three in `cli.go` — while `cmdStatusJSON` reports the raw id. The three TUI item lists come from `stoppedMenuItems` / `loadedMenuItems` / `idleMenuItems`, and every variant, the numbered fallbacks included, asks `menuOffersEdit` before offering "Edit config": `editConfigCommand` builds `open <config>` on darwin and `$VISUAL`/`$EDITOR` with the terminal attached elsewhere, and reports no command when neither is set ([§3.1](#31-interactive-mode)). Before the loop starts, `offerKeyMigration` raises the plaintext-key offer once per launch ([§3.1](#31-interactive-mode)): the decision itself comes from `keymigrate.go`, so this file only draws it — `askKeyMigration` on the same `selectMenu` / numbered-prompt pair every other action uses, and `showKeyMigrationResult` reporting what the answer changed (and what stopped it) in one popup. |
| `sysmem.go` | macOS unified-memory, swap, and GPU snapshot for the status header. `ReadMemStats` shells out to `sysctl -n hw.memsize`, `sysctl -n vm.swapusage`, `vm_stat`, and `ioreg -r -c IOAccelerator`, with a 0.9-second mutex-guarded cache — just below the menu's 1-second status tick so every tick reads fresh values while per-keystroke re-renders stay cheap. Each shell-out runs under a 2-second context timeout (`memCmdTimeout`), and `memCacheMu` is held only to read and publish the cache, never across a subprocess: a caller arriving mid-refresh gets the last published value (or no readout before the first one lands) instead of waiting. A timed-out refresh skips that tick — it keeps the last good data, or publishes the timeout when there is none — and stamps the cache so the TTL still throttles the retry; an `ioreg` timeout only zeroes the GPU fields. Free RAM follows the Activity Monitor "available" definition; `Compressed` is sourced from `vm_stat`'s "Pages occupied by compressor" line. GPU fields come from the `AGXAccelerator…` entry's `PerformanceStatistics` dict (`Device Utilization %`, `In use system memory`, `Alloc system memory`) on Apple Silicon and degrade silently to `0` on Intel Macs or ioreg failure — `parseIOAccelerator` returns zero values rather than erroring so the rest of the readout still renders. `percentValue` provides the rounded integer percentage (0 on a zero denominator) used by the template engine in `memformat.go`. This is the one feature ADR-0012 does not carry to the other platforms: off macOS the first shell-out fails, `ReadMemStats` returns an error, and `serverStatusLines` simply leaves the readout line out. |
| `memformat.go` | Compiled-template engine for `memory_status_format`. `CompileMemoryTemplate` scans the template once (hand-rolled `{…}` tokenizer, no regex) into a segment list — literals (including pre-resolved ANSI escapes for style tags), value placeholders, and bar specs — and `MemoryTemplate.Render` walks the segments against a `MemStats` snapshot each tick (~0.3 µs, independent of template complexity). Compilation never fails: unknown or malformed tokens pass through literally. Style tags cover the 16 named ANSI colors ({gray} aliasing {bright-black}), 256-color palette indices ({0}–{255}), and 24-bit hex colors ({#rrggbb} / {#rgb}), plus {bold}/{dim}/{reset}; `memColor` resolves any of the three color forms to its foreground and background escapes (named: SGR fg+10; palette: 38;5→48;5; hex: 38;2→48;2). `MemoryTemplate.Styled()` reports whether the template carries its own styling, which disables the menu's legacy dim wrap. Bars (`{pct_name:bar[:width[:color[:bgcolor]]]}`, colors in any of the three forms) render full blocks plus an eighth-block partial cell (8 levels per cell) in the fill color, with the background color painted as an ANSI background behind the partial cell and the empty remainder so the strip is continuous; any nonzero percentage shows at least a sliver, widths clamp to 1–40. `Config.CompiledMemoryTemplate()` (config.go) memoizes compilation keyed on the format string and resolved `memory_status_bar` defaults, recompiling at most once per config reload. |
| `config_test.go` | Tests for config loading, validation (deprecated fields, server enable/disable, auto-assignment, `defaults.server` deprecation warning), parameter merging, boolean accessors, `ExpandTilde` edge cases, `ConfiguredBackendAddr`, `memory_status_bar` resolution (partial blocks, clamping, unknown-color warnings), `CompiledMemoryTemplate` memoization, and the `api_key_cmd`/`plaintext_key_ok` key sources (parsing both entry forms, the both-sources and blank-command refusals, resolution at load, the disabled-entry skip, the failure/oversize/timeout refusals, and the unix trust gate — `TestLoadConfig_KeyCommandTrustGate`: `0600` and a symlink to it run the command, `0620`/`0602` refuse without running it, a command-free file loads at `0666`). |
| `configwrite_test.go` | Golden before/after fixtures for both key-source writers: the rewritten line keeps its indentation, alignment gap and end-of-line comment while every other byte of the file is preserved, each refusal case leaves the file untouched, an entry already in the target state is a no-op, and the written file is mode 0600. |
| `keymigrate_test.go` | Tests for the migration policy against a fake `secretStore`: candidate selection (disabled, acknowledged, command-source and whitespace-only entries are not candidates), the ordered move and its two failure paths (read-back failure and mismatch leave the config byte-identical), the multi-entry answers stopping at the first failure, `decideKeyOffer`'s offer/notice/silence with a counting probe, the notice text, and the stderr notice end-to-end through `Run` on a non-interactive command. |
| `backend_llamacpp_test.go` | Tests for llama.cpp arg assembly (including sampling-flag emission and the api_key staying off argv), the `LLAMA_API_KEY` server environment, Model resolution, httptest-based health check (401/403 → `ErrAuthFailed`, `StartingUp` false), and auth header propagation. |
| `backend_ollama_test.go` | httptest-based tests for Ollama health check (body discrimination, 401 → `ErrAuthFailed`), `LoadModel`, `UnloadModel`, `ListRunningModels`, and auth header propagation. |
| `backend_splash_test.go` | Tests for Splash registration, the `/ready` health check and `StartingUp` probe (the `Server: Splash` header required, foreign 200/503 rejected), arg assembly, the `SPLASH_API_KEY` server environment, `ParamSpecs`, `ServerBinary`, repo-id validation, and the Hugging Face cache install check against temp hubs with all four hub env vars pinned. |
| `backend_lmstudio_test.go` | httptest-based tests for LM Studio health check (cross-backend exclusion), `LoadModel`, `UnloadModel`, `extractLMStudioError`, and auth header propagation (including the discrimination probes). |
| `backend_test.go` | Tests for `GetLLMServer` with known and unknown LLM Server names. |
| `backend_http_test.go` | Tests for `authedGet`/`authedPostJSON` (header present/absent, JSON content type), `authFailedErr`, `redactAPIKeyArgs`, `applyAPIKeys` (apply and clear on reload), `sanitizeServerString` (ANSI/OSC, C0/C1/DEL stripping), the `boundedBody` read cap, and `probeAddr`'s wildcard-to-loopback mapping (`TestProbeAddr`). |
| `redact_test.go` | Table tests for `RedactLogText` (plain key, flag pair, multi-line, empty-keys no-op on flag-free text, empty keys still masking a flag value, `=`/quoted/list spellings, `--api-key-file` untouched, longest key first) and log-surface tests asserting a planted key never reaches output: `cmdLogs` against a fake running instance, and `showInstanceLog` for a non-llamacpp backend. |
| `log_cleanup_test.go` | Tests for `parseLogTimestamp` (millisecond and legacy second-precision stamps), `formatBytes`, and `cleanupLogs` (empty dir, nonexistent dir, old/new file filtering for both stamp forms, `--all` mode, non-log file safety). |
| `server_test.go` | Tests for `IsProcessAlive` (including PID 0 guard), `readLastLines`, the start-crash tail's key redaction (`TestStartManagedServer_CrashTailRedactsKeys`), the binary-not-found install hint (`TestStartManagedServerBinaryInstallHint`), `createLogPath`'s distinct, exclusively created per-start log files and its non-truncating collision retry (`TestCreateLogPath_*`), the legacy state cleanup (`TestCleanupLegacyStateFiles`: launcher-written `state-*.json` / `state.json` removed, including a pid-0 external connect; look-alike names, non-state JSON and broken JSON kept; `TestIsLegacyStateFile`: oversized files, symlinks and directories refused), `RunningInstance` methods (`Addr`, `Uptime`), `paramDrift`, `liveParamDrift`, `shouldCrossServerUnload`, `WaitForHealth`, the stop decision layer (`StopInstance`, `identifyBackend` including its 401/403 third pass, `terminatePID` — `TestTerminatePID_MismatchedIdentity`: a real child whose start time differs from the recorded identity is left unsignalled), the auth-refusing server legs (`TestStopServerAt_AuthFailedListener`: listener signalled, no native hook, a surviving 401 not reported stopped; `TestUnloadInstanceModel_AuthFailed`; `TestLoadProfile_Orchestration_AuthFailed`, `TestLoadProfile_RefusesAuthFailedServer`, `TestLoadProfile_StopsHealthySplashAnswering403`; the auto-stop sweep skipping an AuthFailed row), the loading-Splash seam (`TestStartingUp_LoadingSplash`, `TestDiscoverRunningInstances_ReportsLoadingSplash`, `TestStartManagedServer_RefusesLoadingSplash`, `TestStopInstance_LoadingSplash`: a faked process table and listener lookup drive `startingUp`, discovery, the start refusal, and a real stop of a detached `sleep` child; [ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)), and the `LoadProfile` orchestration ([ADR-0009](docs/adr/0009-activation-operations-seam.md)): fake-`activationOps`-driven tests for the idempotent no-op, `--restart`, the `auto_stop_server`/`auto_unload` matrix, and the external swap/connect fork, plus httptest-backed tests (real probes, recorded stops via `stopRecordingOps`) for the shared-address foreign-occupant stop and the still-starting-up double-spawn refusal. The unified `Stop`/`Unload` entry points are covered through the same seam (`steppedOps`): managed backend → server stop, external backend → API unload with the server left running, step collection into the `StopResult`, and error paths, including the refusal when another backend holds the address. `TestLoadProfile_StartupTimeoutIsErrStartupTimeout` drives the **real** activation wait loop against a never-healthy stand-in for the `ErrStartupTimeout` contract ([§12.2](#122-server--config-tests)), and `TestLoadProfile_ServerExitMidWaitEndsTheLoad` drives it while a forked child exits mid-wait for the `ErrLoadCanceled`/crash split; `TestStartupTimeoutErr_ManagedMessageUnchanged` pins the managed timeout text byte for byte, and the `TestConnectExternal_*` tests drive `connectExternalServer` with fake never-healthy external backends (§12.2). |
| `server_reap_test.go` | Start-crash detection test driven by an in-package fake `ManagedLLMServer` whose forked child exits immediately: `StartServer` must return the "exited immediately" error rather than a `RunningInstance`, verifying the child is reaped (not left as a zombie that a liveness-only check would read as alive). |
| `cli_test.go` | CLI-layer tests: `cmdStart` fail-fast without a profile, `cmdStatusJSON` per-instance enumeration and idle-backend entries, `cmdList` ★-column and context-column display-width alignment with multi-byte titles, and `Run`-dispatcher exit-code contract tests (usage errors → 2, nothing-running stop/unload → 1, `status --json` exit 1 with valid JSON). `TestCmdStop_StopsAuthFailedServer` stops a re-executed child of the test binary that answers 401 to everything (`TestAuthRefusingHelperProcess`) and pins the `Stopped server at <addr> (PID n)` report; `TestCmdLogs_NoTargetSkipsAuthFailed` pins a bare `logs` picking the Starting llamacpp beside an AuthFailed row. `TestCmdStatus_RendersAuthFailedInstance`, `TestCmdStatusJSON_ReportsAuthFailedInstance` and `TestCmdUnload_AuthFailedInstance` pin the AuthFailed rendering: the shared `auth failed at <addr>` label in `status` and the ambiguous-unload listing, the `backend: ""`/`running: false`/`auth_failed: true` JSON object, and unload's `authFailedErr` refusal. |
| `discovery_test.go` | httptest-based tests for `DiscoverRunningInstances` (reachable / unreachable, hostile model names sanitised), `LlamaCpp.ListRunningModels` (including the oversized-body cap), `LlamaCpp.QueryLiveParams` (`/props` populated and 404 fallback), and `findManagedLogFile` (most-recent picker by lexicographic timestamp). |
| `menu_test.go` | Tests for `parseChoice`, `formatUptime`, `profileDisplayName`, the Profile-list context-size column (`formatContextSize`'s number format plus direct coverage of `buildProfileItems` and `buildSimpleProfileLines`: merged values, right-alignment, the blank Ollama cell, the presence gate's byte-identical pre-column shape, the single-enabled-server case, and the still-rightmost `★`), and `formatProfileParams` (`TestFormatProfileParams_LMStudio` asserts LM Studio profiles omit GPU offload — it is not part of the load request — while showing the params the load request does send; `TestFormatProfileParams_RendersBackendOwnedSpec` registers a stub backend and asserts the pop-up renders exactly its `ParamSpecs`, in order, proving a new backend needs no menu edit; API-key redaction and the empty Ollama spec are covered separately). |
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
    ParamSpecs() []ProfileParamSpec
}

type ProfileParamSpec struct {
    Label  string
    Format func(p *ProfileParams) (value string, ok bool)
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

type StartupProber interface {
    StartingUp(addr string) bool
}

type binaryInstallHinter interface { // package-private
    BinaryInstallHint() string
}
```

The interface name `LLMServer` matches the domain language in [CONTEXT.md](CONTEXT.md).

The `LLMServer.TryStart` / `LLMServer.TryStop` pair drives the unified lifecycle: each LLM Server type encapsulates its own start mechanism (fork-and-detach for llamacpp and Splash; `ollama serve` for Ollama; `lms server start` for LM Studio) and stop mechanism (signal-to-PID for llamacpp, Splash and Ollama, whose `TryStop` is a no-op — the address-scoped PID path is its stop; `lms server stop` for LM Studio). The launcher does not branch on "did we start this?" — see [ADR-0001](docs/adr/0001-stop-is-unconditional.md).

`ManagedLLMServer` extends `LLMServer` for LLM Server types where the launcher knows how to fork the server process directly (llamacpp and Splash). The server lifecycle code uses `if mb, ok := b.(ManagedLLMServer)` to decide whether to assemble argv and fork, or to call `LLMServer.TryStart`. A managed server may also implement the package-private `binaryInstallHinter`: when `exec.LookPath` cannot find its `ServerBinary`, `startManagedServer` appends the hint to the "server binary not found" error. Splash implements it to explain the wrapper-script setup and the launchd / MCP-adapter `PATH` (§4.2); llamacpp keeps the bare message.

`PIDTracker` is implemented by LLM Servers that auto-start a server process and can report the resulting PID (Ollama). The launcher uses it for `status` display only; liveness and stop decisions go through `HealthCheck` and `lsof`, not PID.

`ModelLister` is implemented by LLM Servers that report their currently-loaded Models: llamacpp via `/v1/models`, Ollama via `/api/ps`, LM Studio via `/v1/models`, Splash via `/v1/models`. Discovery uses it on every invocation to know what is actually loaded (instead of relying on a persisted snapshot that can drift from reality when a Model is loaded externally).

`LiveParamsQuerier` is implemented by LLM Servers that report their currently-active parameters: llamacpp via `/props` (per-slot n_ctx scaled by total_slots into the profile's total `context_size`, plus total_slots as `parallel`; sampling settings are deliberately not read — the launcher passes them only as launch flags that set request defaults, API clients override them per call, and `/props` floats need not round-trip the configured values exactly, so diffing them would manufacture spurious drift). Used by ADR-0007 drift detection — the launcher compares live params against the freshly resolved Profile instead of a persisted snapshot, and only the fields the server actually reports are compared, so unreported fields never manufacture drift. Ollama, LM Studio and Splash do not implement this; on those backends, model-name match alone is the idempotency signal.

`LLMServer.ParamSpecs` makes each backend the single source of truth for which profile parameters it *actually applies* — and therefore which ones the "Show model config" pop-up may display. Each spec pairs a display label with a formatter over `ProfileParams` (`ok=false` for an unset field skips the line); `menu.go` renders the list generically, with no backend-name branching, so a parameter a backend never sends can never be displayed for it and a new backend needs no menu edit — the compiler forces it to declare its spec. The shared `spec*` values in `backend.go` keep labels/formatting identical for parameters honoured by several backends. Current ownership: llamacpp lists everything `BuildServerArgs` turns into launch flags, the sampling parameters included; LM Studio lists the four fields its load endpoint accepts (`context_size`, `batch_size`, `flash_attn`, `parallel`); Ollama lists none, because its load request carries only the model name and a keep-alive; Splash lists only `context_size`, the one profile parameter it receives as a launch flag (`--max-context`) — sampling parameters are not passed to Splash and so are not displayed.

`StartupProber` is implemented by LLM Servers that can tell a server that is reachable but still starting up apart from one that is not running at all: llama-server answers `/health` with 503 Service Unavailable while it loads its model, before turning healthy. The managed start path probes this before every fork and refuses to spawn a duplicate onto an address where an earlier server is still coming up (§6.2) — a still-loading server fails every backend's health check, so without this probe a retry would treat the address as free. Since [ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md) the same probe is the health check's fallback in two more places: discovery surfaces a positive answer as a Starting instance (§7.2), and the stop path's identification runs a `StartingUp` second pass so a Starting instance can be stopped (§6.5). Both llamacpp and Splash implement it. Splash's probe accepts `/ready` → 503 carrying its `Server: Splash` header, but the Splash build tested binds its port only once the Model has loaded, so a loading Splash refuses the connection and the probe cannot see it. Ollama and LM Studio have no Starting window (their server is healthy before any Model is loaded) and do not implement the interface.

`LoadingProcessFinder` covers the Splash gap ([ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)). It is implemented by LLM Servers whose process loads its Model before it binds its address; only Splash implements it. `LoadingPID(addr)` returns the PID of a server process loading for `addr`, found in the process table (`processTable`, backed by `listProcesses`: `ps -A -ww -o pid=,pgid=,command=` on unix, `ErrUnsupported` on windows). ps prints each command line joined by spaces, so `ps` supplies only the PID and PGID by position; a session leader's command line is its true argv, read from the kernel (`procArgv`: `kern.procargs2` on darwin, `/proc/<pid>/cmdline` on linux), so a spaced path, `--host` or `--port` stays one argument. A leader whose argv cannot be read keeps the whitespace-split command. A match needs three things. First, the process must lead its own process group (PGID == PID, as the launcher's `Setsid` fork makes it). Second, its command line must be a Splash server launch: `--model` present, and either `serve` preceded by `splash` or `launcher.py`, or a `--binary` whose base name is `splash`. Those cover the forms one launch `exec`s through, in the same process: the `splash` wrapper script, the checkout's `splash` script, `install/launcher.py serve` and `server/server.py`. Third, the last `--host` and `--port` must equal the address, with Splash's default address filling an absent flag. The package helper `startingUp(b, addr)` is the single "is this Starting?" question behind every use: a `StartupProber` answer, else `loadingPID(b, addr)`. That helper trusts the finder only while `addrHasListener(addr)` (the `lsof` lookup) reports nothing listening, so a process that holds the address is never judged by its command line. `processTable` and `addrHasListener` are package variables so tests can substitute them.

Per-server API keys do not appear in any interface signature: several call paths (`identifyBackend`, `WaitForHealth`, instance stop/unload) have no `*Config` in scope. Instead each backend struct embeds the package-private `apiKeyHolder` — an RWMutex-guarded key store whose promoted `setAPIKey`/`apiKey` accessors implement `apiKeyConfigurable` — set by `applyAPIKeys` at the end of `LoadConfig` (and thus refreshed on every `Reload`), and attaches the key as a `Bearer` header via the helpers in `backend_http.go`. The mutex matters because discovery probes run in parallel goroutines: a reload may replace a key while a probe reads it, which the race detector flagged when the key was a bare field. Ollama's last-started bookkeeping shares that mutex: `TryStart` records the spawned PID and log path under its write lock, and the `PIDTracker` accessors (`LastStartedPID`/`LastStartedLogFile`) read them under its read lock. Health-check discrimination is unaffected: a key-protected llama-server still answers its auth-exempt `/health`, and it 401s LM Studio's `/v1/models` probe (a correct rejection); LM Studio's own probes carry the key so they keep working when its token requirement is enabled.

Adding a new LLM Server requires:
1. Create `backend_<name>.go` implementing `LLMServer` (and optionally `ManagedLLMServer`).
2. Register via `init()` calling `RegisterLLMServer()`.
3. Add the LLM Server name to `servers:` in the config YAML.
4. Set `server:` on relevant Profiles.
5. Add the name to the MCP adapter's pinned `knownBackends` (`cmd/llama-launcher-mcp/validate.go`, §15.2), which does not import `internal/launcher`.

#### Health Check Discrimination

All backends may share the same address so that a single client configuration works regardless of which backend is active. Each backend's `HealthCheck` must therefore identify whether the responding server is actually its own, not another backend running on the same port.

| Backend | Primary Endpoint | Discrimination |
|---|---|---|
| `llamacpp` | `GET /health` → 200 | Body must parse as JSON with a non-empty `"status"` field (e.g. `{"status":"ok"}`). LM Studio returns 200 for all paths but with `{"error":"..."}` — the missing `"status"` field rejects it. Splash returns the same `{"status":"ok"}` body, even while loading, so a response carrying the `Server: Splash` header is rejected first; the `StartingUp` probe likewise ignores a 503 carrying that header. |
| `lmstudio` | `GET /v1/models` → 200 | Excludes llamacpp (rejects if `/health` body has a `"status"` field — which also rejects Splash, whose `/health` body is the same) and Ollama (rejects if `/api/tags` body parses as JSON with a `"models"` field). LM Studio returns `{"error":"..."}` for both paths. |
| `ollama` | `GET /` → 200 | Body must contain "Ollama" (positive identification). |
| `splash` | `GET /ready` → 200 | Response must carry a `Server` header starting with `Splash` (positive identification by header). `/ready` is used instead of `/health` because Splash's `/health` answers 200 `{"status":"ok"}` even while the model loads; `/ready` is 200 only once serving. The `StartingUp` probe accepts `/ready` → 503 only with the same header, so a foreign 503 is never taken for a loading Splash; the Splash build tested never serves that 503, because it binds its port only once the model has loaded. A loading Splash is instead identified by its process (`LoadingProcessFinder`, §5.3, [ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)) while nothing listens at the address. Splash refuses a `Host` naming a wildcard bind address, so for a wildcard or empty host both probes dial loopback (`probeAddr`) while the instance keeps its configured address (ADR-0006). |

When adding a new backend that shares an endpoint with an existing one (e.g. `/v1/models`), add an exclusion entry to any existing backend whose health check could false-positive, and ensure the new backend either uses a unique endpoint or performs its own exclusion checks.

**Technical reasoning — why content-based discrimination:**

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
- **splash** cannot be told apart by body at all: its `/health` returns llama-server's exact `{"status":"ok"}`, so llamacpp's body check alone would claim a Splash server (and its 503 `StartingUp` probe a loading one). Splash sends `Server: Splash` on every response, so Splash identifies itself by that header and llamacpp rejects any response carrying it.

The general rule: when backends share a port, **discrimination must rest on response content — the body, or a response header that positively identifies the server**. Status codes alone are insufficient because some servers (LM Studio) return 200 for every path. This supersedes the earlier "all discrimination must be body-based" rule: Splash needs the header because its `/health` body matches llama-server's.

### 5.4 External Dependencies

| Module | Purpose |
|---|---|
| `gopkg.in/yaml.v3` | YAML config parsing |
| `golang.org/x/term` | Raw terminal mode for arrow-key menu navigation |

Standard library only beyond that. No TUI framework; ANSI escape codes are used directly for colors and screen control.

**Runtime binaries.** The Go build has no other dependencies, but each LLM Server's own program must be installed for the launcher to start it, looked up on `PATH` at start time only:

| Server | Binary | Used for |
|---|---|---|
| `llamacpp` | `llama-server` | Forked per Profile (§6.2). |
| `ollama` | `ollama` | `ollama serve` auto-start. |
| `lmstudio` | `lms` | `lms server start` / `lms server stop`. |
| `splash` | `splash` | Forked per Profile as `splash serve` (§6.2). May be a wrapper script (§4.2); a launchd or MCP-adapter `PATH` must include its directory. Never needed to resolve a Splash Profile (§4.4). |

## 6. Process and Model Management

The launcher operates on **LLM Server instances**, each identified by its `host:port` address ([ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)). Multiple instances of any LLM Server type may run concurrently as long as each binds a distinct address.

### 6.1 Activating a Profile

`load <profile>` is the canonical entry point. It performs (in order):

1. Resolve the Profile (merge defaults, validate parameters, resolve Model path).
2. Compute the target address from the resolved Profile.
3. Probe the target address (`LLMServer.HealthCheck`), and — for a `StartupProber` backend — whether a server there is still starting up (Starting, [ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)). When it is neither, ask `activationOps.authRefusal` (`authRefusalAt`, discovery's AuthFailed rule, §7.2) whether the server there refuses the configured api_key; if so, refuse with the `authFailedErr` message (`errors.Is(err, ErrAuthFailed)`). The Profile backend's own health error never decides this: a healthy Splash on `0.0.0.0` answers llamacpp's wildcard-Host probe with 403 and is still a foreign occupant to auto-stop.
4. **Idempotency check** ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)): if the target is healthy, ask the LLM Server which Model is loaded (`ModelLister.ListRunningModels`). If it matches the resolved Profile's Model:
   - For `llamacpp`, query `LiveParamsQuerier.QueryLiveParams` (`GET /props`) and diff against the freshly resolved Profile.
   - If no drift (or backend doesn't expose live params), exit silently (no-op).
   - If drift is detected, print a notice to stderr naming the divergent fields and pointing the user to `--restart`. Exit silently otherwise (no-op).
   - If `--restart` is given, fall through to step 5.
5. **`auto_stop_server` / `auto_unload`** ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)): discover every other running instance via `DiscoverRunningInstances`.
   - If `auto_stop_server: true` (default), stop instances whose address differs from the target's — Starting instances included: a loading server already consumes the memory the flag protects (ADR-0010). AuthFailed rows are skipped: no backend identified them, so only an explicit `stop` signals them.
   - If `auto_stop_server: false`, leave them running. Then, regardless of `auto_stop_server`, if `auto_unload: true` (default), unload any Model on any still-running instance that is not the one we are about to load. A Starting instance is never an unload candidate here — it reports no Model.
6. Start the LLM Server at the target address if it isn't running (§6.2). A Starting occupant at the target address is displaced only by an explicit `--restart` (stopped, then replaced, like a healthy occupant); a plain `load` refuses with guidance pointing at `llama-launcher stop` / `--restart` (ADR-0010).
7. Load the Model (§6.3).

The whole sequence runs against the package-private `activationOps` seam ([ADR-0009](docs/adr/0009-activation-operations-seam.md)): the exported `LoadProfile` binds the production adapter (`realOps` — exec/lsof/signals for processes, backend HTTP for probes), and the orchestration tests in `server_test.go` drive the same code against an in-memory fake, covering the idempotent no-op, `--restart`, and the `auto_stop_server`/`auto_unload` matrix without real processes. The target address is computed once (step 2) and carried through the fan-out rather than re-derived per call site.

### 6.2 Starting an LLM Server

The path forks on whether the backend implements `ManagedLLMServer`:

**`ManagedLLMServer` (llama.cpp, Splash):**

1. Ask `startingUp` (§5.3) whether a server at the target address is still starting up. For llamacpp that is `StartupProber.StartingUp()`: llama-server answers `/health` with 503 while it loads its model. For Splash, which has not bound its port while it loads, it is `LoadingProcessFinder`: a Splash launch for the address in the process table ([ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)). If one is, refuse to fork a duplicate — it could only collide with the loading server — and fail with an error naming the loading server's PID (via `lsof`, or the process table for a loading Splash) and log path, plus guidance pointing at `llama-launcher stop <backend>` or `--restart` ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md); the guidance used to say `kill <PID>`). A plain load or bare start leaves the loading server alone; `load --restart` displaces it — the activation stops the Starting occupant first (§6.1 step 6), so this in-start probe remains only as the backstop for the race where an occupant appears (or survives a failed stop) between that decision and the fork.
2. Check the target port for a foreign occupant (`portOccupants`, built on the same `lsof` lookup as §6.5): if any process is still listening there, refuse and fail with an error naming the port, the address the backend cannot bind, and every listening PID with its executable name (via `ps -o comm=`, reduced to its base name). By this point the activation has stopped a healthy or Starting server of this backend (§6.1 step 6) and step 1 has ruled out one of ours still coming up, so a remaining listener is something the launcher does not control and will not step aside — the fork could only end in the backend's own "couldn't bind HTTP server socket" and die inside the startup grace window, surfacing as a step-8 log tail that names neither the port's occupant nor, when the occupant shadows loopback, why discovery reported nothing running at all. The check is advisory in one direction only: when `lsof` cannot answer, it reports no occupant and the start proceeds, so a check that cannot run never blocks a start that would have worked. It is deliberately port-wide rather than address-specific, because a listener on any interface of that port is enough to fail the bind — a foreign process holding `127.0.0.1:<port>` blocks a `0.0.0.0` bind just as a wildcard listener does.
3. Resolve the backend; verify binary exists via `exec.LookPath` (`llama-server`, or `splash` for Splash — §5.4). A missing binary fails with "server binary not found", followed by the backend's `binaryInstallHinter` text when it has one: Splash's says to put its `splash` command on `PATH`, for a source checkout through a wrapper script that execs `<checkout>/splash` (a plain symlink breaks it), and that a launchd or MCP-adapter `PATH` must include the wrapper's directory (§4.2).
4. Build server arguments via `ManagedLLMServer.BuildServerArgs()` and environment via `BuildServerEnv()`. For llamacpp the Model path is in `--model`, so the Model is baked into the server's start arguments ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)); Splash likewise gets its `owner/repo` Model as `serve --model`, and its `api_key` as `SPLASH_API_KEY` in the environment. A Splash Model reached this step only if it is installed (§4.4), so the start never triggers Splash's download.
5. Open the log file for stdout/stderr redirection.
6. Create `exec.Cmd` with `SysProcAttr{Setsid: true}` to detach the child process.
7. Call `cmd.Start()` (non-blocking), then reap the child with a `cmd.Wait()` goroutine (`watchExit`) that reports the exit through a `processExit` — a channel closed on exit plus the `Wait` result — which the returned `RunningInstance` carries.
8. Detect early exit (bad arguments, a port taken in the race after step 2, etc.) by selecting on that channel against a 500 ms (`startupGracePeriod`) timer: an exit within the window returns the "server exited immediately after start" error with the log tail (masked by `RedactLogText`), while a still-running child leaves the wait goroutine parked to reap it later. Reaping is required — an unreaped child that dies becomes a zombie that still satisfies `kill(pid, 0)`, so a liveness-only check would report a dead server as alive.
9. Wait for backend health check to succeed (up to 15 seconds on `start`, 30 on `load`). On timeout the spawned process is **left running** — a large Model on a cold disk can legitimately need longer — and the error names its PID and log path with recovery guidance: watch the log and retry once healthy, or `llama-launcher stop <backend>` (ADR-0010 made stopping a Starting server the launcher's own job, so the guidance no longer points at a manual `kill`). A plain retry while it is still loading hits the step-1 refusal; a retry after it turns healthy is the idempotent no-op ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)). On `load` the wait also watches the spawned process's exit: when it ends mid-wait the load returns at the next poll gap instead of waiting out the window — a non-zero exit code as a crash error with the redacted log tail, anything else (status 0, a signal, 128+SIGTERM/SIGINT, an unreadable status — what a `stop` of the loading server produces) wrapping `ErrLoadCanceled` (§16.2).
10. Print confirmation. The active Model and parameters are observable on subsequent invocations via the LLM Server's own API.

**Plain `LLMServer` (Ollama, LM Studio):**

1. Resolve the address from `servers` map (if host:port value) or `LLMServer.DefaultAddr()`.
2. Call `LLMServer.HealthCheck(addr)` to verify server is reachable.
3. If not reachable, call `LLMServer.TryStart()` (e.g. `lms server start`, `ollama serve`). A `TryStart` failure is returned wrapped — `<Server> not reachable at <addr> and could not be started: <TryStart error>` — so its own text (the binary missing from `PATH`) and any sentinel it carries (`ErrUnsupported` on windows) reach the caller. A forked `ollama serve` child is reaped by a `cmd.Wait()` goroutine so it cannot linger as a zombie in a long-lived launcher process (a zombie still satisfies `kill(pid, 0)` and would stall a later stop's signal escalation).
4. Poll health check until successful (up to 15 seconds, `externalStartWait`). On timeout the started server is **left running**, as on the managed arm, and the error wraps `ErrStartupTimeout` (`externalStartupTimeoutErr`). It names the spawned PID and log path only when the backend implements `PIDTracker` (Ollama; LM Studio does not, so neither a `PID 0` nor an empty `Log:` line is printed), and its guidance is to retry once the server is healthy — it names no `logs`/`stop` command, since both find an external server only after it answers its health check.
5. Print confirmation.

### 6.3 Loading a Model

**For `ManagedLLMServer` (llama.cpp, Splash):** Loading is fused with server start — the Model is in the start arguments and there is no separate API call. If a different Profile is already active at the target address, that instance is stopped first (the stop is unconditional — [ADR-0001](docs/adr/0001-stop-is-unconditional.md)) and a new server is started with the new Model. `LLMServer.LoadModel`/`UnloadModel` are no-ops on llamacpp and Splash.

**For plain `LLMServer` (Ollama, LM Studio):** Call `LLMServer.LoadModel(addr, resolvedProfile)`. This is an HTTP request to the server's load endpoint. The currently-loaded Model is observable on subsequent invocations via the LLM Server's own API (`/api/ps`, `/v1/models`).

### 6.4 Unloading a Model

`unload [profile]` always means "the Model is no longer loaded after this returns successfully."

- **llamacpp / Splash:** stops the server (the Model is part of the server's args — there is no API-level unload). A Starting instance — still loading its Model — is a valid unload target and reduces to the same stop ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)); the ambiguous-target listing labels it `(starting…)`.
- **Ollama / LM Studio:** calls `LLMServer.UnloadModel` via HTTP. The server stays running with no Model loaded — visible on the next `status` invocation as a healthy server with no `active_model`.

The `auto_unload` flag governs whether an unload is *implicit* during a Profile activation (§6.1 step 5); the user-invoked `unload` subcommand is always explicit.

### 6.5 Stopping an LLM Server

`stop [target]` is unconditional ([ADR-0001](docs/adr/0001-stop-is-unconditional.md)) — the launcher does not distinguish servers it started from servers that were already running.

Before anything is signalled, the occupant of the address is identified (`identifyBackend`) — the launcher never signals a process it cannot attribute to a known backend ([ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)), with one exception: a listener answering an LLM API path at a configured address with 401/403. Identification runs three passes, each over the registered backends in sorted-name order for determinism: first the discriminating health checks, then `startingUp` — the `StartingUp` probes of backends implementing `StartupProber`, and the process-table match of backends implementing `LoadingProcessFinder` — because a Starting instance fails its health check for the whole Model load but must still be identifiable so it can be stopped ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)). The second pass accepts a weaker discrimination signal — consciously accepted, confined to addresses the config already assigns to the launcher:

- for llamacpp, a bare 503 on `/health` without the `Server: Splash` header, versus the `{"status":"ok"}` body the healthy check requires;
- for Splash, a 503 on `/ready` carrying that header;
- for a Splash that has not bound its address yet, a process-group leader running a Splash launch with the address's host and port, counted only while nothing listens there ([ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)).

The third pass runs only when the first two found nothing: a backend whose health check answered 401/403 (`errors.Is(err, ErrAuthFailed)`) marks a server refusing the configured api_key. It returns the sort-first such backend's name together with an error wrapping `ErrAuthFailed`: `stop` proceeds — signalling the listening PID only, with no loading-process lookup and no native hook, and reporting `Backend ""` since no backend identified the server — while `UnloadInstanceModel` and `Unload` refuse with that error (`unloadServerModel` asks `activationOps.identify` before the managed/external split, so a managed unload never turns into a stop of such a server). An address where no pass identifies anything still refuses with `ErrNotRunning`: the foreign-occupant protection survives for everything that is not an LLM API refusing auth. `Unload(backend, addr)` acts only on the backend it names: when `identify` names another backend at `addr` it refuses before either arm, signalling and unloading nothing, with `no <backend> server at <addr> (<occupant> is serving there)`, an error matching `ErrNotRunning`. Only a positive mismatch refuses; an address where nothing is identified falls through to the managed/external arms as before.

The launcher then attempts both available mechanisms, each exactly once, in order (a single routine behind `StopInstance` runs both — there is no second pass):

1. Discover the listening PID via `lsof -nP -iTCP@host:port -sTCP:LISTEN -t` (host-specific first, then a port-only fallback for servers bound to `0.0.0.0`). When nothing listens, a loading Splash's PID comes from the process table instead (`loadingPID`, [ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)). If found and alive, record its identity — its start time, read by `processIdentity` (the `kern.proc.pid` sysctl on darwin, field 22 of `/proc/<pid>/stat` on linux) — then send `SIGTERM` to the process and its process group (`kill(-pid, SIGTERM)`). Poll for exit (100ms intervals, up to 15 seconds). If still alive, send `SIGKILL`, then wait up to 5 seconds for the process to die, then 500ms for the OS to release the TCP port. Every signal is sent only while the PID's identity still matches the recorded one — checked before `SIGTERM`, on every poll and again before `SIGKILL` — because an exited server's PID may already name an unrelated process; a mismatch or an unreadable identity sends no further signal and counts the PID as gone.
2. Call `LLMServer.TryStop(addr)` — skipped for a third-pass (auth-refusing) server — so the backend can run its native shutdown mechanism (`lms server stop` for LM Studio; a no-op for llamacpp, Splash and Ollama — Ollama's CLI has no server-stop command, `ollama stop MODEL` only unloads a model, so the address-scoped PID signal in step 1 is its stop mechanism). This is best-effort and idempotent — a hook failure surfaces only if the address is still serving afterwards; it never blocks a stop that already succeeded.

The stop fails with an error when the address is still reachable after both steps — and "stopped" means the health check fails with something other than a 401/403 (a survived auth-refusing server still answers `ErrAuthFailed`) **and** `startingUp` answers no — neither the `StartingUp` probe nor, for Splash, the process-table match finds the server. A *survived* Starting server also fails the health check (it keeps answering 503), so health alone would report a failed stop as success ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)). Then print confirmation. There is no state file to remove.

**Technical reasoning — process group signals:**

Forked servers are started with `SysProcAttr{Setsid: true}`, which gives the child its own session and process group (PGID = PID). Sending `SIGTERM` to just the PID signals only the main process. If the server has spawned child processes (e.g. worker threads for CUDA/Metal, Model loading), those children may keep the main process alive or hold resources. Sending the signal to the entire process group via `syscall.Kill(-pid, sig)` ensures all children also receive it. Errors from the group signal are ignored (the group may not exist if the process already exited).

**Technical reasoning — SIGKILL port release wait:**

After `SIGKILL`, the stop path must wait for the process to actually die before returning. Without this wait, the TCP port may still be held by the dying process when the next backend tries to start on the same port, causing a 15-second health check timeout ("server did not become healthy within 15s"). The implementation polls the PID's identity (`sameProcess`, §6.5 step 1) for up to 5 seconds after SIGKILL, then waits an additional 500 ms (`startupGracePeriod`) for the OS to release the TCP socket in the `TIME_WAIT` / cleanup phase.

### 6.6 Status Check

1. Call `DiscoverRunningInstances(cfg)` — probes every (backend, address) pair derivable from the config and returns the reachable set with the loaded Model and live params for each, including Starting instances (§7.2, [ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)).
2. Print one row per live instance with: backend, address, active Profile (matched against config by backend + address + model), active Model. A Starting instance renders `starting…` in the state column and names no Profile or Model — its details line shows a `Starting:` marker with the backend name instead. An AuthFailed instance is one row, `auth failed at <addr> — check api_key in the servers section`, with no state column and no details line. PID, uptime, and log file are populated lazily via `lsof`, `ps -o lstart=`, and a glob of `{log_dir}/{backend}-*.log`; `lsof` finds a Starting server's PID like any other, and a loading Splash's PID comes from the process table, so its details render too.

Exit 0 if any instance is discovered (healthy, Starting or AuthFailed); exit 1 if all are stopped.

### 6.7 Stale State Handling

There is no state. Each invocation is a fresh look at the live LLM Servers. Health checks are discriminating — each backend identifies its own server and rejects responses from other backends sharing the same address (see [§5.3 Health Check Discrimination](#health-check-discrimination)). A server that crashed since the last invocation simply isn't in the discovered set; a Model loaded externally on Ollama or LM Studio is.

### 6.8 `auto_stop_server` and `auto_unload`

These two flags determine what happens to *other* running instances when a Profile is activated.

| `auto_stop_server` | `auto_unload` | Behaviour for instances *other than* the activation target |
|---|---|---|
| `true` (default) | (any) | All other instances are stopped. |
| `false` | `true` (default) | Other instances stay running. Any Model loaded on them is unloaded (`LLMServer.UnloadModel`) — same rule for same-server swap and cross-server case ([ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md)). |
| `false` | `false` | Other instances stay running with their Models intact. |

`auto_unload` is silently ignored on llamacpp and Splash instances (Model swap requires a server restart — [ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)).

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
    Starting      bool              // address bound, health check not yet passing (ADR-0010)
    AuthFailed    bool              // every configured backend answers 401/403; Backend ""
}
```

The struct is transient — built fresh in memory on each invocation, never serialised. `PID`, `StartedAt`, and `LogFile` are best-effort fields populated by `fillRuntimeDetails` only when a command needs them (status display, log tailing). `Starting` marks an instance whose process is up and address bound but whose health check does not pass yet — llama-server answers `/health` with 503 for the whole Model load ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)). `AuthFailed` marks an address whose server refuses the configured api_key (§7.2): such a row has `Backend ""`, no Model or Profile, and is at most one per address.

### 7.2 DiscoverRunningInstances

`DiscoverRunningInstances(cfg)` enumerates every (backend, address) pair derivable from the config:

- Each enabled backend's configured address (`cfg.ConfiguredBackendAddr(name)`).
- Each Profile's resolved address (`host:port` from the merged Profile params), so Profiles that bind a backend to a non-default port are still discovered.

It probes them all in parallel with `LLMServer.HealthCheck`. A failing health check does not always mean the address is empty: discovery falls back to `startingUp` (§5.3) — the `StartingUp(addr)` probe of a backend implementing `StartupProber`, or, for Splash, a loading server process found in the process table while nothing listens at the address — and a positive answer yields an instance with `Starting: true` ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md), [ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)) — `ListRunningModels` is skipped (the server cannot answer yet), so `ActiveProfile`/`ActiveModel` stay empty; with no Model to match, several Profiles sharing the address would be ambiguous anyway. A health check answering 401/403 with no Starting server behind it is recorded, not dropped: an address where **every** probing backend answered 401/403 yields exactly one row with `AuthFailed: true` and `Backend ""` — a server refusing the configured api_key, which would otherwise be invisible and so unstoppable ([ADR-0001](docs/adr/0001-stop-is-unconditional.md)). Any backend finding a healthy or Starting server at the address suppresses the row. It sorts first (`Backend ""`); `primaryInstance` (menu) skips it unless nothing else runs, a bare `logs` omits it (`logCandidates`), and `load` refuses against it by the same rule (`authRefusalAt`, §6.1). For every healthy address it then asks the backend:

- `ModelLister.ListRunningModels` → the currently loaded Model (`/v1/models` for llamacpp / LM Studio / Splash, `/api/ps` for Ollama).

The result is matched back to the config by `matchProfileName` — among Profiles whose backend and address equal the discovered instance, an exact resolved-model-path match wins; failing that, a basename match (`modelNamesMatch`). The fallback exists because a server reports the model as whatever path or alias it was launched with: current llama.cpp builds report the absolute `--model` path they were started with, and a server started outside the launcher may name the same file by a different path or by a bare alias — so the reported name rarely equals the Profile's full resolved path. The same helper drives the `LoadProfile` idempotency check (ADR-0007). Ambiguity (several equally good matches) yields no match. When no Profile matches, the field is empty; the launcher still shows the running Model and address.

### 7.3 Legacy Cleanup

On first run after upgrade, `CleanupLegacyStateFiles` (called once at CLI startup, behind `sync.Once`) removes the state files earlier versions wrote to `~/.config/llama-launcher/` — through `cleanupLegacyStateFiles(dir)`, which judges each `state.json` and `state-*.json` candidate with `isLegacyStateFile`. The config directory is the user's, so a name match alone deletes nothing: a file goes only when its name matches `^state-(llamacpp|ollama|lmstudio)(-.+)?\.json$`, it is a regular file (never a symlink) owned by the current uid (unix only — `ownedByCurrentUser` in `legacy_state_*.go`; windows skips the check), at most 64 KiB, and it decodes as one JSON object whose `backend` equals the name's backend, whose `pid` is present as a number ≥ 0 (external connects stored `0`) and whose `port` is > 0. `state.json` passes the same checks with `backend` any of the three. Anything else stays. The cleanup is silent and best-effort — these files are no longer read or written, so failure to remove them has no functional impact.

## 8. Server Argument Assembly

The launcher builds the `llama-server` command line from the merged default parameters. The Model is baked into the start arguments via `--model`, so each Profile activation produces a fresh server with that Profile's hardware parameters honoured ([ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md)). Flag names are verified against llama-server b10068.

| Config Field | Flag |
|---|---|
| `model` | `--model` |
| `gpu_layers` | `-ngl` |
| `threads` | `-t` |
| `threads_batch` | `-tb` |
| `batch_size` | `-b` |
| `context_size` | `-c` |
| `host` | `--host` |
| `port` | `--port` |
| `flash_attn` | `-fa on` / `-fa off` |
| `mlock` (true) | `--mlock` |
| `no_mmap` (true) | `--no-mmap` |
| `cont_batching` (true) | `-cb` |
| `parallel` | `-np` |
| `embedding` (true) | `--embedding` |
| `jinja` (true) | `--jinja` |
| `temperature` | `--temp` |
| `repeat_penalty` | `--repeat-penalty` |
| `top_k` | `--top-k` |
| `top_p` | `--top-p` |
| `min_p` | `--min-p` |

Boolean flags are only appended when the resolved value is `true` — except `flash_attn`, which is emitted whenever set: `-fa on` for `true`, `-fa off` for `false` (llama-server's default is `auto`). Numeric flags are only appended when explicitly set (not nil after merge). `models_dir` is never passed to llama-server — it is launcher-side only, joining relative Model paths during resolution (§4.4; llama-server's own `--models-dir` is an unrelated router-server option). A configured per-server `api_key` is never emitted on argv — it reaches llama-server through the environment instead (`LLAMA_API_KEY`, §4.2). The sampling flags set llama-server's request defaults — parameters sent with an API request still override them per call — and are emitted before `extra_args`, so an `extra_args` override wins (llama-server honours the last occurrence of a repeated flag).

Argument assembly is delegated to the backend via `ManagedLLMServer.BuildServerArgs()`, allowing each `ManagedLLMServer` to map config fields to its own CLI flags.

### 8.1 Splash

Splash is the second `ManagedLLMServer`. Its command line is `splash serve --model <owner/repo> [--host H] [--port P] [--max-context N] [--allowed-host NAME...] <extra_args...>`:

| Config Field | Flag / channel |
|---|---|
| `model` | `--model` (the Hugging Face `owner/repo` id, checked as installed at resolution — §4.4) |
| `host` | `--host`; a `host` that is not loopback also adds `--allowed-host` for the machine hostname and its short form (below) |
| `port` | `--port` |
| `context_size` | `--max-context` (a plain token count; Splash accepts 1–262144) |
| `api_key` (from `servers:`) | `SPLASH_API_KEY` in the server's environment, never argv |

Nothing else is mapped. The sampling parameters (`temperature`, `repeat_penalty`, `top_k`, `top_p`, `min_p`) and the llama.cpp hardware parameters are ignored for Splash, and its `ParamSpecs` therefore show only `context_size` (§5.3). Every other Splash flag — reasoning effort, KV format, max memory, an `--allowed-host` for another name — goes through the Profile's `extra_args`, appended last so a user flag extends or overrides the launcher's own.

Splash answers 403 to a request whose `Host` header names a host outside its allow-list. That check accepts the socket's local IP, so LAN or VM access by IP needs no `--allowed-host`. For access by name, the launcher adds `--allowed-host` itself whenever the configured `host` is not loopback (a wildcard such as `0.0.0.0`, or a LAN address): once for the machine hostname from `os.Hostname()` with any trailing dot dropped, and once for its first label when the name has a dot (`Apollo-II.local` adds `Apollo-II.local` and `Apollo-II`). A loopback `host` (`localhost` or a loopback IP) or an unset one — Splash's loopback default — adds no names, and neither does a failed lookup or a hostname of `localhost`. `extra_args` `--allowed-host` is needed only for other names, such as a reverse proxy or a DNS alias; Splash appends every occurrence to its allow-list.

The same check refuses a `Host` naming a wildcard bind address, so the launcher's own Splash probes (`HealthCheck`, `StartingUp`, `ListRunningModels`) dial loopback for a wildcard or empty host (`probeAddr`, §5.3). The instance keeps its configured address as its identity ([ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)); only the dial target changes.

## 9. Log Management

Server stdout and stderr are redirected to a log file at:

```
<log_dir>/<backend>-<YYYYMMDD>-<HHMMSS>.<mmm>.log
```

Example: `~/.config/llama-launcher/logs/llamacpp-20260519-171200.042.log`

The stamp carries milliseconds and `createLogPath` creates the file exclusively (`O_CREATE|O_EXCL`), retrying with a fresh stamp if the name is already taken — so two starts of the same backend never share a log, and a second start can never truncate a live server's log. Logs written before the millisecond stamp (`<backend>-<YYYYMMDD>-<HHMMSS>.log`) are still read, aged and cleaned.

The `logs` subcommand tails the log file of a launcher-managed running instance. The path is reconstructed deterministically by globbing `{log_dir}/{backend}-*.log` and picking the most recent — log filenames embed the start timestamp so lexicographic order is chronological. Externally-started servers log to wherever they were started; `llml logs` prints a clear message in that case rather than guessing. With `--follow`, the launcher uses `tail -f` and is the only mode where it remains running.

### 9.1 Log Cleanup

Old log files can be cleaned up manually or automatically:

- **Manual:** `logs clean` deletes files older than 7 days (default). `--days N` overrides the threshold (N ≥ 1; `--days 0` is refused rather than read as "delete everything"); `--all` removes everything. Reports files removed and space freed.
- **Automatic:** Setting `log_retention: N` (positive) in config causes `createLogPath` to silently delete files older than N days before each new log is created. No output during automatic cleanup. `0` — like leaving `log_retention` unset — disables it; a zero retention never means "delete everything".

Both paths use `cleanupLogs()`, which determines file age from the filename timestamp (not mtime) and always skips log files belonging to running servers (checked via `DiscoverRunningInstances` + `fillRuntimeDetails` so the live log path of each instance is known and protected).

## 10. Error Handling

| Scenario | Behaviour |
|---|---|
| Config file missing (first run) | Generate example config, print path, reload it, and continue (no exit). |
| Config file parse error | Print error with line number (from yaml.v3), exit 2. |
| Profile missing `server:` with no defensible fallback | Print warning (deprecation notice) or error (if no fallback is defensible). See [§4.6](#46-llm-server-selection). |
| Unknown Profile name | Print error, exit 2. |
| Model file not found | Print resolved path, exit 2. |
| Server binary not found | Print configured path, exit 3. |
| Server already running, same Profile, no drift | No-op. Exit 0. |
| Server already running, same Profile name, parameters drifted | Print drift notice to stderr; no-op unless `--restart`. Exit 0. |
| No server running (on `stop`/`unload`) | Print message, exit 1. |
| Failed to start process | Print OS error, exit 3. |
| Server health timeout | The spawned server is left running — it may still be loading a large Model. Print timeout message naming its PID and log path plus recovery guidance (watch the log and retry once healthy, or `llama-launcher stop <backend>` — [ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md) replaced the old `kill <PID>` instruction), exit 3. An auto-started Ollama or LM Studio that misses its 15 s start wait is left running too: the message names its PID and log path when the backend tracks them and advises a retry once healthy (§6.2), exit 3. |
| Managed server exits during the `load` health wait | The load returns within a health-poll interval, exit 3. A non-zero exit code prints "server exited while loading its model (exit status N)" with the redacted log tail; any other exit — the server was stopped (a `stop` from another terminal, or Splash exiting 0 on SIGTERM) — reports the load canceled (`ErrLoadCanceled`). |
| Managed start while a server is still starting up at the target address (health 503) | A plain `load`/`start` refuses to fork a duplicate (it would die on the bind); print the loading server's PID and log path plus `llama-launcher stop` / `--restart` guidance, exit 3. Applies to plain retries after a health timeout. `load --restart` instead stops the Starting occupant and replaces it (ADR-0010). |
| Managed start while a foreign process holds the target port | Refuse before forking (§6.2 step 2): print the port, the address the backend cannot bind, and every listening PID with its executable name, plus guidance to stop the occupant or configure a different `port`, exit 3. Covers an occupant on any interface of that port, including one shadowing loopback — which also makes discovery report nothing running there, so the refusal is often the first place the conflict is named. If `lsof` cannot answer, the check reports nothing and the start proceeds to the fork, where a bind failure still surfaces as the "server exited immediately after start" log tail. |
| Model load/unload API error | Print server response, exit 3. |
| SIGTERM timeout (on `stop`) | Escalate to SIGKILL, warn, exit 0 (server is stopped). |
| `lsof` not on PATH (stop path) | Print message that the listening PID could not be determined, exit 3. |
| Port already in use | Detected via early server exit — the forked child is reaped by a `cmd.Wait()` goroutine, and if it exits within ~500 ms of start the launcher reports the log tail instead of a running instance. |

## 11. Future Considerations

These are explicitly out of scope for v1 but noted as natural extensions:

- **Per-instance log file naming**: `{backend}-{port}-{timestamp}.log` rather than `{backend}-{timestamp}.log`. Collisions are already ruled out — the stamp carries milliseconds and the file is created exclusively with a retry (§9) — so this would only make a log's instance readable from its name.
- **Shell completions**: Generate bash/zsh/fish completions for subcommands and Profile names.
- **Config reload subcommand**: A `reload` subcommand that restarts the matching instance with the same Profile using updated config values. (Note: automatic config reload in the interactive menu is already implemented — this item covers the CLI subcommand.)
- **Additional LLM Servers**: vLLM and others — each as a new `backend_<name>.go` file implementing the `LLMServer` interface.
- **Launchd integration**: Generate a launchd plist for auto-start on login.
- **Per-Profile log retention**: Today log retention is global; a per-Profile `log_retention` would let chatty debug Profiles keep more history without inflating storage for everything.

## 12. Testing

Tests come in two layers (historical plan: `backend-tests-plan.md`; the validated Layer-2 spec is `docs/plans/2026-07-19-starting-stop-and-integration-tests.md`):

- **Layer 1 — unit tests** (§12.1–12.4): fake-driven and `httptest`-based, no external processes. Run with `make test` (= `go test ./...`) on every change. Since v1.6.1 they pass **natively on Linux** as well as on macOS — the claim ADR-0012 makes checkable, and the reason a client's Linux CI can run them.
- **Layer 2 — integration tests** (§12.5): files carrying the `integration` build tag that start and stop **real** backend servers. Invisible to the untagged build; run manually **on the host** with `make test-integration` — by convention the *user* runs this layer and reports back, because agents working on this repository operate in a container and must not start real servers (they verify only that the tagged files compile, which `make cross` does for all three platforms).

Riding alongside them is the **cross-compile gate** (ADR-0012): `make cross` runs `go build ./...`, `go vet ./...` and `go vet -tags=integration ./internal/launcher/` for `GOOS=darwin`, `GOOS=linux` and `GOOS=windows`, so a platform regression fails in this repository instead of in an importing client's CI. `vet` type-checks test files, which is what keeps both layers free of unix-only calls. **`make check` (= `make test` + `make cross`) is the aggregate to run before every commit**: it starts no process, so it is safe for agents and CI alike. `make test-all` (Layer 1 + Layer 2) remains the owner's host-only target.

CI (`.github/workflows/ci.yml`) runs `make check` on GitHub Actions for every push to `main`, every pull request and on manual dispatch, on `macos-latest` and `ubuntu-latest` — the shipped platform plus the Linux half of ADR-0012's Layer-1 claim. The Go version comes from `go.mod`. Layer 2 never runs there.

### 12.1 Unit Tests (httptest)

Backend methods are tested using `net/http/httptest` mock servers. These tests run as part of `go test ./...` with no external dependencies.

| Test | What it covers |
|---|---|
| `TestLlamaCppHealthCheck` | 200 on `/health` with `{"status":"ok"}` body → success; non-llamacpp body (missing `status` field) → rejects; Splash-shaped `{"status":"ok"}` with `Server: Splash` → rejects; non-200 → error; unreachable → error. |
| `TestOllamaHealthCheck` | 200 with "Ollama" body → success; empty body → error; non-Ollama body → error; non-200 → error. |
| `TestLMStudioHealthCheck` | 200 on `/v1/models` → success when `/health` body lacks `status` field; healthy when LM Studio returns `{"error":"..."}` for `/health` and `/api/tags`; detects llamacpp via `/health` body containing `{"status":"ok"}`; detects Ollama via `/api/tags` body containing `{"models":[...]}`; non-200 → error; unreachable → error. |
| `TestLMStudioLoadModel` | Success, context_length inclusion, param mapping (`batch_size`→`eval_batch_size`, `flash_attn`→`flash_attention`, `parallel`; unsupported params like `gpu_layers` never enter the payload), error with message, error without message. |
| `TestLMStudioUnloadModel` | Success, non-200 with error message, non-200 with empty body returns error. |
| `TestExtractLMStudioError` | Valid JSON, empty body, malformed JSON, missing message field. |
| `TestOllamaLoadModel` | Success (verifies keep_alive payload), error status. |
| `TestOllamaUnloadModel` | Success (verifies keep_alive=0), error status. |
| `TestOllamaListRunningModels` | Success with models, empty list, malformed JSON. |
| `TestLlamaCppListRunningModels` | `/v1/models` parsing — single-entry `data` array with `id` populated. |
| `TestLlamaCppStartingUp` | 503 on `/health` → starting; a healthy server → not starting; a healthy or loading Splash server (`Server: Splash`) → not starting. |
| `TestSplashHealthCheck` | 200 on `/ready` with `Server: Splash` → success; 200 without the header (a foreign server) → rejects; 503 while loading → error; a llama-server-shaped server with only `/health` → error; unreachable → error. |
| `TestSplashStartingUp` | 503 on `/ready` with `Server: Splash` → starting; a foreign 503, a 200, or an unreachable address → not starting. |
| `TestSplashWildcardProbe` | The wildcard case of `HealthCheck`, `StartingUp` and `ListRunningModels`: against a stand-in that 403s a `Host` naming the wildcard, as Splash does, a `0.0.0.0` or empty host is healthy, starting and lists its Model, because the probes dial loopback. A guard subtest proves the stand-in rejects a wildcard `Host`. |
| `TestSplashLoadingPID` / `TestParseProcessTable` | The process-table match for a loading Splash ([ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)). Every command-line form a real launch passes through matches: wrapper script, checkout script, `launcher.py serve`, `server.py … --binary …/splash`. These do not match: a process that does not lead its group, the `serve-native` child, another host or port, another server's `serve`, and a launch without `--model`. The last `--port` wins, the `--flag=value` form works, and absent flags fall back to `127.0.0.1:8000`. `ps` output parses into PID, PGID and argv, with malformed rows skipped. |
| `TestSplashBuildServerArgs` / `TestSplashBuildServerEnv` | `serve --model` plus `--host`/`--port`/`--max-context` only when set, `extra_args` last; with a pinned hostname, a non-loopback `host` (`0.0.0.0`, `::`, a LAN IP) adds `--allowed-host` for the hostname and its first label (one name for a dotless hostname, trailing dot dropped), while a loopback IP, `localhost`, a failed lookup, an empty hostname or a `localhost` hostname add none; the `api_key` only as `SPLASH_API_KEY`, never on argv. |
| `TestSplashResolveModel` / `TestSplashResolveModelInstallCheck` | Repo-id validation (traversal, `--`, `..`, `.git`, length); the Hugging Face hub precedence with empty variables counting as unset; installed only with both a `refs/splash/*/<rev>` pin and `snapshots/<rev>/manifest.json`; a not-installed Model's error names `splash serve --model <id>`; an empty ref resolves without touching the cache. |
| `TestSplashParamSpecs` / `TestSplashServerBinary` / `TestSplashRegistered` | Only `context_size` is displayed; the binary is `splash`; the backend registers as `splash` with default address `127.0.0.1:8000`. |
| `TestLlamaCppQueryLiveParams` | `/props` parsing populates `ContextSize` (per-slot n_ctx × total_slots) and `Parallel`, leaves sampling fields nil; `404` returns `(nil, nil)` so liveParamDrift treats it as "no drift". |

### 12.2 Server & Config Tests

| Test | What it covers |
|---|---|
| `TestIsProcessAlive` | Current PID → true; PID 0 → false; negative PID → false; invalid PID → false. |
| `TestReadLastLines` | More lines than requested; fewer lines; nonexistent file. |
| `TestRunningInstance_Addr` / `_Uptime` / `_Uptime_ZeroStart` | Instance helper methods including the zero-StartedAt fallback. |
| `TestDiscoverRunningInstances_*` | Discovery returns the empty set when nothing listens; an httptest llama-server stand-in is found with `ActiveModel` populated from `/v1/models`, and `/props` is not probed (live params are queried on demand by drift detection, not during discovery). `TestDiscoverRunningInstances_WildcardSplash` finds a Splash configured on `0.0.0.0` that 403s a wildcard `Host`: probed over loopback, keyed by the configured address (ADR-0006). `TestDiscoverRunningInstances_AuthFailed` pins the one `AuthFailed` row per address answering every backend with 401/403 and its suppression by a healthy or Starting backend there; `TestDiscoverAuthRefusalAt` pins the same rule as `load`'s refusal. |
| `TestFindManagedLogFile` | Most-recent file picked by lexicographic timestamp, across millisecond and legacy second-precision names; filters by backend prefix; returns empty when no matching file exists. |
| `TestCleanupLegacyStateFiles` / `TestIsLegacyStateFile` | `cleanupLegacyStateFiles(t.TempDir())` removes launcher-written `state-llamacpp.json`, `state-ollama-11434.json`, a pid-0 `state-lmstudio-1234.json` and a legacy `state.json`; keeps a non-state `state.json`, `state-notes.json`, `state-ollama-backup-2024.json` with non-state JSON, a legacy name with invalid JSON, a backend mismatch, and absent, negative or quoted `pid` / zero `port`. The predicate refuses oversized files, symlinks and directories (§7.3). |
| `TestParamDrift` | Identical params, nil-vs-nil, set-vs-unset, bool/float comparisons, slot-identity fields skipped. |
| `TestShouldCrossServerUnload` | Decides whether to issue an unload on a discovered instance during cross-server `auto_unload`. |
| `TestStartServer_ManagedChildExitsImmediately` | A managed child that exits within the startup grace period yields the "exited immediately" start-crash error (with log tail), not a `RunningInstance` — the reaped child cannot linger as a zombie that `kill(pid, 0)` reads as alive. |
| `TestLoadProfile_StartupTimeoutIsErrStartupTimeout` | The **real** activation wait loop against an httptest stand-in that answers 503 forever: the error wraps `ErrStartupTimeout` (so a client recognises the outcome without matching on text), still names the PID, the log path and the "left running" guidance verbatim, and the timed-out server is not stopped. The health wait is the only real operation: `realWaitOps` overrides just `waitHealthy` (production `WaitForHealth`, with the wait window shortened — the production 30 s would stall the suite) and embeds `fakeOps` for the rest, so start, stop, discovery and the probes stay in memory; the PID and log path the message names are the fake instance's, and nothing is forked or signalled. |
| `TestLoadProfile_ServerExitMidWaitEndsTheLoad` / `TestLoadCanceled_ExitClassification` | The real activation wait loop against a never-healthy stand-in while `exitingStartOps` forks an `sh -c` child that exits after one poll: a signal, exit 0 and exit 143 wrap `ErrLoadCanceled`, exit 1 is a crash error naming the exit status and the redacted log tail, all well under the wait window and none wrapping `ErrStartupTimeout`; `serverExitErr` also cancels on a nil (status 0) and an unreadable `Wait` result. |
| `TestStartupTimeoutErr_ManagedMessageUnchanged` | The managed arm's timeout message, byte for byte: the external arm gained its own decoration and the managed text must not drift with it. |
| `TestConnectExternal_TimeoutIsErrStartupTimeout` / `TestConnectExternal_UntrackedTimeoutOmitsPIDAndLog` / `TestConnectExternal_TryStartErrorSurfaces` | `connectExternalServer` against fake external backends whose health never passes, with a shortened wait: an Ollama-shaped `PIDTracker` backend's timeout wraps `ErrStartupTimeout`, names the PID and log path and suggests no `logs`/`stop`; an LM Studio-shaped backend (no `PIDTracker`) times out with neither a PID nor a `Log:` line; a `TryStart` error — "not found in PATH", or one wrapping `ErrUnsupported` — reaches the caller wrapped, without the generic "start it manually" advice and without reading as a timeout. |
| `TestRun_StatusJSONNothingRunning` / `TestRun_ExitCodes` | The real `Run` dispatcher's exit-code contract (§3.3): usage errors exit 2, nothing-running `stop`/`unload` exit 1, and `status --json` exits 1 while still emitting the JSON array — the mapping the MCP adapter's result handling keys off. |
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
| `TestFormatProfileParams_LMStudio` | Omits GPU offload (not part of LM Studio's load request); shows batch size, flash attention, and parallel (the params the load request sends); omits llamacpp-only params. |
| `TestFormatContextSize` | The compact number format of the context column ([§3.1](#31-interactive-mode)): below a thousand verbatim (`0`, `512`), thousands by integer floor division (`4K`, `16K`, `32K`, `65K`, `98K`, `131K`), millions as `1M`. |
| `TestBuildProfileItems_ContextColumn` | Two enabled servers: the llamacpp cells render `131K` / ` 65K` right-aligned, the Ollama row is a blank cell of the same width (its `ParamSpecs` omit the parameter), both `[` and `]` keep one column across every row, and `★` stays the rightmost aligned marker. |
| `TestBuildProfileItems_ContextFromDefaults` | The effective value is shown: a Profile with no `context_size` of its own inherits `defaults.context_size`, an explicit Profile value still wins. |
| `TestBuildProfileItems_NoContextColumn` | The presence gate — descriptions keep their byte-identical pre-column shape when no Profile qualifies: mixed servers with no context size at all, a context size only an Ollama Profile carries (which must not open the column), and a single server with none (empty descriptions). |
| `TestBuildProfileItems_SingleServerContextOnly` | With one enabled server there is no `[server]` tag, so the context cell is the entire description (`131K` / `  4K`). |
| `TestBuildSimpleProfileLines_ContextColumn` | The same contract on the non-terminal numbered fallback: merged values, right-alignment, blank Ollama cell, aligned tag column, and `★` suffix placement. |
| `TestCmdList_ContextColumnAlignment` | The third surface (`cli_test.go`): the text `list` rows are asserted verbatim, so the cell sits between the name and the tag, the Ollama cell is blank, and a multibyte Profile name keeps every following column in place (rows are measured by visible width, not bytes). |

### 12.4 Test Helpers

`helpers_test.go` provides `addrFromURL(t, rawURL) string`, which parses an `httptest.NewServer` URL and returns the `host:port` portion for passing to backend methods that expect an `addr` string.

### 12.5 Integration Tests (Layer 2)

The files `integration_test.go` (shared helpers), `integration_llamacpp_test.go`, `integration_ollama_test.go`, `integration_lmstudio_test.go`, and `integration_splash_test.go` carry `//go:build integration`, so `go test ./...` never compiles them. `make test-integration` runs them (`go test -tags=integration -count=1 -timeout 5m -v ./internal/launcher/` — `-count=1` bypasses Go's test cache, so an unchanged tree still exercises the real servers instead of replaying a cached pass); `make test-all` runs both layers.

| Test | What it covers |
|---|---|
| `TestLlamaCppLifecycle` | Real `llama-server` through the in-package start path (`StartServer` with a constructed `*Config` + `ResolvedProfile`), so Setsid, the reaper goroutine, and the startup-grace crash detection are exercised for real — then `waitForHealthy`, unified `Stop(addr)`, and `waitForUnhealthy`. |
| `TestLlamaCppStopWhileStarting` | The ADR-0010 flagship: start a real model load, poll until `StartingUp(addr)` reports the 503 window, call `Stop(addr)` mid-load, assert success, and assert port release via an explicit `net.Listen` re-bind probe. Skips when the model turns healthy before a 503 is ever observed (use a larger model). |
| `TestOllamaLifecycle` | `TryStart` → `HealthCheck` → optional model steps → unified `Stop(addr)` → gone. `TryStart` exports `OLLAMA_HOST=<addr>`, so the suite serves on a free loopback port and never touches a real instance at 11434. The stop goes through `Stop(addr)` because Ollama's `TryStop` is deliberately a no-op (§6.5). |
| `TestSplashLifecycle` | Real `splash` through the Profile path (`Config.ResolveProfile`, so the Hugging Face cache install check runs for real) and the managed `StartServer` path: wait for `/ready`, list the running Model, confirm llamacpp's health check rejects the live Splash server, then unified `Stop(addr)` and verify the port and process group are gone. The load must be observed as Starting on the way, through the process table ([ADR-0015](docs/adr/0015-a-loading-splash-is-found-by-its-process.md)). Liveness checks go through the process seam so the file still vets under `GOOS=windows`. |
| `TestSplashStopWhileLoading` | Real `splash` stopped mid-load, before it binds its port: discovery reports it Starting, a second `StartServer` is refused as still starting up, and `Stop(addr)` signals the started PID and takes its process group down. Skips if the Model loads before the first probe. |
| `TestSplashWildcardHost` | Real `splash` bound to the IPv4 wildcard `0.0.0.0` and driven through its configured address: it turns healthy, discovery reports it Ready with the served Model, and `Stop(addr)` frees the port and ends the process group. Real Splash refuses a `Host` naming the wildcard, so each step passes only when the probes dial loopback while the instance stays keyed by `0.0.0.0:<port>` (ADR-0006). |
| `TestLMStudioLifecycle` | Same shape via `lms`. LM Studio runs exactly one app-owned API server (`TryStart` forwards only `--port`), so the suite starts, moves, and stops *that* instance — running it can interfere with an interactive LM Studio session, which is accepted for a manually-invoked host-side suite. |

Suite conventions (pinned in `integration_test.go`'s package comment): every test skips when its backend binary is not on `PATH` (`mustFindBinary`); no `t.Parallel()` anywhere — real servers contend for ports, GPU memory, and the per-backend singletons; log directories are per-test `t.TempDir()` paths; subtests chain, so a failed step aborts the rest; `waitForUnhealthy` applies the ADR-0010 stop-verification rule (gone ⇔ neither healthy nor still `StartingUp`).

Model selection is via environment variables — the suite skips or trims itself when they are unset:

| Variable | Meaning |
|---|---|
| `INTEGRATION_MODEL_LLAMACPP` | Absolute path to a `.gguf` model file. Required by both llamacpp tests (they skip without it); pick a model that loads slowly enough to open a Starting window for the stop-while-Starting test. |
| `INTEGRATION_MODEL_OLLAMA` | Name of an already-pulled Ollama model (e.g. `qwen3:0.6b`). Gates the load/list/unload steps — `LoadModel` does not pull. |
| `INTEGRATION_MODEL_LMSTUDIO` | Name of a model already downloaded in LM Studio. Gates the load/list/unload steps — `LoadModel` does not download. |
| `INTEGRATION_MODEL_SPLASH` | Hugging Face `owner/repo` of a Model already installed for Splash (run `splash serve --model <id>` once first). Required by `TestSplashLifecycle`, `TestSplashStopWhileLoading` and `TestSplashWildcardHost`, which skip without it — the launcher never downloads. |

## 13. Build and Installation

The version number lives in the root `VERSION` file and is injected at build time via `ldflags` into `launcher.Version`.

```bash
make build             # builds ./llama-launcher binary (version injected from VERSION file)
make build-mcp         # builds ./llama-launcher-mcp, the optional control-plane adapter (§15)
make test              # unit tests: go test ./... (Layer 1, §12.1–12.4)
make cross             # cross-compile gate: build + vet for GOOS darwin/linux/windows (§12, ADR-0012)
make check             # test + cross — the pre-commit aggregate; starts no process
make test-integration  # real-backend suite, host only (Layer 2, §12.5)
make test-all          # both test layers, host only
make clean             # removes binaries

go test ./internal/launcher/ -run TestMergeParams  # run a single test
go vet ./...        # static analysis
```

Installation is deliberately Homebrew-only: `brew install airiclenz/tap/llama-launcher` for a first install, `brew upgrade llama-launcher` afterwards. `make install` does not copy anything to `~/.local/bin` or anywhere else — it prints that pointer and exits non-zero; use `make build` for local testing.

The binary is statically linked (default for Go on macOS with CGO_ENABLED=0) and has no external dependencies at runtime.

### Homebrew

The published formula (`airiclenz/tap/llama-launcher`) builds from the release source tarball and installs **both** binaries: the `llama-launcher` CLI and the optional `llama-launcher-mcp` control-plane adapter (§15). The two builds inject the version with different `ldflags` targets — `internal/launcher.Version` for the CLI and `main.Version` for the adapter — because the adapter lives in `package main` under `cmd/llama-launcher-mcp/`. Packaging-only changes that don't bump `VERSION` (e.g. starting to ship the adapter from an already-tagged release) are released as a formula `revision` bump rather than a new tag.

## 14. Coding Standards

Follow the `coding-standards` skill when writing or modifying code. It is a personal Claude skill kept outside this repository at `~/.claude/skills/coding-standards/SKILL.md`; read its base references and the Go-specific extensions before making changes.

### After Changing Code

1. Update the documents `llama-launcher.TDD.md`, `README.md`, and `CHANGELOG.md` if the change affects behavior, configuration schema, subcommands, error handling, or any other aspect covered here. Close, update or file the matching beads (`bd`, see `AGENTS.md`) — beads is the issue register; `TODO.md` was retired into it on 2026-09-24.
2. If the change touches one of the architectural decisions in [docs/adr/](docs/adr/), update or supersede the relevant ADR in the same change.
3. Run `make check` — unit tests plus the ADR-0012 cross-compile gate ([§12](#12-testing)) — before committing.
4. Run `make build` and exercise the freshly built `./llama-launcher` locally; installed copies come from Homebrew once a release is tagged (`brew upgrade llama-launcher`, §13).

## 15. Optional MCP Control-Plane Adapter

`llama-launcher` itself never opens a socket — that property is load-bearing for [ADR-0002](docs/adr/0002-not-a-router.md). Remote control (e.g. a coding agent in a container deciding which Model the host runs) is provided by a **separate, optional binary**, `llama-launcher-mcp`, living under `cmd/llama-launcher-mcp/`. The rationale and trust model are pinned in [ADR-0008](docs/adr/0008-mcp-control-plane-adapter.md); this section describes the implementation.

The adapter is the *remote* access path; the in-process library facade ([§16](#16-public-library-facade)) is the *same-machine* one — they compose rather than compete, and the adapter does not import the facade (it shells out to the CLI).

### 15.1 Shape

The adapter is a thin shim: it runs on the host, exposes an MCP server over Streamable HTTP (via `github.com/modelcontextprotocol/go-sdk`), and implements every tool by **shelling out to the installed `llama-launcher` CLI** and returning its output. It holds no Models and parses no inference requests — it forwards the same control commands a human or the `manage-llm-server` skill drives. The new dependency is scoped to this binary; the core CLI build does not import it.

It ships with the CLI: `make build-mcp` builds it locally, and the Homebrew formula installs it alongside `llama-launcher` (§13). It is inert until started — installing it adds no resident process.

The HTTP listener sets connection timeouts so a stuck or hostile client cannot hold it open indefinitely: `ReadTimeout` 30 s, `ReadHeaderTimeout` 10 s, `IdleTimeout` 2 min, and `WriteTimeout` 10 min — the write window is generous because it must outlast the slowest tool call (`load_profile` waits up to 5 minutes for a model load, plus health-check and stop grace periods). Request bodies are capped at 1 MiB via `http.MaxBytesReader` before they reach the MCP handler, which buffers the whole body in memory — control-plane calls are small JSON-RPC payloads, so an allowlisted but hostile client cannot exhaust the adapter's memory with one huge POST.

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

`start_server` without a profile starts the default backend with no Model loaded; the managed backends (llamacpp, splash) need a Profile and fail otherwise, which the tool description says. The mutating tools are registered only when `--read-only` is not set. Judgment that needs context (e.g. "never swap mid-simulation") stays with the agent via the skill; the adapter exposes the tools plainly.

**Input validation.** Every free-form string a tool forwards to the CLI — as a positional argument or a flag value — is vetted in the adapter (`cmd/llama-launcher-mcp/validate.go`) before shelling out; a rejected value comes back as a tool error without the CLI ever being invoked, so the CLI's own argument grammar is deliberately not relied on as a security boundary. `tail_log`'s and `stop_server`'s `target` share `validateTarget` (`stop` and `logs` share one target grammar), which applies a positive allowlist: empty (the CLI auto-selects the single discovered instance), a known backend name (`llamacpp`, `lmstudio`, `ollama`, `splash` — pinned in the adapter's `knownBackends`, which does not import `internal/launcher`; an unknown name's error lists all four), or a `host:port` whose port parses as 1–65535. Anything else — flags such as `-f`/`--days`, the `clean` subcommand, shell metacharacters — is rejected. It matters most for `tail_log`, a read tool that stays exposed under `--read-only` and must not be able to reach a mutating (`logs clean`) or blocking (`logs -f`) CLI path. `unload_model`'s `profile`, `load_profile`'s `name`, and `start_server`'s `profile` (forwarded as the value of `--profile`, where a leading dash could be read as another flag) go through `validateProfile`: profile names are user-defined in a config the adapter deliberately does not parse, so no pinned name list exists — the same character allowlist plus the no-leading-dash rule rejects flag-shaped arguments, extra words, and shell metacharacters, and resolving the name is left to the CLI, which fails cleanly on an unknown profile.

**Result mapping.** Keyed off the CLI's exit code ([§3.3](#33-exit-codes)), not stdout emptiness — mutating subcommands print progress to stdout before they can fail. Exit 0: stdout is returned verbatim as the first text content item, so a JSON payload parses from `Content[0]` on its own; non-empty stderr (warnings such as the plaintext-key notice) follows as a separate second item, never fused into stdout. Empty stdout leaves the stderr item alone; both empty yields the single item `(no output)`. Exit 1 (informational negative — e.g. `status --json` exits 1 when nothing is running but still emits the JSON array): returned as normal content, mapped the same way, so the caller keeps the data. Exit ≥ 2, a signal, or a failure to run the CLI at all: flagged as a tool error carrying stderr, with stdout appended for context. Each captured stream (stdout and stderr) is capped at 1 MiB: content past the cap is dropped and a `[output truncated: 1MiB cap reached]` notice is appended, so a runaway subprocess cannot grow the adapter's memory or the MCP response without bound.

**In-flight cap.** At most 4 `llama-launcher` subprocesses run at once across all tool calls (`maxInFlight` in `cmd/llama-launcher-mcp/config.go`, no flag), so an allowlisted client firing calls in a loop cannot fork the host without bound. `newServer` sizes a buffered-channel semaphore on the adapter's `config`, and `config.run` — the one exec site — holds a slot for the life of the subprocess; a call that finds all four taken waits, and if its request context ends first it returns the tool error `canceled while waiting for a free slot` without the CLI ever being invoked. `stop_server` is the one exception: it runs outside the cap (`runUnbounded`), because an explicit stop is what cancels an in-flight load ([ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md)) and must never queue behind the `load_profile` calls holding the slots.

### 15.3 Access control

The driving constraint is that the remote client may be a cloud LLM agent that must not be handed credentials. The adapter therefore uses a **source-IP allowlist**, not a token:

- `--listen host:port` — bind the container-facing bridge interface, **not** `0.0.0.0`.
- `--allow ip|cidr|host` — repeatable; a request whose source IP is not matched gets `403`. A hostname is resolved to its addresses **once at startup** (each becomes an exact-IP matcher, so the request-time check stays a numeric comparison); restart the adapter if the container's IP changes, or allow its subnet as a CIDR. Note a hostname may resolve to a *public* address (e.g. `devbox.dev` is a real domain, not the local container) — prefer a private CIDR or `--allow-interface`.
- `--allow-interface name` — repeatable; allow the network of every address bound to a local interface (e.g. `bridge100`, the container-facing bridge). Each address's CIDR (`ip.Mask(mask)` + mask) becomes a subnet matcher, so any IP the bridge assigns the container is covered without the operator knowing or pinning it, and a private bridge subnet can never collide with a public hostname. The interface is read once at startup (`interfaceAddrs`, a package var so tests can stub it); an unknown interface is a fatal startup error.
- `--llama-launcher-bin path` / `--config path` — which CLI binary and config the adapter drives (config is forwarded as `--config` on every call).
- `--read-only` — register only the read tools.

`resolveAllowlist` combines the `--allow` specs and `--allow-interface` networks; the loopback default applies **only when neither is given**, so naming an interface (or any `--allow`) drops the implicit loopback. The IP check (`allowlistMiddleware`) is defense-in-depth on top of the narrow bind, not a substitute for it.

**Cross-origin refusal.** In addition to the IP allowlist, every request passes through `http.NewCrossOriginProtection()`'s handler (`crossOriginHandler`) before it reaches the MCP handler: a non-safe request that `Sec-Fetch-Site` or a host-mismatched `Origin` marks as cross-origin is refused with `403` and never reaches the MCP layer. The allowlist admits a whole *machine*, so without this a page loaded in any browser on an allowlisted machine could drive the control plane from an attacker's origin — the allowlist would see only the browser's (allowed) source IP. It is always on and there is no flag to disable it: non-browser MCP clients send neither header, so `Check` lets them through untouched and a flag would buy nothing.

## 16. Public Library Facade

Since v1.6.0 the launcher is importable: the `launcher/` package at the repository root (`github.com/airiclenz/llama-launcher/launcher`) is a curated facade over the unchanged `internal/launcher/` implementation — type aliases for the domain types plus thin wrapper functions for the verbs, nothing more. The decision, its alternatives, and the deliberate exclusions are pinned in [ADR-0011](docs/adr/0011-public-library-facade.md); this section describes what shipped. v1.6.1 completes it: the core now compiles on darwin, linux and windows so a client's CI can build it at all ([§16.6](#166-platform-contract), [ADR-0012](docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md)), and two sentinels join the surface.

All launcher code lives in `internal/launcher/`, which other modules cannot import by Go rule, so *some* export had to exist — the fork was its shape. A facade keeps the implementation home single and untouched while making the public surface opt-in per symbol: `main.go`, `cmd/llama-launcher-mcp/`, and every `internal/launcher/` behaviour are unchanged by it, and the facade adds no dependency to `go.mod`.

### 16.1 Exported surface

Twenty symbols, all in `launcher/launcher.go`:

| Symbol | Kind | Purpose |
|---|---|---|
| `Config`, `Profile`, `ProfileParams`, `ResolvedProfile` | type aliases | Configuration and Profile types. `Config`'s methods include `ResolveProfile` (merge with defaults + resolve the Model path), `ProfileNames`, `IsServerEnabled`. |
| `RunningInstance` | type alias | One discovered instance: `Addr()`, `Uptime()`, the ADR-0010 `Starting` flag, the `AuthFailed` flag (a server refusing the configured api_key: `Backend ""`, stoppable, refused by `LoadProfile`/`Unload`), `ActiveModel`. |
| `StopResult` | type alias | What a `Stop`/`Unload` did: instance acted on, server-stopped vs model-unloaded, steps taken. Non-nil even on error. |
| `ProgressFunc`, `NoticeFunc` | type aliases | The two callback sinks (§16.3). Nil discards. |
| `ErrConfigNotFound`, `ErrNotRunning` | sentinel errors | Re-exported by value, so `errors.Is` matches errors produced inside `internal/launcher/`. |
| `ErrStartupTimeout` | sentinel error | v1.6.1. The activation wait expired — for a managed server, or for an Ollama/LM Studio server `LoadProfile` auto-started (§6.2). Not a failed load: the server is deliberately left running (§6.2) and a slow model load may still finish, so a client keeps observing rather than treating the call as a failure. `LoadProfile` wraps it. |
| `ErrLoadCanceled` | sentinel error | The server a managed `LoadProfile` spawned ended mid-health-wait without crashing — the outcome of a `Stop` on the still-loading address (exit status 0, death by signal, 128+SIGTERM/SIGINT, or an unreadable status). `LoadProfile` returns it within a health-poll interval instead of waiting out the window; a non-zero exit code is a plain crash error carrying the redacted log tail instead (§16.2). `LoadProfile` wraps it. |
| `ErrUnsupported` | sentinel error | v1.6.1. The platform this program was built for cannot perform the operation — on windows, what needs unix process control ([§16.6](#166-platform-contract), [ADR-0012](docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md)). |
| `LoadConfig(path string, notice NoticeFunc) (*Config, error)` | verb | Read + validate the config; each non-fatal warning goes to `notice` as raw text. Wraps `ErrConfigNotFound` when the file is absent. |
| `DefaultConfigDir() string` / `DefaultConfigPath() string` | verb | `~/.config/llama-launcher` and the `config.yaml` inside it. |
| `DiscoverRunningInstances(cfg *Config) []*RunningInstance` | verb | Parallel probe of every address the config implies; unreachable addresses are omitted, not reported as errors, and an address answering every probing backend with 401/403 is one `AuthFailed` row (§7.2). |
| `LoadProfile(cfg, profile, restart, progress, notice) (*RunningInstance, bool, error)` | verb | Activation (§6.1), including the ADR-0007 idempotency check whose drift notice reaches `notice`. |
| `Stop(addr string) (*StopResult, error)` | verb | Unconditional stop of whatever is listening at `addr` (ADR-0001, §6.5). |
| `Unload(backend, addr string) (*StopResult, error)` | verb | Model unload; on a managed backend this reduces to stopping the server (ADR-0003/0004, §6.4). Acts only on the named backend: another backend's server at `addr` is refused with an error matching `ErrNotRunning` (§6.5). |

The wrappers contain **zero logic** — the ADR-0009 `realOps` discipline applied to the facade: if a wrapper wanted an `if`, the seam would be in the wrong place. Two of them (`LoadConfig`, `LoadProfile`) delegate to the notice-taking internal entry points of §16.3; the other five delegate to their internal namesake directly.

Deliberately **not** exported (ADR-0011): the `LLMServer` interface and `RegisterLLMServer` (the core's most change-sensitive seam), `TailLog` and log cleanup, memstats and the template engine, `GenerateExampleConfig`, and `QueryLiveParams` — the launcher's half of the actuate/observe split is actuation, and a client that watches server state already observes it itself. Widening later is a cheap minor bump; narrowing would be a v2.

### 16.2 The contract

Four decisions define what a client may rely on. `launcher/doc.go` states all four in the package documentation, so `go doc` carries the contract with the code.

1. **The documented surface is the contract.** A type alias unavoidably exposes every exported method of the aliased type — `Config`'s terminal-UI accessors, for one. The compatibility promise covers only the symbols §16.1 and the package documentation name; alias-reachable extras may change in any release. From v1.6.0 on, changing a documented symbol is a breaking change, adding one is a minor bump, and `internal/launcher/` remains free to refactor behind them. The one departure so far is deliberate and recorded: **v1.6.1 adds two symbols inside the 1.6.x line** (`ErrStartupTimeout`, `ErrUnsupported`) rather than as a minor, because portability and those two sentinels complete 1.6.0's own promise of importability instead of opening a new surface — the owner's call, reasoned in [ADR-0012](docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md).
2. **Notices are callbacks, never stream writes.** The library never writes to its host's stderr — the first client is an alt-screen TUI. Both notice-producing paths take a `NoticeFunc`: `LoadConfig` delivers one call per non-fatal config warning with the raw text and no prefix (the `warning: ` prefix belongs to the CLI printer), and `LoadProfile` delivers the ADR-0007 drift notice as a single call carrying the full formatted text — header, one indented line per drifted field, the `--restart` guidance. Progress steps use the separate `ProgressFunc`. Both sinks are safe to leave nil. `Config.Reload` stays CLI-flavoured (it delegates to the stderr-binding `LoadConfig`), so library clients re-read a changed config by calling the facade's `LoadConfig` again.
3. **Verbs block; cancellation is the domain's own verb.** No `context.Context` in v1. `LoadProfile`, `Stop`, and `Unload` run to completion on the calling goroutine: activation waits up to ~30 s for the new server to report healthy, and a restart first stops the occupant through the SIGTERM → SIGKILL → port-release escalation (up to ~20 s more), so clients call them from a goroutine. Cancelling an in-flight load is `Stop(addr)` on the Starting instance — [ADR-0010](docs/adr/0010-starting-instances-are-visible-and-stoppable.md) made that a first-class operation, and a Go-level ctx would duplicate it while forcing a rewrite of the activation internals. When the ~30 s wait expires, `LoadProfile` returns an error wrapping `ErrStartupTimeout`, and the wording matters: the server is deliberately left running (§6.2), so a later health success still completes the load. A client attaches "may still come up" handling to that sentinel and keeps observing the address with `DiscoverRunningInstances` instead of reporting a failure. The cancellation is noticed by the load itself: `startManagedServer` reaps its child through a `processExit` (`watchExit`) that the returned `RunningInstance` carries, and the managed arm's health wait (`waitForHealth`, the loop behind `WaitForHealth`) takes that exit as its liveness probe. When the spawned server exits mid-wait the load returns at the next poll gap: a non-crash exit — status 0 (Splash traps SIGTERM and exits cleanly), a signal death, 128+SIGTERM/SIGINT, or an unreadable status — wraps `ErrLoadCanceled`; a non-zero exit code is a crash error carrying the redacted log tail (`serverExitErr`). Neither wraps `ErrStartupTimeout`. An instance this process did not fork carries no exit, and the wait runs as before.
4. **One `Config` per process, stated honestly.** Backends live in a process-global registry and `LoadConfig` pushes the per-server API keys onto them (`applyAPIKeys`, §5.3), so the last load wins for the whole process; loading two configs with different API keys in one program is not supported. The read verbs (`LoadConfig`, the two path helpers, `DiscoverRunningInstances`, the `Config` accessors) are safe to call concurrently; the lifecycle verbs must be serialized per address by the caller, because concurrent lifecycle calls against the same `host:port` race each other. The one carve-out is cancellation (point 3): a `Stop` issued while a `LoadProfile` on the same address is still waiting for its server is the supported way to cancel that load, and the load returns `ErrLoadCanceled`.

### 16.3 The two internal seams

The facade needed exactly two notice seams inside `internal/launcher/` — config warnings and the ADR-0007 drift notice — plus the shared sink type they report through. All three edits are behaviour-preserving; everything else the facade re-exports untouched.

- **`NoticeFunc` / `reportNotice`** (`progress.go`) — the notice sink, deliberately the same shape as the existing `ProgressFunc` / `reportStep` pair, nil-safe in the same way.
- **`LoadConfigNotify(path, notify)`** (`config.go`) — today's `LoadConfig` body with the warning loop reporting to the sink. `LoadConfig(path)` keeps its signature and delegates, binding `fmt.Fprintf(os.Stderr, "warning: %s\n", w)`.
- **`LoadProfileNotify(cfg, profile, restart, progress, notify)`** (`server.go`) — the activation orchestration gained a trailing sink parameter, and `printDriftNotice` split into the pure text builder `driftNotice(profileName, addr, drifts)` plus delivery through `reportNotice`. `LoadProfile` keeps its signature and delegates, binding `fmt.Fprint(os.Stderr, s)`.

Both stderr bindings reproduce the pre-facade format strings exactly, so the CLI's and the menu's output is byte-identical to 1.5.0 — no CLI or menu call site changed.

### 16.4 Composition with the MCP adapter

The in-process facade and the MCP control-plane adapter (§15) are complementary access paths to the same core, not alternatives: importing the library means the client runs on the same machine as the servers it drives, while the adapter (ADR-0008) exists precisely for the client that does not — it runs on the host, shells out to the CLI, and does not import `internal/launcher` or the facade. A client can use both: the library for local lifecycle control, an MCP entry for a remote host's. Neither changes the rule that the launcher manages servers on the local machine and is not a router ([ADR-0002](docs/adr/0002-not-a-router.md)).

### 16.5 Tests

`launcher/launcher_test.go` is an **external** test package (`package launcher_test`): it may only reach the launcher through the exported surface, so it proves the client's view compiles and behaves. It covers config load with the warning sink and the merged `context_size` off the resolved profile, the `ErrConfigNotFound` and `ErrNotRunning` paths (`errors.Is` across the module boundary), httptest-backed discovery, and the drift notice threaded end-to-end — a plain `LoadProfile` on the ADR-0007 idempotent path against an httptest llama-server stand-in, delivering exactly one notice to the sink with nothing reaching stderr. No real processes: httptest and temp directories only, so it runs under plain `make test` (§12).

The two v1.6.1 sentinels are proved differently, because neither is reachable through a facade verb in a test: `ErrStartupTimeout` needs a real managed fork that then misses its health window, and `ErrUnsupported` only ever comes back from a windows build; `ErrLoadCanceled` likewise needs a real managed fork, stopped mid-load. So the boundary proof for all three is **identity** — `launcher/launcher_internal_test.go` is the facade's one same-package file and holds each re-exported value against the core value it aliases, which is exactly what lets `errors.Is` find the sentinel through an error the core wrapped (a sentinel re-declared with `errors.New` would read the same in godoc and match nothing). The behaviour behind `ErrStartupTimeout` is covered where it lives, in `internal/launcher`'s `TestLoadProfile_StartupTimeoutIsErrStartupTimeout` (§12.2), and the behaviour behind `ErrLoadCanceled` in `TestLoadProfile_ServerExitMidWaitEndsTheLoad`. The windows stubs have no test host and are error-return one-liners; the cross-compile gate (§12) is what keeps them compiling.

What in-repo tests cannot see is the alias-over-internal pattern compiling *from another module*; that was verified once during implementation with a throwaway module outside the repo (`replace` to the local checkout) referencing every documented symbol, and it is re-checked whenever the surface changes.

### 16.6 Platform contract

The facade aliases into `internal/launcher/`, so importing it compiles the **whole** core — which makes "does this build on the client's OS?" part of the public contract rather than an implementation detail. v1.6.0 failed that test in the field: it built on darwin only, while its first client's CI runs on Linux and keeps a Windows cross-build green. [ADR-0012](docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md) settles it, and `launcher/doc.go` carries the same contract in prose so `go doc` states it beside the API:

> The package **compiles on darwin, linux and windows; every verb works where its mechanism exists; where it does not, the verb returns a clean sentinel instead of failing to build.**

| Platform | What it does |
|---|---|
| **darwin** | Everything, byte-identically — the status quo the seams were extracted from, not a reimplementation of it. |
| **linux** | Everything. Process control is POSIX and shared with darwin behind a `unix` build tag; the runtime shell-outs (`lsof`, `ps`, `tail`) exist; the menu poll needed only linux's two-value `syscall.Select`. The menu's "Edit config" runs `$VISUAL`/`$EDITOR` in the terminal in place of darwin's `open`, and is not offered when neither is set (§3.1). The single omission is the menu's memory/GPU readout, which is macOS-only and leaves its line out elsewhere (§5.2, `sysmem.go`). |
| **windows** | Compiles, and actuates over HTTP: `DiscoverRunningInstances`, Ollama/LM Studio model load and unload, and `LoadProfile` against an **already-running** server. LM Studio's start and stop go further, because they are `lms` shell-outs with no unix dependency. What needs unix process control — forking `llama-server`, `splash serve` or `ollama serve`, and every kill-by-PID path — does not act; see below for exactly how each refusal surfaces. |

The platform knowledge lives in per-OS build-tagged files, never in a `runtime.GOOS` branch inside a shared path: `process_unix.go` / `process_windows.go` (`detachedSysProcAttr`, `signalPID`, `signalGroup`, `requireProcessControl`, `listProcesses`), `proc_argv_darwin.go` / `proc_argv_linux.go` / `proc_argv_other.go` (`procArgv`, the true argv behind `listProcesses`), `proc_identity_darwin.go` / `proc_identity_linux.go` / `proc_identity_windows.go` (`processIdentity`, the start-time check before each stop signal), `config_trust_unix.go` / `config_trust_windows.go` (`configTrusted`, the `api_key_cmd` ownership gate) and `ui_poll_darwin.go` / `ui_poll_linux.go` / `ui_poll_windows.go` (`pollStdin`, `requireInteractiveMenu`). One `runtime.GOOS` read in a shared path is deliberate: `menu.go`'s `editConfigGOOS` picks what "Edit config" runs. That verb needs no platform primitive, only a different program (`open` or the user's editor), so both branches compile everywhere and a test can drive either on any host. The two `require…` guards exist so a refusal happens *before* the irreversible step — forking a child no stop path could reach would be worse than not starting one. Because Go's implicit filename constraints bind that set, the package's platform vocabulary is now exactly darwin, linux and windows; other unixes (freebsd, which used to compile the BSD poll) are outside the contract.

**What `errors.Is(err, ErrUnsupported)` actually holds for.** Each windows stub returns an error wrapping the sentinel, but only the paths that hand that error straight back preserve it. Three do, one does not, and a client should know which:

- **Holds — starting a managed server.** `startManagedServer` calls `requireProcessControl()` first and returns its error unchanged, so `LoadProfile` (and the CLI's `load` / `start`) on a llamacpp or Splash profile fails with a wrapped sentinel before anything forks.
- **Holds — the interactive menu.** `RunInteractiveMenu` calls `requireInteractiveMenu()` first; the CLI's zero-arg path prints `the interactive menu is not supported on windows` and exits 3 (§3.3). The menu never ran on windows — this is that fact expressed as a refused verb instead of a build error.
- **Holds — auto-starting an external server.** `Ollama.TryStart` asks `requireProcessControl()` before it forks, and `connectExternalServer` wraps the `TryStart` error (`Ollama not reachable at <addr> and could not be started: …`), so `LoadProfile` against a stopped Ollama fails with the wrapped sentinel. Until 2026-09-24 this path replaced every `TryStart` failure with a generic "start it manually" message and lost the sentinel.
- **Does not hold — the stop verbs.** `IsProcessAlive` is `signalPID(pid, 0) == nil`, always false on windows, so `terminatePID` is never reached and no wrapped sentinel is produced. `Stop` / `Unload` fall through to the backend's own stop hook: LM Studio's `lms server stop` works, so the stop genuinely succeeds there, while llamacpp's and Ollama's hooks are no-ops (§6.5) and the verb ends at the generic `server at <addr> is still reachable and its PID could not be determined`.

So `ErrUnsupported` is precise at the **seam** and at the three paths that surface a seam error directly; it is not yet a blanket promise that every operation a windows build cannot complete comes back wrapping it. ADR-0012 was amended on 2026-07-29 to say exactly this — its windows bullet had claimed the sentinel for every kill-by-PID stop path, which the code does not do — and it recorded the last two paths as a known gap in its own precision rather than as intended behaviour. The external auto-start was closed on 2026-09-24 (ADR-0012's second amendment); closing the stop verbs would be additive and would not change darwin behaviour; it stays open only because there is no windows host to prove it against, and it is tracked in `TODO.md`.

The gate that keeps all of this true is `make cross` (§12): a platform regression fails here rather than in an importing client's CI.
