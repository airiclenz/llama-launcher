# Changelog

## 1.2.0

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

### Changed

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
