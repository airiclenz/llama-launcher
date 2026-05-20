# llama-launcher — Technical Design Specification

## 1. Overview

`llama-launcher` is a lightweight Go CLI tool for managing LLM servers through named configuration profiles. It starts the server as a detached background process and manages models dynamically via the server's HTTP API (router mode), freeing all launcher memory between invocations. Subsequent invocations interact with the running server via a persisted state file.

The architecture is backend-agnostic: a `Backend` interface abstracts server-specific logic, making it straightforward to add support for other LLM servers (LM Studio, Ollama, etc.) alongside the initial llama.cpp implementation.

Target platform: macOS (Apple Silicon). The compiled binary is the only artifact — no runtime dependencies.

## 2. Goals and Non-Goals

### Goals

- Profile-based configuration with defaults and per-profile overrides for all relevant server flags.
- One-shot execution model: the launcher process exits after dispatching work, consuming zero resident memory while the server runs.
- Router mode: start the server once, load/unload models dynamically via HTTP API without restarting.
- Interactive profile selection with arrow-key navigation when invoked without arguments.
- Non-interactive subcommands for scripting and automation.
- Clean process lifecycle management (start, stop, load, unload, status) via PID tracking.
- Single YAML config file, easy to read and version-control.
- Modular backend architecture for supporting multiple LLM server implementations.

### Non-Goals

- Persistent TUI or dashboard (contradicts the memory-freeing goal).
- Model downloading or GGUF management.
- API proxying or request routing.
- Heavy multi-server orchestration (basic concurrent servers are supported via `auto_stop_server: false`).
- Cross-platform support beyond macOS (Linux would work but is not a design target).

## 3. User Experience

### 3.1 Interactive Mode

Running `llama-launcher` with no arguments enters a one-shot interactive menu with arrow-key navigation, colored output, and full-screen repainting. Falls back to numbered input when stdin is not a terminal.

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

After selecting a profile, the launcher starts `llama-server`, loads the model via API, prints a confirmation, and exits:

```
  Loading code-deepseek...
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

    ↑↓ select · enter load · q quit
```

Selecting "Switch model" presents the profile list (excluding the currently loaded profile), unloads the current model via API, loads the new one, and exits.

### 3.2 Subcommands

For scripting, automation, and quick access:

| Command | Behaviour |
|---|---|
| `llama-launcher load <profile>` | Primary command. Start server if needed, then load model via API. Unloads current model first if different (respects `auto_unload`/`auto_stop_server`). |
| `llama-launcher unload [profile]` | Unload model via API (external backends) or stop server (managed backends). Optional profile argument to target a specific backend; auto-detects when only one model is loaded. |
| `llama-launcher start` | Start server without loading a model. |
| `llama-launcher stop [backend]` | Send SIGTERM to running server, wait for graceful shutdown, clean up state. Optional backend argument; auto-detects when only one server is running. |
| `llama-launcher status` | Print per-backend server state (running/stopped, model, address) for all configured backends. Exit code 0 if any running, 1 if all stopped. |
| `llama-launcher list` | Print all configured profiles with descriptions and backend. |
| `llama-launcher logs [backend] [--follow]` | Tail a server's log file. Optional backend argument; auto-detects the active backend. With `--follow`, behaves like `tail -f`. This is the one subcommand that remains running until interrupted. |

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

# Stop the old server when switching to a different backend (default: true).
# Set to false to allow multiple servers to run simultaneously.
# auto_stop_server: true

# Unload the current model when loading a different one on the same
# server (default: true). Set to false to keep multiple models loaded.
# auto_unload: true

# Default parameters applied at server start (shared by all models).
# The "server" field selects which server to use by default.
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

  # Sampling defaults (used as server-side defaults for API requests)
  temperature: 0.7
  repeat_penalty: 1.1
  top_k: 40
  top_p: 0.95
  min_p: 0.05

# Named profiles (model configurations).
# Profiles can target different servers — switching between them
# stops the old server and starts/connects to the new one.
profiles:
  code-deepseek:
    description: "DeepSeek Coder V2 Lite — coding tasks"
    model: deepseek-coder-v2-lite-instruct-Q4_K_M.gguf
    context_size: 8192
    temperature: 0.3
    extra_args:
      - "--no-warmup"

  chat-qwen:
    description: "Qwen 2.5 32B — general conversation"
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

The `servers` map lists available backends. Keys are backend names (`llamacpp`, `ollama`, `lmstudio`). Values are booleans: `true` enables the server, `false` disables it. Disabled servers are hidden from status display, and profiles targeting a disabled server are excluded from menus, CLI output, and profile resolution. At least one server must be enabled. Managed vs external is determined by backend type — llamacpp is always managed (fork), Ollama and LM Studio are always external (connect).

### 4.3 Parameter Resolution

Parameters are resolved in order of precedence (highest first):

1. Profile-level value (including `server`)
2. `defaults` block value (including `server`)
3. Backend-specific fallback from `servers` map address or `Backend.DefaultAddr()` (e.g. Ollama defaults to `localhost:11434`, LM Studio to `localhost:1234`)
4. Built-in fallback (only for `host: 127.0.0.1` and `port: 8080`)

All numeric and boolean parameters use pointer types internally to distinguish "not set" from zero/false. A nil pointer means "inherit from the next level."

### 4.4 Model Path Resolution

The `model` field in a profile is resolved as follows:

1. If the path is absolute, use it directly.
2. If relative, join with `models_dir` from config.
3. `models_dir` itself supports `~` expansion.
4. Validate that the resolved path exists and is a regular file before loading.

Model path resolution is delegated to the backend via `Backend.ResolveModel()`. llama.cpp validates that the file exists on disk. Ollama and LM Studio accept opaque model identifiers (e.g. `llama3.1:8b`, `lmstudio-community/meta-llama-3.1-8b-instruct`) without file validation.

### 4.5 Extra Arguments

The `extra_args` list in a profile is appended verbatim to the assembled server command line. This provides an escape hatch for flags not explicitly modelled in the config schema without requiring a launcher update.

### 4.6 Backend Selection

Each profile can specify a `server` field to override `defaults.server`. The `server` field participates in the same tier merge as all other parameters (profile → defaults → fallback). If only one server is configured and `defaults.server` is not set, it is auto-detected.

## 5. Architecture

### 5.1 Component Overview

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  CLI / Menu  │────▶│    Config    │────▶│   Server     │
│  (main.go)   │     │ (config.go)  │     │ (server.go)  │
│  (menu.go)   │     └──────────────┘     └──────┬───────┘
│  (ui.go)     │                                 │
└──────────────┘     ┌──────────────┐            │ managed: fork
                     │   Backend    │◀───────────┤ external: connect
                     │ (backend.go) │            │
                     └──────┬───────┘     ┌──────┴───────┐
                            │             │  State File  │
              ┌─────────────┼──────────┐  │ (state.json) │
              │             │          │  └──────────────┘
       ┌──────┴───────┐ ┌───┴───┐ ┌────┴─────┐
       │  LlamaCpp    │ │Ollama │ │ LMStudio │
       │ (backend_    │ │       │ │          │
       │  llamacpp.go)│ │       │ │          │
       └──────────────┘ └───────┘ └──────────┘
```

### 5.2 Source Files

| File | Responsibility |
|---|---|
| `main.go` | Entry point, `--config` flag parsing, subcommand dispatch, usage text. |
| `config.go` | Config/Profile/ProfileParams struct definitions, YAML loading, `~` expansion, parameter merging, validation, example config generation. Server enable/disable filtering via `IsServerEnabled()`. |
| `defaults/config.yaml` | Example config template, embedded at compile time via `go:embed`. |
| `defaults/embed.go` | Embeds `config.yaml` and exports it as `defaults.ExampleConfig`. |
| `backend.go` | `Backend` and `ManagedBackend` interface definitions, `ResolvedProfile` struct, backend registry (register/get). |
| `backend_llamacpp.go` | llama.cpp backend (managed): server arg assembly, model path resolution. Registers via `init()`. |
| `backend_ollama.go` | Ollama backend (external): HTTP API model load/unload, auto-start via `ollama serve`. Registers via `init()`. |
| `backend_lmstudio.go` | LM Studio backend (external): HTTP API model load/unload, `lms` CLI for server start/stop. Registers via `init()`. |
| `server.go` | Backend-agnostic lifecycle: managed path (fork/detach/SIGTERM/SIGKILL) and external path (connect/health-check/disconnect). Per-backend state file read/write (`state-{backend}.json`), PID tracking, `LoadProfile` orchestration with configurable auto-stop/auto-unload, log management. |
| `ui.go` | Low-level terminal operations: raw mode (via `golang.org/x/term`), ANSI escape codes, key reading, reusable `selectMenu()` component. |
| `menu.go` | Interactive menu logic for three states (stopped, running-with-model, running-no-model), backend-aware headers/items, simple fallback for non-terminals. |
| `config_test.go` | Tests for config loading, validation (deprecated fields, server enable/disable, auto-assignment), parameter merging, boolean accessors, `ExpandTilde` edge cases, and `ConfiguredBackendAddr`. |
| `backend_llamacpp_test.go` | Tests for llama.cpp arg assembly, model resolution, and httptest-based health check. |
| `backend_ollama_test.go` | httptest-based tests for Ollama health check (body discrimination), `LoadModel`, `UnloadModel`, and `ListRunningModels`. |
| `backend_lmstudio_test.go` | httptest-based tests for LM Studio health check (cross-backend exclusion), `LoadModel`, `UnloadModel`, and `extractLMStudioError`. |
| `backend_test.go` | Tests for `GetBackend` with known and unknown backends. |
| `server_test.go` | Tests for `IsProcessAlive` (including PID 0 guard), `readLastLines`, `backendStatePath`, `ServerState` methods, and state file permissions. |
| `menu_test.go` | Tests for `parseChoice`, `formatUptime`, `profileDisplayName`, and GPU offload display formatting. |
| `helpers_test.go` | Shared test helper `addrFromURL` for extracting `host:port` from httptest server URLs. |

### 5.3 Backend Interface

```go
type Backend interface {
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

type ManagedBackend interface {
    Backend
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

`PIDTracker` is implemented by external backends that auto-start a server process (Ollama). When `TryStart` succeeds and the backend implements `PIDTracker`, the launcher records the PID and marks the state as managed. `ModelLister` is implemented by backends that can enumerate loaded models (Ollama via `/api/ps`), used by the status display.

`ManagedBackend` is for backends where the launcher forks and owns the server process (llama.cpp). External backends (Ollama, LM Studio) implement only `Backend` with `TryStart`/`TryStop` hooks for auto-starting. The server lifecycle code uses `if mb, ok := b.(ManagedBackend)` to determine the process model.

Adding a new backend requires:
1. Create `backend_<name>.go` implementing `Backend` (and optionally `ManagedBackend`).
2. Register via `init()` calling `RegisterBackend()`.
3. Add the backend name to `servers:` in the config YAML.
4. Set `server:` on relevant profiles (or set as `defaults.server`).

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
| `GET /v1/models` | 200 | Valid model list JSON (real endpoint) |
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

### 6.1 Starting the Server

#### Managed backends (llama.cpp)

1. Load and validate config.
2. Resolve the backend; verify binary exists via `exec.LookPath`.
3. Build server arguments via `ManagedBackend.BuildServerArgs()` and environment via `BuildServerEnv()`.
4. Check state file — if a server is already running (PID alive) with the same backend, reuse it.
5. If a server is running with a different backend, stop it first.
6. Open the log file for stdout/stderr redirection.
7. Create `exec.Cmd` with `SysProcAttr{Setsid: true}` to detach the child process.
8. Call `cmd.Start()` (non-blocking).
9. Write state file with `managed: true`, PID, backend, host, port, and timestamp.
10. Wait 500ms to detect early exit (port conflict, binary not found, etc.).
11. Wait for backend health check to succeed (up to 15 seconds).
12. Print confirmation line.

#### External backends (Ollama, LM Studio)

1. Resolve the backend address from `servers` map (if host:port value) or `Backend.DefaultAddr()`.
2. Call `Backend.HealthCheck(addr)` to verify server is reachable.
3. If not reachable, call `Backend.TryStart()` (e.g. `lms server start`, `ollama serve`).
4. Poll health check until successful (up to 15 seconds) or fail with a user-friendly message.
5. Write state file with `managed: false`, PID 0, backend, host, port, and timestamp.
6. Print confirmation.

### 6.2 Loading a Model

Before loading, if `auto_stop_server` is true (the default), any running servers on *other* backends are stopped. This is skipped when `auto_stop_server: false`, allowing multiple servers to run concurrently.

#### Managed backends (llama.cpp)

1. Read per-backend state file for the target backend.
2. If the same profile is already loaded (check state file), exit early.
3. Stop existing server, start new server with model in args (llama.cpp bakes model path into `--model` flag).
4. Wait for health check (up to 30 seconds).
5. Update per-backend state file with active_profile and active_model.
6. Print confirmation.

#### External backends (Ollama, LM Studio)

1. Ensure server is connected (connect if needed — see 6.1).
2. If the same profile is already loaded, exit early.
3. If a different model is loaded and `auto_unload` is true (the default), call `Backend.UnloadModel()` via HTTP API. When `auto_unload: false`, the previous model remains loaded.
4. Call `Backend.LoadModel()` via HTTP API.
5. Update per-backend state file with active_profile and active_model.
6. Print confirmation.

### 6.3 Unloading a Model

#### Managed backends

Stop the server entirely (model is embedded in server args).

#### External backends

Call `Backend.UnloadModel()` via HTTP API. Clear active_profile and active_model in state file. Server remains connected.

### 6.4 Stopping the Server

#### Managed backends

1. Read state file; extract PID.
2. Verify the process is alive (`syscall.Kill(pid, 0)`).
3. Send `SIGTERM` to the process and its process group (`kill(-pid, SIGTERM)`).
4. Poll for process exit (100 ms intervals, up to 15 seconds).
5. If still alive after timeout, send `SIGKILL` to both process and group, and log a warning.
6. Wait for the process to actually die (up to 5 seconds), then wait 500 ms for the OS to release the TCP port.
7. Remove state file.
8. Print confirmation.

**Technical reasoning — process group signals:**

The server is started with `SysProcAttr{Setsid: true}`, which gives the child its own session and process group (PGID = PID). Sending `SIGTERM` to just the PID signals only the main process. If the server has spawned child processes (e.g. worker threads for CUDA/Metal, model loading), those children may keep the main process alive or hold resources. Sending the signal to the entire process group via `syscall.Kill(-pid, sig)` ensures all children also receive it. Errors from the group signal are ignored (the group may not exist if the process already exited).

**Technical reasoning — SIGKILL port release wait:**

After `SIGKILL`, `stopManagedServer` must wait for the process to actually die before returning. Without this wait, the TCP port may still be held by the dying process when the next backend tries to start on the same port, causing a 15-second health check timeout ("server did not become healthy within 15s"). The implementation polls `IsProcessAlive` for up to 5 seconds after SIGKILL, then waits an additional 500 ms (`startupGracePeriod`) for the OS to release the TCP socket in the `TIME_WAIT` / cleanup phase.

#### External backends

1. Read state file.
2. Optionally call `Backend.TryStop()` (e.g. `lms server stop`).
3. Remove state file.
4. Print "Disconnected from X (server still running at addr)".

### 6.5 Status Check

1. Read state file. If missing → report stopped, exit 1.
2. Verify server is alive (`IsServerAlive` — PID check for managed, health check for external).
   - Alive → report running/connected with model (if any), backend display name, address. For managed: also PID, uptime, log. Exit 0.
   - Dead/unreachable → report stale state, clean up state file. Exit 1.

### 6.6 Stale State Handling

On every operation that reads state, the launcher verifies the server is alive. For managed backends, this is a PID check. For external backends, this is a health check HTTP call. Health checks are discriminating — each backend identifies its own server and rejects responses from other backends sharing the same address (see §5.3 Health Check Discrimination). If the server is gone or belongs to a different backend, the per-backend state file is removed and the launcher proceeds as if that backend is not running.

## 7. State Files

### 7.1 Location

Per-backend state files in `~/.config/llama-launcher/`:

```
state-llamacpp.json
state-ollama.json
state-lmstudio.json
```

Each backend has its own state file, enabling multiple servers to run concurrently. `ReadBackendState(backend)` reads a single backend's state; `ReadAllStates()` reads all state files via glob.

**Migration**: On first access, if a legacy `state.json` exists, it is migrated to `state-{backend}.json` and the old file is deleted.

### 7.2 Schema

Each state file has the same schema:

```json
{
  "pid": 41023,
  "managed": true,
  "backend": "llamacpp",
  "host": "127.0.0.1",
  "port": 8080,
  "started_at": "2026-05-19T17:12:00+02:00",
  "log_file": "/Users/airic/.config/llama-launcher/logs/llamacpp-20260519-171200.log",
  "active_profile": "code-deepseek",
  "active_model": "/Users/airic/Models/deepseek-coder-v2-lite-instruct-Q4_K_M.gguf",
  "context_size": 4096,
  "gpu_layers": 99
}
```

`managed` indicates whether the launcher owns the server process (true) or connected to an external server (false). For managed backends, PID is tracked and used for liveness checks. For external backends that were auto-started via `TryStart` (and implement `PIDTracker`), PID is also tracked and managed is set to true. For external backends not started by the launcher, PID is 0 and liveness is checked via the backend's health endpoint.

`active_profile` and `active_model` are empty strings when the server is running but no model is loaded. `context_size` and `gpu_layers` record the server's global settings for hardware conflict detection. `log_file` is omitted for external backends not started by the launcher.

**Backward compatibility**: Legacy state files without the `managed` field are treated as managed if PID > 0.

### 7.3 Atomicity

The state file is written atomically: write to a temporary file in the same directory, then `os.Rename` to the final path. This prevents corruption from a crash or power loss during the write.

## 8. Server Argument Assembly

The launcher builds the `llama-server` command line from the merged default parameters. In router mode, no `-m` model flag is passed — models are loaded via the HTTP API.

| Config Field | Flag |
|---|---|
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

Argument assembly is delegated to the backend via `Backend.BuildServerArgs()`, allowing each backend to map config fields to its own CLI flags.

## 9. Model Management API (llama-cpp)

The llama-cpp backend communicates with `llama-server` in router mode using these HTTP endpoints:

| Endpoint | Method | Purpose |
|---|---|---|
| `/health` | GET | Server health check (polled after start). |
| `/models` | GET | List available models with load status. |
| `/models/load` | POST | Load a model. Body: `{"model": "filename.gguf"}`. |
| `/models/unload` | POST | Unload a model. Body: `{"model": "filename.gguf"}`. |

Model names in API calls use the basename of the resolved file path, since `--models-dir` tells the server where to find files.

## 10. Log Management

Server stdout and stderr are redirected to a log file at:

```
<log_dir>/<backend>-<YYYYMMDD>-<HHMMSS>.log
```

Example: `~/.config/llama-launcher/logs/llamacpp-20260519-171200.log`

The `logs` subcommand tails the current server's log file (path from state file). With `--follow`, it uses `tail -f` and is the only mode where the launcher remains running.

Log rotation and cleanup are out of scope — the user manages this externally or the files are small enough to be irrelevant.

## 11. Error Handling

| Scenario | Behaviour |
|---|---|
| Config file missing (first run) | Generate example config, print path, exit 2. |
| Config file parse error | Print error with line number (from yaml.v3), exit 2. |
| Unknown profile name | Print error, exit 2. |
| Model file not found | Print resolved path, exit 2. |
| Server binary not found | Print configured path, exit 3. |
| Server already running (on `start`) | Report existing server, exit 0. |
| No server running (on `stop`/`unload`) | Print message, exit 1. |
| Failed to start process | Print OS error, exit 3. |
| Server health timeout | Print timeout message, exit 3. |
| Model load/unload API error | Print server response, exit 3. |
| SIGTERM timeout (on `stop`) | Escalate to SIGKILL, warn, exit 0 (server is stopped). |
| State file corrupt or unreadable | Delete state file, treat as stopped, warn. |
| Port already in use | Detected via early server exit — the launcher checks if the process is still alive ~500 ms after start and reports the log tail if it died. |
| Hardware param conflict | Warn when profile requests different context_size or gpu_layers than server's global setting (router mode limitation, #20851). |

## 12. Known Limitations

### Router Mode Per-Model Hardware Overrides

llama-server's router mode does not properly apply per-model hardware overrides (context_size, n-gpu-layers) defined in model configuration. The router forces global hardware flags on every child process. See [llama.cpp issue #20851](https://github.com/ggml-org/llama.cpp/issues/20851).

**Impact**: On a 32 GB setup, all models share the same context_size and gpu_layers set at server start via `defaults`. Profiles that specify different values will trigger a warning but the server's global settings apply.

**Workaround**: Stop and restart the server with different defaults when profiles need incompatible hardware settings.

## 13. Future Considerations

These are explicitly out of scope for v1 but noted as natural extensions:

- **Automatic restart for hardware conflicts**: When a profile needs different context_size/gpu_layers, offer to restart the server with updated global settings.
- **Multiple simultaneous servers**: Basic support is implemented via `auto_stop_server: false` and per-backend state files. Advanced orchestration (e.g. load balancing, port conflict detection) could be added.
- **Shell completions**: Generate bash/zsh/fish completions for subcommands and profile names.
- **Config reload**: A `reload` subcommand that restarts with the same profile using updated config values.
- **Additional backends**: vLLM and other backends — each as a new `backend_<name>.go` file implementing the `Backend` interface.
- **Homebrew formula**: Package for `brew install llama-launcher`.
- **Launchd integration**: Generate a launchd plist for auto-start on login.
- **Health-based model status**: Query `GET /models` to verify actual loaded state rather than relying solely on state file.

## 14. Testing

### 14.1 Unit Tests (httptest)

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

### 14.2 Server & Config Tests

| Test | What it covers |
|---|---|
| `TestIsProcessAlive` | Current PID → true; PID 0 → false; negative PID → false; invalid PID → false. |
| `TestReadLastLines` | More lines than requested; fewer lines; nonexistent file. |
| `TestBackendStatePath` | Absolute path format, correct filename. |
| `TestServerState_Addr` / `_Uptime` | State helper methods. |
| `TestGetBackend` | Known backends return correct instance; unknown returns error. |
| `TestExpandTilde` | `~/path`, bare `~`, `~username` (unchanged), absolute path, empty. |
| `TestLoadConfig` | Missing file, valid config, no-profiles validation. |
| `TestValidate_*` | Deprecated fields, no servers enabled, auto-assign default server. |
| `TestShouldAutoClose` / `TestShouldDisplayCentered` | Nil-defaults-to-true/false asymmetry. |
| `TestConfiguredBackendAddr` | Returns merged address with colon separator. |

### 14.3 Menu Helper Tests

| Test | What it covers |
|---|---|
| `TestParseChoice` | Valid, zero, negative, exceeds max, non-numeric, empty. |
| `TestFormatUptime` | Hours, minutes, seconds-only branches. |
| `TestProfileDisplayName` | With description, without, unknown profile. |
| `TestFormatProfileParams_GPULayers_LMStudio` | Intermediate value shows number; 99 shows max; 0 shows off. |

### 14.4 Test Helpers

`helpers_test.go` provides `addrFromURL(t, rawURL) string`, which parses an `httptest.NewServer` URL and returns the `host:port` portion for passing to backend methods that expect an `addr` string.

## 15. Build and Installation



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

## 16. Coding Standards

Follow `skills/coding-standards/SKILL.md` when writing or modifying code. Read the base references and the Go-specific extensions before making changes.

### After Changing Code

1. Update the documents `llama-launcher.TDD.md`. `README.md` as well as `CHANGELOG.md` if the change affects behavior, configuration schema, subcommands, error handling, or any other aspect covered here.
2. Run `make install` to build and install the updated binary.
