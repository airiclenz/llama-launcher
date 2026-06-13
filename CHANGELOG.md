# Changelog

## 1.4.4

### Added

- **Optional MCP control-plane adapter (`llama-launcher-mcp`).** A separate binary (`make build-mcp`) that exposes the lifecycle commands as [MCP](https://modelcontextprotocol.io) tools over HTTP, so a client on another machine — typically a coding agent in a container — can control which model is running on the host. It runs on the host and implements every tool by shelling out to the CLI; it dispatches *control* commands only and never proxies inference traffic, so `llama-launcher` itself keeps no listener and [ADR-0002](docs/adr/0002-not-a-router.md) stays intact (see [ADR-0008](docs/adr/0008-mcp-control-plane-adapter.md)). Tools: `list_profiles`, `server_status`, `tail_log` (read) plus `load_profile`, `unload_model`, `start_server`, `stop_server` (mutating, omitted under `--read-only`). Access is gated by a **source-IP allowlist** (`--allow <ip|cidr|host>`, repeatable; a hostname is resolved to its IPs once at startup; defaults to loopback only) — or `--allow-interface <name>` (repeatable) to allow the subnet(s) of a local interface such as the container-facing bridge (`bridge100`), which covers whatever IP the bridge hands the container so you never need to know or pin it. The listener is meant to **bind the container-facing bridge interface** (`--listen`, not `0.0.0.0`) — so the client receives no token or key it could leak, which is the point when the remote is a cloud LLM agent. `--llama-launcher-bin` and `--config` tune which CLI and config the adapter drives. The new dependency (`github.com/modelcontextprotocol/go-sdk`) is scoped to this binary; the core CLI build stays dependency-light.

## 1.4.3

### Added

- **Per-server API keys.** Entries in the `servers:` section now accept a mapping form alongside the plain bool: `llamacpp: {enabled: true, api_key: "secret"}` (`enabled` defaults to `true` when omitted). For `llamacpp` the key is passed as `--api-key` at launch, so llama-server rejects client requests without `Authorization: Bearer <key>` (`/health` stays open). For `lmstudio` the key is the token generated in LM Studio's own *Require API token* setting — the launcher sends it so its health checks and model loads keep working when that setting is on. For `ollama`, which has no native auth, a key is only useful behind an authenticating reverse proxy. In all cases the launcher attaches the key as a `Bearer` header to every HTTP call it makes to that server; 401/403 responses now produce an actionable "check api_key" error, the key is trimmed of stray whitespace with a load-time warning, and a `--api-key` value appearing in `extra_args` is masked as `***` in the "Show model config" pop-up.

## 1.4.2

### Added

- **Colors in the memory readout.** `memory_status_format` accepts inline style tags: the 8 standard ANSI color names (`{red}`, `{green}`, …), their `{bright-*}` variants, `{gray}` (the ANSI bright-black slot), 256-color palette indices (`{0}`–`{255}`), exact 24-bit hex colors (`{#rrggbb}` / `{#rgb}`), `{bold}`, `{dim}`, and `{reset}`. Named colors follow the terminal theme; palette and hex colors render identically everywhere. Bar colors and the `memory_status_bar` keys accept the same three forms. A template without style tags or bars keeps the classic all-dim rendering byte-for-byte; one that contains any styling is rendered as-is, with `{reset}` returning to the terminal default. Unknown tags pass through literally, as placeholders always have.
- **Percentage bar graphs in the memory readout.** Any percentage placeholder can render as a value-less bar: `{used_ram_pct:bar}`, or with inline overrides `{swap_used_pct:bar:6:yellow:gray}` (`:width:color:bgcolor`, trailing and empty parts optional). The fill uses full blocks plus an eighth-block partial cell (`▏▎▍▌▋▊▉` — 8 levels per cell) in the fill color; the background color is painted as a solid ANSI background behind the partial cell and the empty remainder, so the bar is one continuous two-color strip. Any nonzero percentage shows at least a sliver; widths clamp to 1–40; malformed tokens pass through literally. The new optional `memory_status_bar` block sets the defaults (`width: 10`, `color: green`, `background: gray`); unknown color names fall back to the defaults with a load-time warning instead of failing config load.
- **Optional `title` profile field.** A human-readable label shown wherever a profile is presented to the user: the status header, the profile selection lists (server stopped / running with no model), the "Switch model" pop-up, the unload picker, and the load progress text. When unset, the profile name is shown instead. `list --json` includes the new field (`title`, omitted when empty).

### Changed

- **New default memory readout.** When `memory_status_format` is unset, the readout now uses the new styling: `{bold}Free RAM:{reset} {yellow}{free_ram} {bright-blue}{free_ram_pct}{reset} {used_ram_pct:bar} ✦ {bold}Swap:{reset} {yellow}{swap_used}{reset} ✦ {bold}GPU:{reset} {gpu_util_pct:bar}` — bold labels, colored values, and bars for used RAM and GPU utilization (filled = used, so the empty tail of the RAM bar is what's free). Set `memory_status_format: "RAM: {free_ram} free · Swap: {swap_used} used"` to keep the previous plain line.
- **The memory readout template is compiled once, not re-parsed per tick.** `memory_status_format` is parsed into a segment list and memoized across config reloads (`Config.CompiledMemoryTemplate`) instead of rebuilding a 14-entry string replacer on every 1-second render tick; rendering a styled template with two bars costs ~0.3 µs. Explicitly configured plain templates render byte-identically to before, dim wrap included.
- **The memory readout now updates every second.** The open menu runs on two timers: a fixed 1-second render tick refreshes the memory / swap / GPU readout, while server polling (running instances, loaded model, stale-menu detection) stays on `refresh_duration` (default 10 s) — `liveStatusHeaderFn` caches the discovery result between probes, so the faster tick adds no extra server traffic. When `show_memory_status` is `false` the menu keeps ticking at `refresh_duration` as before. The `MemStats` cache TTL drops from 2 s to 0.9 s so each tick reads fresh values; the `sysctl` / `vm_stat` / `ioreg` shell-outs now run once per second while a menu is open.
- **`description` is demoted to an optional, popup-only field.** Menus and the status header no longer render the description next to the profile name; it now appears only as a `Description` line in the "Show model config" pop-up, when set. The pop-up's frame title is the profile's `title` (or name) instead of the description. In `list --json`, `description` is omitted when empty instead of being emitted as `""`.
- **Status header no longer repeats the server-reported model name.** When a running instance matches a configured profile, the header shows `address · title-or-name` only; the raw model id from the server is shown only when no profile matches.
- **`[server]` tag visibility keys off enabled servers, not profiles.** Profile rows in the selection menus (server stopped, running with no model, "Switch model" pop-up) show the column-aligned `[server]` tag whenever more than one server is enabled in `servers:`. Previously the tag only appeared when the configured profiles themselves spanned more than one server, so it vanished as soon as all profiles targeted the same backend — even with several servers enabled. With a single enabled server the tag remains omitted, as before.

### Fixed

- **Externally loaded models now match their profile.** `matchProfileName` and the `load` idempotency check (ADR-0007) compared the profile's fully resolved model path against the name the server reports verbatim. Servers started outside the launcher report whatever path or alias they were launched with — llama-server defaults the id to the model file's basename — so the comparison never succeeded: "Show model config" said `No matching profile in config` and `load` would needlessly restart a server already serving the right model. Matching now falls back to basename comparison (`modelNamesMatch`); an exact path match still wins over a basename match, and ambiguity (several profiles sharing a basename) yields no match, as before.
- **Open menus now react to background state changes.** Loading or unloading a model from another terminal (e.g. via the CLI) while the interactive menu was open only updated the status header — the item list and the menu variant (stopped / idle / loaded) stayed frozen until a keypress, so e.g. "Show model config" never appeared. The header refresh tick (`refresh_duration`, default 10 s) now compares a signature of the live discovery result (backend, address, loaded model per instance) against the one the menu was built from and rebuilds the menu on mismatch. Detection is purely probe-based — no state file, consistent with the 1.3.1 stateless design.

## 1.4.1

### Added

- **GPU placeholders in the memory readout** — `memory_status_format` accepts three new placeholders: `{gpu_util_pct}` (GPU `Device Utilization %`), `{gpu_used_ram}` (unified RAM currently held by the GPU), and `{gpu_alloc_ram}` (unified RAM allocated to the GPU). Values are sourced from the `AGXAccelerator…` entry's `PerformanceStatistics` dict in `ioreg -r -c IOAccelerator`, folded into the existing 2-second `MemStats` cache (one extra subprocess per refresh, not per keystroke). Apple Silicon only; Intel Macs and ioreg failures degrade silently to `0%` / `0B`. No new top-level config keys.

### Changed

- **Tighter humanised byte format.** `humanBytes` and `formatBytes` now render without a space between value and unit (`12.4GB`, `512MB`, `0B`) for a more compact status line. Affects the memory readout placeholders and the log cleanup summary.

## 1.4.0

### Added

- **`refresh_duration` config option** — top-level integer (seconds, default `10`) controlling how often the interactive menu re-renders idle (memory readout, server status). Values below `1` are clamped to 1 second so a misconfigured `0` cannot spin the render loop. Threaded through `selectMenu` as a `time.Duration` parameter; replaces the previously hardcoded 10-second key-timeout.
- **Memory + swap readout in the status header** — the interactive menu's status block now shows free unified memory and current swap usage on a dim line beneath the per-server status. Refreshes via the existing 10-second key-timeout tick and on every keystroke, with a 2-second internal cache so the underlying `sysctl` / `vm_stat` shell-outs don't fire on every key. Two new top-level config keys: `show_memory_status` (default `true`; set to `false` to hide the line) and `memory_status_format` (template string; default `"RAM: {free_ram} free · Swap: {swap_used} used"`). Placeholders include byte values rendered via a 1024-based humaniser (`{free_ram}`, `{used_ram}`, `{total_ram}`, `{compressed_ram}`, `{swap_used}`, `{swap_total}`, `{free_swap}`) and rounded integer percentages (`{free_ram_pct}`, `{used_ram_pct}`, `{swap_used_pct}` — the last returns `0%` when swap is disabled). Free RAM follows Activity Monitor's "available" definition (free + inactive + speculative + purgeable pages); compressed RAM comes from `vm_stat`'s "Pages occupied by compressor" line. macOS-only, like the rest of the launcher.

## 1.3.2

### Added

- **`sort_alphabetically` config option** — top-level boolean (default `true`, preserving the existing favourites/server/name sort). Set to `false` to list profiles in the order they appear in `config.yaml` across every UI surface (TUI menu, non-terminal fallback, `llama-launcher list`). YAML insertion order is captured by re-parsing the document into a `yaml.Node` in `parseConfig` and walking the `profiles:` mapping in document order; disabled-server filtering is applied identically in both modes.
- **Embedded example config now lists every supported option** — `sort_alphabetically` is shown in the UI behaviour block, and `jinja` is included in the `defaults` block so the generated config exposes the full schema.

## 1.3.1

### Removed

- **State files are gone** — `~/.config/llama-launcher/state-*.json` is no longer written or read. Every command now derives the running set from live probes of the addresses in `config.yaml` plus the LLM Server's own API (`/v1/models`, `/api/ps`, `/props`). A one-shot cleanup deletes any leftover `state-*.json` on first run. Eliminates a class of "the tool says X but the truth is Y" bugs: a crashed server no longer leaves stale state behind, and models loaded externally on Ollama / LM Studio now show up in `status` and the menu without re-activation.

### Changed

- **`load` drift detection is now live** — ADR-0007's "the params have drifted since you activated this profile" notice is computed by querying the running llama-server (`GET /props`) and diffing against the freshly resolved Profile, instead of comparing against a persisted snapshot. Coverage narrows on Ollama and LM Studio (no equivalent endpoint) — there, model-name match alone is enough for the idempotency no-op and no per-parameter drift notice is emitted.
- **`llml logs` covers launcher-managed servers only** — log paths are resolved by globbing `{log_dir}/{backend}-*.log` and picking the most recent. Servers started outside the launcher log to wherever they were started; `llml logs` prints a clear message in that case rather than guessing.
- **`stop` discovers via `lsof`, always** — the PID-from-state shortcut is gone. Every stop probes the address, then asks `lsof` for the listening PID before signalling. Same behaviour as before for users; one fewer source of stale data.

## 1.3.0

### Added

- **`--json` flag on `status` and `list`** — both subcommands accept `--json` for structured output. `status --json` prints a JSON array with one element per enabled configured backend (`backend`, `running`, `address`, `active_profile`, `active_model`, `pid`, `uptime_seconds`); exit code parity with the human path (0 if any backend is running, 1 if all are stopped). `list --json` prints a JSON array with one element per profile (`name`, `description`, `backend`, `model`, plus `gpu_layers` and `context_size` when set). Default human-readable output is unchanged.

### Fixed

- **`stop` works for untracked / externally-started servers** — `stop` (both the TUI "Stop server" item and the CLI `llama-launcher stop`) now terminates servers that have no per-instance state file. The "Stop server" item appears whenever any enabled server is reachable at its configured address (mirroring the status-header probe strategy: state-file instances plus a fallback probe at `cfg.ConfiguredBackendAddr(name)`). When the backend's CLI stop hook is a no-op (notably llama.cpp, which has no stop command), the launcher discovers the listening PID via `lsof -nP -iTCP@host:port -sTCP:LISTEN -t` and signals it directly (SIGTERM, then SIGKILL after the existing 15s grace period). Recovers gracefully from the destructive legacy-state migration: an external `llama-server` that lost its state record can still be reaped through the menu or CLI. Requires `lsof` on PATH.
- **Menu header lists every enabled server again** — the status header now shows one row per enabled server in `servers:` (sorted alphabetically), with running/stopped indicator, address, active profile, and loaded model. The Phase 4 per-instance rewrite had reduced the header to a single `stopped` line when no state files existed, hiding configured-but-not-currently-tracked servers (including external servers running on their configured addresses). For each enabled server, the header probes either the known per-instance state addresses (when present) or the configured default address (when no state file exists yet), so externally-started servers light up without re-activation. Multi-instance is preserved: each healthy instance gets its own row; `stopped` is only emitted when no instance of that server type is reachable.

### Changed

- **Idempotent profile activation with drift notice** — `load <profile>` is now a no-op when the same profile is already active at the target address. If the recorded resolved parameters match, the call exits silently. If they have drifted (e.g. the user edited `context_size` in the config), a notice is printed to stderr listing each divergent field as `old → new` and pointing to `--restart`. Pass `--restart` (or `-r` / `--force`) to bypass the check and force re-activation. The check is per-address: activating the same profile on a different `host:port` does not collide. Implements [ADR-0007](docs/adr/0007-profile-activation-idempotency.md).
- **Per-instance state files keyed by `host:port`** — state is now tracked per running instance rather than per backend type. Files are named `state-{backend}-{port}.json` for loopback (`127.0.0.1`) and `state-{backend}-{host}-{port}.json` otherwise. Multiple instances of any backend (including multiple `llamacpp` servers on different ports) can coexist. State read/write goes through `ReadInstanceState(addr)`, `ReadInstancesForBackend(backend)`, and `ReadAllStates()`. Implements [ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md).
- **`stop` is unconditional** — the `managed` field is gone from the state schema, and `stop` no longer branches on whether the launcher started the server. The unified stop path signals the recorded PID (if alive) and also calls each backend's native shutdown command (`ollama stop`, `lms server stop`). PID is kept on the state record for `status` display and log-file association only; liveness is decided by `Backend.HealthCheck`. The CLI's `stop [target]` and `logs [target]` accept either a `host:port` (preferred when multiple instances of the same backend run) or a backend name. Implements [ADR-0001](docs/adr/0001-stop-is-unconditional.md).
- **Resolved-params snapshot on the state record** — activating a profile now records the merged `resolved_params` on the per-instance state file. The field is unused today but is the input for the drift notice / `--restart` flag landing in a later release ([ADR-0007](docs/adr/0007-profile-activation-idempotency.md)).
- **Legacy state files removed on first access** — `state.json` (pre-1.2) and `state-{backend}.json` (pre-1.3) files are deleted automatically; if the recorded process is still alive, re-activate the relevant profile to recreate per-instance state.
- **`defaults.server` soft-deprecated** — every profile should declare `server:` explicitly. When more than one server is enabled and a profile omits `server:`, the launcher still falls back to `defaults.server` but emits a deprecation warning naming the profile (printed to stderr at config load, and reported by `config validate`). Single-enabled-server configs are unaffected: the missing `server:` is auto-resolved with no warning. `defaults.server` is removed from the example config, the `example:` profile now sets `server: llamacpp` explicitly, and profile sort order in menus and `list` no longer ranks "default backend first" (it sorts alphabetically by server). Implements [ADR-0005](docs/adr/0005-profile-server-is-identity.md).
- **Cross-server `auto_unload`** — when `auto_stop_server: false` and `auto_unload: true` (default), activating a profile now unloads any model loaded on other still-running instances, not just the previous model on the same server. Implements [ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md): a single rule covers both same-server swap and cross-server cases. Managed backends (llamacpp) are silently skipped — they cannot unload without stopping the server.

### Architecture

- **`Backend` → `LLMServer` Go rename** — the central interface is now `LLMServer` (was `Backend`); `ManagedBackend` is now `ManagedLLMServer`; registry functions are `RegisterLLMServer` / `GetLLMServer`. Matches the domain term pinned in [CONTEXT.md](CONTEXT.md). No on-disk or YAML schema change: `ServerState.Backend` (JSON field `backend`) and the legacy `backend:` YAML migration check keep their existing names so persisted state and config-migration paths stay compatible.
- **Documented architectural decisions in ADRs** — the design intent behind `llama-launcher` is now recorded as numbered Architectural Decision Records under [`docs/adr/`](docs/adr/):
  - [ADR-0001](docs/adr/0001-stop-is-unconditional.md) — `stop` is unconditional; the `managed` distinction is removed.
  - [ADR-0002](docs/adr/0002-not-a-router.md) — `llama-launcher` is a process manager, not a request router.
  - [ADR-0003](docs/adr/0003-llamacpp-restart-per-profile.md) — llamacpp uses restart-per-Profile, not multi-Model hosting.
  - [ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md) — `auto_unload` is one rule covering both same-server and cross-server cases.
  - [ADR-0005](docs/adr/0005-profile-server-is-identity.md) — every Profile must name its LLM Server; `defaults.server` is soft-deprecated.
  - [ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md) — LLM Server instances are keyed by `host:port`; multi-instance is supported.
  - [ADR-0007](docs/adr/0007-profile-activation-idempotency.md) — Profile activation is idempotent by name within an address slot; `--restart` forces re-activation.
- **Domain language pinned in CONTEXT.md** — the terms LLM Server, Model, and Profile (and the verbs Activate, Load/Unload, Start/Stop) are now defined in [CONTEXT.md](CONTEXT.md).
- **Technical design doc realigned with the ADRs** — `llama-launcher.TDD.md` drops the obsolete "router mode" framing, the never-implemented hardware-conflict warning, and the managed/external split; rewrites the lifecycle and state-file sections to match the per-instance, address-keyed model. Implementation of the behavioural changes (cross-server `auto_unload`, `defaults.server` deprecation warning, per-instance state files, drift notice + `--restart`) lands in subsequent releases — see [`docs/handoffs/20260526-fit-gap-adrs-vs-code.md`](docs/handoffs/20260526-fit-gap-adrs-vs-code.md) for the phased plan.

## 1.2.2

### Added

- **Favourite profiles** — set `is_favourite: true` on a profile to pin it to the top of the menu and `list` output. Favourites display a right-aligned `★` marker at the end of the row, in a consistent column across the listing. Profile ordering now sorts by favourite status first, then by server (default backend first), then by name. Applies to the TUI menu, non-terminal fallback, and the `list` subcommand.
- **`config init` subcommand** — generate the example config file on demand. Refuses to overwrite an existing file unless `--force` (or `-f`) is passed.
- **`config reset` subcommand** — overwrite the config file with the example config, providing a quick way to return to a known-good starting point.
- **Automatic config reload** — the interactive menu re-reads the config file before each menu display and on every 10-second header refresh. Changes made via "Edit config" or an external editor take effect without restarting. If the file is invalid, the last good config is silently preserved.

### Changed

- **Reorder loaded-model menu** — menu items now appear as: Switch model, Unload model, Stop server, Show log, Show model config, Edit config. Moves destructive actions closer to the top and informational items toward the bottom. Applies to both TUI and non-terminal menus.

## 1.2.1

### Changed

- **Hide "Switch model" when only one profile exists** — when the config defines a single profile and it is loaded, the "Switch model" menu item is no longer shown. Applies to both the TUI and non-terminal fallback menus.

### Fixed

- **State file persisted model before load succeeded** — `connectExternalServer` and `startManagedServer` wrote `active_profile` and `active_model` to the state file before the model was actually loaded. If `LoadModel` or the health check then failed, the state file on disk incorrectly showed the model as loaded. Model fields are now written only after the load or health check succeeds.

### Added

- **`logs clean` subcommand** — delete old log files from the log directory. Defaults to removing files older than 7 days; `--days N` changes the threshold, `--all` removes everything. Always skips log files belonging to running servers. Reports how many files were removed and how much space was freed.
- **`log_retention` config option** — set `log_retention: 7` (days) to automatically clean up old log files on every server start. Runs silently before the new log file is created. Unset by default (no automatic cleanup).
- **`start --profile` flag** — `llama-launcher start --profile <name>` (or `-p`) starts the server and loads a profile in one step, acting as an alias for `load`. Plain `start` behavior is unchanged.
- **`config validate` subcommand** — dedicated command to check the config file for errors after editing. Reports all validation problems at once (deprecated fields, unknown servers, disabled servers, missing model files) instead of stopping at the first error. Exit code 0 for valid, 2 for invalid.
- **Step-by-step progress popup** — loading, stopping, and unloading operations now show a multi-step progress popup that updates in place as each lifecycle stage completes (e.g. "Starting server" → "Waiting for server"). CLI subcommands print plain text step output. Replaces the static single-line activity indicator.

## 1.2.0

### Fixed

- **Health check cross-detection** — llamacpp no longer falsely claims LM Studio's server when both share a port. LM Studio returns HTTP 200 for all paths (including `/health`) with `{"error":"..."}` in the body. Health checks now inspect the `/health` response body: llamacpp requires a `"status"` JSON field (e.g. `{"status":"ok"}`), and LM Studio's anti-llamacpp exclusion checks for the same field. Status-code-only checks have been replaced with body-based discrimination.
- **SIGKILL port release** — after SIGTERM timeout and SIGKILL, `stopManagedServer` now waits for the process to actually die (up to 5s) and adds a 500ms grace period for the OS to release the TCP port, preventing "not reachable after start attempt" errors when switching backends on the same port.
- **Ollama health check** — an empty-body 200 response no longer passes the health check; the body must contain "Ollama".
- **State migration data loss** — `migrateOldState` now checks the write result before deleting the old file; a failed write no longer silently destroys the only copy of the state.
- **UnloadModel error handling** — switching models now aborts if the current model fails to unload, instead of silently loading a second model on top.
- **LM Studio UnloadModel** — a non-200 response with no parseable error body now returns an error instead of silently succeeding.
- **GPU offload display** — LM Studio profiles with `gpu_layers` between 1 and 98 now display the actual value instead of "max".
- **ExpandTilde** — paths like `~username/data` are no longer corrupted; only `~` and `~/...` are expanded.
- **PID 0 guard** — `IsProcessAlive(0)` now returns false instead of signaling the calling process group.
- **TryStop error propagation** — LM Studio and Ollama `TryStop` methods now return errors instead of silently discarding them.
- **Blanket PID→Managed migration removed** — state files with `PID > 0` no longer have `Managed` forced to `true`, preventing the launcher from killing processes it did not start.

### Added

- **Server enable/disable toggle** — servers in the `servers` section now use boolean values (`true`/`false`). Disabled servers are hidden from status display, and their profiles are excluded from menus and CLI output. At least one server must be enabled.
- **Embedded example config** — the default config template is now a standalone YAML file (`internal/launcher/defaults/config.yaml`) embedded at compile time via `go:embed`, instead of an inline string constant.
- **Multi-server support** — multiple servers can run simultaneously when `auto_stop_server` is set to `false`. Status, menus, and CLI commands are aware of all running backends.
- **`auto_stop_server` config option** — controls whether switching to a different backend automatically stops the previous one (default: `true`). Set to `false` to allow concurrent servers.
- **`auto_unload` config option** — controls whether loading a new model on the same external backend automatically unloads the previous one (default: `true`). Set to `false` to keep multiple models loaded.
- **"Unload model" menu option** — available in the loaded-model menu. Shows a picker when multiple models are loaded across backends. For managed backends (llama.cpp), unloading stops the server; for external backends, the server stays running.
- **Per-backend state files** — state is now tracked in `state-{backend}.json` files (e.g. `state-ollama.json`, `state-llamacpp.json`). Old `state.json` is migrated automatically on first access.
- **Optional arguments for CLI commands** — `unload [profile]` to target a specific profile, `stop [backend]` to target a specific backend, `logs [backend]` to view a specific backend's log.
- **`PIDTracker` interface** — external backends that auto-start (Ollama) now track PID and log file for proper managed-mode lifecycle.
- **`ModelLister` interface** — backends can list running models (Ollama's `/api/ps`), shown in status output.
- **Ollama lifecycle management** — `ollama serve` auto-start with PID tracking, proper process stop via `ollama stop` + SIGTERM, model unload via API with error checking.
- **Backend health check tests** — httptest-based unit tests for all three backends' `HealthCheck` methods, including cross-detection discrimination (LM Studio excludes llamacpp and Ollama responses on the same port).
- **Backend HTTP method tests** — httptest-based tests for `LoadModel`, `UnloadModel`, and `ListRunningModels` across LM Studio and Ollama.
- **Server state tests** — tests for `IsProcessAlive`, `readLastLines`, state path construction, and `ServerState` methods.
- **Config validation tests** — tests for deprecated field rejection, server enable/disable, auto-assignment, boolean accessors, and `ExpandTilde` edge cases.
- **Menu helper tests** — tests for `parseChoice`, `formatUptime`, `profileDisplayName`, and GPU offload display formatting.
- **Backend registry tests** — tests for `GetBackend` with known/unknown backends.

### Changed

- **Menu refresh interval** — interactive menu now polls backend health every 10 seconds instead of every 1 second, reducing HTTP traffic.
- **File permissions tightened** — config, state, and log files/directories now use 0600/0700 instead of 0644/0755.
- **State migration runs once** — `migrateOldState` is wrapped in `sync.Once` to avoid redundant filesystem reads on every state access.

- **Multi-server status display** — `status` command and menu header show a compact one-line-per-backend view with running/stopped indicator, address, and loaded models.
- **State functions refactored** — `StopServer()` → `StopBackendServer(backend)`, `UnloadCurrentModel()` → `UnloadBackendModel(backend)`, `ReadState()` → `ReadBackendState(backend)` / `ReadAllStates()`.
- **"Stop server" menu option** — shows a picker when multiple servers are running.
- **CLI multi-server awareness** — `stop` and `unload` auto-detect when only one server/model is active; print disambiguation list when multiple are active and no argument is given.

## 1.1.0

### Added

- **Ollama backend** — connect to a running Ollama instance or auto-start `ollama serve`. Models loaded/unloaded via HTTP API (`/api/generate`).
- **LM Studio backend** — connect to a running LM Studio server. Models loaded/unloaded via REST API (`/api/v1/models/load`, `/api/v1/models/unload`). Auto-starts via `lms server start` if not reachable.
- **Multi-backend profiles** — a single config can mix profiles from llama.cpp, Ollama, and LM Studio. Switching profiles across backends stops the old server and starts/connects to the new one.
- **`server` field in tier system** — `defaults.server` sets the default backend; profiles override with `server: ollama` etc. Same merge rules as all other parameters.
- **Backend display name in UI** — the Server status line now shows the backend name (e.g. `Server   Ollama · localhost:11434`).
- **Backend tags on profiles** — when profiles use multiple backends, non-default profiles show a tag like `[Ollama]` or `[LM Studio]` in the menu.
- **API-based model unload** — `llama-launcher unload` uses the backend's HTTP API for external servers instead of stopping the server.

### Changed

- **Simplified config** — `default_backend` and `endpoints` removed. The `servers` section is now a simple list of enabled backends (uncomment to enable). Binary paths and addresses are auto-detected; custom values are optional.
- **Backend interface split** — `Backend` is now the base interface for all backends. `ManagedBackend` extends it for backends where the launcher forks and owns the server process (llama.cpp only).
- **Ollama is always external** — connects to running instance or auto-starts via `TryStart`. No PID tracking; the launcher doesn't kill Ollama on disconnect.
- **Server state tracks managed vs external** — new `managed` field in `state.json` distinguishes launcher-owned processes from external servers. Backward compatible with legacy state files.
- **Health checks are backend-specific** — each backend implements its own `HealthCheck` method instead of always polling `/health`.
- **Menu items adapt to backend type** — managed backends show "Stop server" and "Show log"; external backends show "Disconnect" and hide log access.
- **Per-backend default ports** — Ollama defaults to `11434`, LM Studio to `1234`, llama.cpp to `8080`. Auto-detected from backend defaults.
- **Status output adapted** — `llama-launcher status` shows "connected" instead of "running" for external backends, omits PID/Uptime/Log when not applicable.
- **Migration errors** — old config fields (`default_backend`, `endpoints`, `backend` on profiles) produce clear error messages explaining the new format.
