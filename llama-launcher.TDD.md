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
- Multi-server orchestration (one server instance at a time).
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
| `llama-launcher load <profile>` | Primary command. Start server if needed, then load model via API. Unloads current model first if different. |
| `llama-launcher unload` | Unload model via API, server keeps running. |
| `llama-launcher start` | Start server without loading a model. |
| `llama-launcher stop` | Send SIGTERM to running server, wait for graceful shutdown, clean up state. |
| `llama-launcher status` | Print current server state (running/stopped, model, PID, port, uptime) and exit. Exit code 0 if running, 1 if stopped. |
| `llama-launcher list` | Print all configured profiles with descriptions and backend. |
| `llama-launcher logs [--follow]` | Tail the active server's log file. With `--follow`, behaves like `tail -f`. This is the one subcommand that remains running until interrupted. |

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
# Server binaries, keyed by backend name.
# Adding a new backend is as simple as adding an entry here
# and implementing the Backend interface.
servers:
  llamacpp: /usr/local/bin/llama-server

# Default backend when profiles don't specify one.
# Auto-detected if only one server is configured.
default_backend: llamacpp

# Base directory for model files. Profile model paths are resolved
# relative to this directory unless they are absolute.
models_dir: ~/Models

# Directory for server log files.
log_dir: ~/.config/llama-launcher/logs

# Default parameters applied at server start (shared by all models).
# Per-model overrides for hardware params (context_size, gpu_layers)
# are not supported in router mode — see llama.cpp issue #20851.
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

# Named profiles (model configurations)
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

  reasoning-phi:
    description: "Phi-4 — structured reasoning"
    model: phi-4-Q5_K_M.gguf
    context_size: 8192
    temperature: 0.4
    repeat_penalty: 1.2
```

### 4.3 Parameter Resolution

Parameters are resolved in order of precedence (highest first):

1. Profile-level value
2. `defaults` block value
3. Built-in fallback (only for `host: 127.0.0.1` and `port: 8080`)

All numeric and boolean parameters use pointer types internally to distinguish "not set" from zero/false. A nil pointer means "inherit from the next level."

### 4.4 Model Path Resolution

The `model` field in a profile is resolved as follows:

1. If the path is absolute, use it directly.
2. If relative, join with `models_dir` from config.
3. `models_dir` itself supports `~` expansion.
4. Validate that the resolved path exists and is a regular file before loading.

Model path resolution is delegated to the backend via `Backend.ResolveModel()`, allowing future backends to use non-file-based model references (e.g., Ollama model identifiers).

### 4.5 Extra Arguments

The `extra_args` list in a profile is appended verbatim to the assembled server command line. This provides an escape hatch for flags not explicitly modelled in the config schema without requiring a launcher update.

### 4.6 Backend Selection

Each profile can specify a `backend` field to override the `default_backend`. If omitted, the profile uses the default. If `default_backend` is also omitted and only one server is configured, that server's backend is used automatically.

## 5. Architecture

### 5.1 Component Overview

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  CLI / Menu  │────▶│    Config    │────▶│   Server     │
│  (main.go)   │     │ (config.go)  │     │ (server.go)  │
│  (menu.go)   │     └──────────────┘     └──────┬───────┘
│  (ui.go)     │                                 │
└──────────────┘     ┌──────────────┐            │ fork + detach
                     │   Backend    │◀───────────┤
                     │ (backend.go) │            │ HTTP API
                     └──────┬───────┘            │ (load/unload)
                            │                    │
                     ┌──────┴───────┐     ┌──────┴───────┐
                     │  LlamaCpp    │     │  State File  │
                     │ (backend_    │     │ (state.json) │
                     │  llamacpp.go)│     └──────────────┘
                     └──────────────┘            │
                                                 ▼
                                          ┌──────────────┐
                                          │ llama-server  │
                                          │  (detached,   │
                                          │  router mode) │
                                          └──────────────┘
```

### 5.2 Source Files

| File | Responsibility |
|---|---|
| `main.go` | Entry point, `--config` flag parsing, subcommand dispatch, usage text. |
| `config.go` | Config/Profile/ProfileParams struct definitions, YAML loading, `~` expansion, parameter merging, validation, example config generation. |
| `backend.go` | `Backend` interface definition, `ResolvedProfile` struct, backend registry (register/get). |
| `backend_llamacpp.go` | llama-cpp backend: server arg assembly, HTTP model load/unload/status, model path resolution. Registers via `init()`. |
| `server.go` | Backend-agnostic process lifecycle: start (fork/detach), stop (SIGTERM/SIGKILL), state file read/write, PID tracking, health check polling, `LoadProfile`/`UnloadProfile` orchestration, log management. |
| `ui.go` | Low-level terminal operations: raw mode (via `golang.org/x/term`), ANSI escape codes, key reading, reusable `selectMenu()` component. |
| `menu.go` | Interactive menu logic for three states (stopped, running-with-model, running-no-model), simple fallback for non-terminals. |

### 5.3 Backend Interface

```go
type Backend interface {
    Name() string
    ServerBinary(cfg *Config) string
    BuildServerArgs(cfg *Config, params *ProfileParams) []string
    ResolveModel(cfg *Config, modelRef string) (string, error)
    SupportsHotSwap() bool
    LoadModel(addr string, profile *ResolvedProfile) error
    UnloadModel(addr string, modelID string) error
    ModelStatus(addr string) ([]ModelInfo, error)
}
```

Adding a new backend requires:
1. Create `backend_<name>.go` implementing the `Backend` interface.
2. Register via `init()` calling `RegisterBackend()`.
3. Add an entry to `servers:` in the config YAML.
4. Set `backend:` on relevant profiles (or set as `default_backend`).

### 5.4 External Dependencies

| Module | Purpose |
|---|---|
| `gopkg.in/yaml.v3` | YAML config parsing |
| `golang.org/x/term` | Raw terminal mode for arrow-key menu navigation |

Standard library only beyond that. No TUI framework; ANSI escape codes are used directly for colors and screen control.

## 6. Process and Model Management

### 6.1 Starting the Server (Router Mode)

1. Load and validate config.
2. Resolve the default backend.
3. Build server arguments from merged defaults (host, port, threads, gpu_layers, flash_attn, models_dir, etc.). No model flag — router mode.
4. Check state file — if a server is already running (PID alive) with the same backend, reuse it.
5. If a server is running with a different backend, stop it first.
6. Open the log file for stdout/stderr redirection.
7. Create `exec.Cmd` with `SysProcAttr{Setsid: true}` to detach the child process.
8. Call `cmd.Start()` (non-blocking).
9. Write state file with PID, backend, host, port, context_size, gpu_layers, and timestamp.
10. Wait 500ms to detect early exit (port conflict, binary not found, etc.).
11. Wait for `/health` endpoint to respond 200 (up to 15 seconds).
12. Print confirmation line.

### 6.2 Loading a Model

1. Ensure server is running (start if needed — see 6.1).
2. If the same model is already loaded (check state file), exit early.
3. If a different model is loaded, unload it first via `POST /models/unload`.
4. Warn if the profile's hardware params (context_size, gpu_layers) differ from the server's — these cannot be overridden per-model in router mode (llama.cpp issue [#20851](https://github.com/ggml-org/llama.cpp/issues/20851)).
5. Load the new model via `POST /models/load` with the model filename.
6. Update state file with active_profile and active_model.
7. Print confirmation.

### 6.3 Unloading a Model

1. Read state file; verify server is running.
2. Call `POST /models/unload` with the model filename.
3. Clear active_profile and active_model in state file.
4. Print confirmation. Server remains running.

### 6.4 Stopping the Server

1. Read state file; extract PID.
2. Verify the process is alive (`syscall.Kill(pid, 0)`).
3. Send `SIGTERM`.
4. Poll for process exit (100 ms intervals, up to 10 seconds).
5. If still alive after timeout, send `SIGKILL` and log a warning.
6. Remove state file.
7. Print confirmation.

### 6.5 Status Check

1. Read state file. If missing → report stopped, exit 1.
2. Verify PID is alive.
   - Alive → report running with model (if any), backend, PID, port, uptime. Exit 0.
   - Dead → report stale state ("server exited unexpectedly"), clean up state file. Exit 1.

### 6.6 Stale PID Handling

On every operation that reads the state file, the launcher verifies the recorded PID is alive. If the process has exited (crashed, killed externally), the state file is automatically removed and the launcher proceeds as if no server is running. A notice is printed to inform the user.

## 7. State File

### 7.1 Location

`~/.config/llama-launcher/state.json`

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
  "context_size": 4096,
  "gpu_layers": 99
}
```

`active_profile` and `active_model` are empty strings when the server is running but no model is loaded. `context_size` and `gpu_layers` record the server's global settings for hardware conflict detection.

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
- **Multiple simultaneous servers**: Track multiple state entries keyed by port. Requires rethinking the state file and the interactive menu.
- **Shell completions**: Generate bash/zsh/fish completions for subcommands and profile names.
- **Config reload**: A `reload` subcommand that restarts with the same profile using updated config values.
- **Additional backends**: LM Studio, Ollama, vLLM — each as a new `backend_<name>.go` file implementing the `Backend` interface.
- **Homebrew formula**: Package for `brew install llama-launcher`.
- **Launchd integration**: Generate a launchd plist for auto-start on login.
- **Health-based model status**: Query `GET /models` to verify actual loaded state rather than relying solely on state file.

## 14. Build and Installation

```bash
# Clone and build
cd llama-launcher
go mod tidy
go build -o llama-launcher .

# Install to PATH
cp llama-launcher /usr/local/bin/

# Or, for user-local install
cp llama-launcher ~/.local/bin/
```

The binary is statically linked (default for Go on macOS with CGO_ENABLED=0) and has no external dependencies at runtime.
