# llama-launcher

A terminal tool for managing local LLM servers through named configuration profiles. Supports [llama.cpp](https://github.com/ggerganov/llama.cpp), [Ollama](https://ollama.com), and [LM Studio](https://lmstudio.ai) as backends. Define your models and parameters once in a YAML file, then load and switch between them with a single command or an interactive TUI.

`llama-launcher` is a process manager, not a request router: it starts and stops LLM servers and tells them which model to load. Clients talk to each server directly via its native address. The launcher exits after dispatching work, consuming zero resident memory while the server runs. Multiple instances of any supported server may run concurrently as long as each binds a distinct `host:port`.

See [CONTEXT.md](CONTEXT.md) for the project's domain language and [docs/adr/](docs/adr/) for the architectural decisions behind the design.

<p align="center">
  <img src="media/screen_1.png" alt="llama-launcher interactive menu" width="600">
</p>

<p align="center">
  <img src="media/screen_2.png" alt="llama-launcher interactive menu" width="825">
</p>

## Install

### Homebrew (macOS)

```bash
brew tap airiclenz/tap
brew install llama-launcher
```

### From source

Requires Go 1.26+.

```bash
make install
```

## Quick start

```bash
# First run generates an example config
llama-launcher
# => Created example config at: ~/.config/llama-launcher/config.yaml

# Edit the config with your model paths, then run again
llama-launcher
```

## Configuration

The config lives at `~/.config/llama-launcher/config.yaml` (override with `--config` or `LLAMA_LAUNCHER_CONFIG`).

```yaml
# Enable the servers available on your system (true/false).
# Disabled servers are hidden from status and menus.
servers:
  llamacpp: true
  ollama:   true
  lmstudio: false

models_dir: ~/Models
log_dir: ~/.config/llama-launcher/logs

# Automatically delete log files older than N days on server start
# log_retention: 7

# Keep the interactive menu open after each action (default: close)
auto_close: false

# Allow multiple servers to run simultaneously (default: true = stop old)
# auto_stop_server: false

# Keep multiple models loaded on the same server (default: true = unload old)
# auto_unload: false

# Center the UI in the terminal (default: false)
display_centered: true

defaults:
  gpu_layers: 99
  threads: 8
  context_size: 4096
  flash_attn: true

profiles:
  qwen-27b:
    description: "Qwen 3.6 27B MTP Q4-K-S"
    server: llamacpp
    model: qwen-27b.gguf
    is_favourite: true
    context_size: 8192

  gemma-4b:
    description: "Gemma-4 E4B IT-Q4-K-M"
    server: llamacpp
    model: gemma-4b.gguf

  llama-8b-ollama:
    description: "Llama 3.1 8B via Ollama"
    server: ollama
    model: llama3.1:8b
```

Parameters merge in three tiers: **profile > defaults > built-in fallbacks**. All numeric and boolean params use pointer types so "not set" is distinct from zero.

Set `is_favourite: true` on a profile to pin it to the top of menus and `list` output. Profiles are sorted by favourite status first, then alphabetically by server, then alphabetically by name.

See the [technical design doc](llama-launcher.TDD.md) for full schema details and behavior.

### Backends

| Backend | Default address | Model reference |
|---------|-----------------|-----------------|
| `llamacpp` | `127.0.0.1:8080` | File path (relative to `models_dir` or absolute) |
| `ollama` | `localhost:11434` | Ollama model name (e.g. `llama3.1:8b`) |
| `lmstudio` | `localhost:1234` | LM Studio model key (e.g. `lmstudio-community/meta-llama-3.1-8b-instruct`) |

For each backend, the launcher knows how to start the server (fork-and-detach for `llamacpp`; `ollama serve` for Ollama; `lms server start` for LM Studio) and how to stop it. `stop` is unconditional — the launcher does not distinguish servers it started from servers that were already running (see [ADR-0001](docs/adr/0001-stop-is-unconditional.md)).

## Usage

### Interactive mode

Run without arguments to get the TUI menu:

```
llama-launcher
```

The menu adapts to three states:

- **Stopped** -- select a profile to start the server and load a model
- **Running with model** -- switch models (hidden when only one profile is configured), unload model, stop server, show log, show model config, edit config
- **Running (no model)** -- load a profile, stop server, show log, edit config

When more than one instance is running, the relevant actions (stop, unload, show log) present an instance picker disambiguated by `host:port`.

### CLI commands

```bash
llama-launcher load <profile> [--restart]   # Activate a profile (no-op if already active; --restart forces)
llama-launcher unload [profile]             # Unload model from the matching instance
llama-launcher start [--profile p]          # Start server (optionally with a profile)
llama-launcher stop [target]                # Stop a server (target = host:port or backend name)
llama-launcher status [--json]              # Show all running instances (--json for structured output)
llama-launcher list [--json]                # List available profiles (--json for structured output)
llama-launcher logs [target] [-f]           # Tail an instance's log
llama-launcher logs clean [--days N|--all]  # Remove old log files
```

### Options

```
--config <path>    Use a custom config file instead of the default
```

## Building

Requires Go 1.26+.

```bash
make build      # Build the binary
make install    # Build + install to ~/.local/bin
make clean      # Remove the binary
```

The version is read from the `VERSION` file and injected at build time.

## Architecture

All code lives in `internal/launcher/`. Three LLM Servers are implemented behind a common `LLMServer` interface: llama.cpp, Ollama, and LM Studio. The architectural decisions are written down as [ADRs](docs/adr/); the domain language is in [CONTEXT.md](CONTEXT.md); the technical design doc is [llama-launcher.TDD.md](llama-launcher.TDD.md).

Key paths:

| Path | Purpose |
|------|---------|
| `~/.config/llama-launcher/config.yaml` | Configuration |
| `~/.config/llama-launcher/state-{backend}-{port}.json` | Per-instance runtime state (PID, active profile/model, resolved-params snapshot) |
| `~/.config/llama-launcher/logs/` | Server log files |

## License

See [LICENSE](LICENSE.md) for details.
