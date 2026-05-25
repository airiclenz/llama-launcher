# llama-launcher

A terminal tool for managing local LLM servers through named configuration profiles. Supports [llama.cpp](https://github.com/ggerganov/llama.cpp), [Ollama](https://ollama.com), and [LM Studio](https://lmstudio.ai) as backends. Define your models and parameters once in a YAML file, then load and switch between them with a single command or an interactive TUI.

Managed backends (llama.cpp) are started as detached background processes. External backends (Ollama, LM Studio) connect to running instances or auto-start them. The launcher is built for ultra low memory usage and even supports immediate exit after selecting a model.

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
  server: llamacpp
  gpu_layers: 99
  threads: 8
  context_size: 4096
  flash_attn: true

profiles:
  qwen-27b:
    description: "Qwen 3.6 27B MTP Q4-K-S"
    model: qwen-27b.gguf
    is_favourite: true
    context_size: 8192

  gemma-4b:
    description: "Gemma-4 E4B IT-Q4-K-M"
    model: gemma-4b.gguf

  llama-8b-ollama:
    description: "Llama 3.1 8B via Ollama"
    server: ollama
    model: llama3.1:8b
```

Parameters merge in three tiers: **profile > defaults > built-in fallbacks**. All numeric and boolean params use pointer types so "not set" is distinct from zero.

Set `is_favourite: true` on a profile to pin it to the top of menus and `list` output. Profiles are sorted by favourite status first, then by server (default backend first), then alphabetically by name.

See the [technical design doc](llama-launcher.TDD.md) for full schema details and behavior.

### Backends

| Backend | Type | Default address | Model reference |
|---------|------|-----------------|-----------------|
| `llamacpp` | Managed | `127.0.0.1:8080` | File path (relative to `models_dir` or absolute) |
| `ollama` | External | `localhost:11434` | Ollama model name (e.g. `llama3.1:8b`) |
| `lmstudio` | External | `localhost:1234` | LM Studio model key (e.g. `lmstudio-community/meta-llama-3.1-8b-instruct`) |

**Managed** backends are forked and owned by the launcher. **External** backends connect to a running instance or auto-start one; the server stays running after the launcher exits.

## Usage

### Interactive mode

Run without arguments to get the TUI menu:

```
llama-launcher
```

The menu adapts to three states:

- **Stopped** -- select a profile to start the server and load a model
- **Running with model** -- switch models (hidden when only one profile is configured), unload model, stop/disconnect, show log, show model config, edit config
- **Running (no model)** -- load a profile, stop/disconnect, show log, edit config

### CLI commands

```bash
llama-launcher load <profile>        # Start server (if needed) and load model
llama-launcher unload [profile]      # Unload model (stops server for managed backends)
llama-launcher start [--profile p]   # Start server (optionally with a profile)
llama-launcher stop [backend]        # Stop the server
llama-launcher status                # Show all server and model status
llama-launcher list                  # List available profiles
llama-launcher logs [backend] [-f]          # Tail the server log
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

All code lives in `internal/launcher/`. Three backends are implemented behind a common `Backend` interface: llama.cpp (managed), Ollama (external), and LM Studio (external).

Key paths:

| Path | Purpose |
|------|---------|
| `~/.config/llama-launcher/config.yaml` | Configuration |
| `~/.config/llama-launcher/state-*.json` | Per-backend runtime state (PID, active model) |
| `~/.config/llama-launcher/logs/` | Server log files |

## License

See [LICENSE](LICENSE.md) for details.
