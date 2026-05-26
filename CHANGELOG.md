# Changelog

## 1.3.0

### Changed

- **`defaults.server` soft-deprecated** — every profile should declare `server:` explicitly. When more than one server is enabled and a profile omits `server:`, the launcher still falls back to `defaults.server` but emits a deprecation warning naming the profile (printed to stderr at config load, and reported by `config validate`). Single-enabled-server configs are unaffected: the missing `server:` is auto-resolved with no warning. `defaults.server` is removed from the example config, the `example:` profile now sets `server: llamacpp` explicitly, and profile sort order in menus and `list` no longer ranks "default backend first" (it sorts alphabetically by server). Implements [ADR-0005](docs/adr/0005-profile-server-is-identity.md).
- **Cross-server `auto_unload`** — when `auto_stop_server: false` and `auto_unload: true` (default), activating a profile now unloads any model loaded on other still-running instances, not just the previous model on the same server. Implements [ADR-0004](docs/adr/0004-auto-unload-is-one-rule.md): a single rule covers both same-server swap and cross-server cases. Managed backends (llamacpp) are silently skipped — they cannot unload without stopping the server.

### Architecture

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
