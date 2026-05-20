# Changelog

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
