# llama-launcher

A terminal tool for managing [llama.cpp](https://github.com/ggerganov/llama.cpp) model servers through named configuration profiles. Define your models and parameters once in a YAML file, then load and switch between them with a single command or an interactive TUI.

llama-launcher starts the server as a detached background process, loads/unloads models via the server's HTTP API, then exits immediately -- zero resident memory between invocations.

<p align="center">
  <img src="media/screen_1.png" alt="llama-launcher interactive menu" width="600">
</p>

## Quick start

```bash
# Build and install
make install

# First run generates an example config
llama-launcher
# => Created example config at: ~/.config/llama-launcher/config.yaml

# Edit the config with your model paths, then run again
llama-launcher
```

## Configuration

The config lives at `~/.config/llama-launcher/config.yaml` (override with `--config` or `LLAMA_LAUNCHER_CONFIG`).

```yaml
servers:
  llamacpp: /usr/local/bin/llama-server

default_backend: llamacpp
models_dir: ~/Models
log_dir: ~/.config/llama-launcher/logs

# Keep the interactive menu open after each action (default: true = close)
auto_close: false

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
    model: qwen-27b.gguf
    context_size: 8192

  gemma-4b:
    description: "Gemma-4 E4B IT-Q4-K-M"
    model: gemma-4b.gguf
```

Parameters merge in three tiers: **profile > defaults > built-in fallbacks**. All numeric and boolean params use pointer types so "not set" is distinct from zero.

See the [technical design doc](llama-launcher.TDD.md) for full schema details and behavior.

## Usage

### Interactive mode

Run without arguments to get the TUI menu:

```
llama-launcher
```

The menu adapts to three states:

- **Stopped** -- select a profile to start the server and load a model
- **Running with model** -- switch models, show config, stop, tail logs, edit config
- **Running (no model)** -- load a profile, stop, tail logs, edit config

### CLI commands

```bash
llama-launcher load <profile>   # Start server (if needed) and load model
llama-launcher unload            # Stop server and unload model
llama-launcher start             # Start server without loading a model
llama-launcher stop              # Stop the server
llama-launcher status            # Show server and model status
llama-launcher list              # List available profiles
llama-launcher logs [--follow]   # Tail the server log
```

### Options

```
--config <path>    Use a custom config file instead of the default
```

## Building

Requires Go 1.22+.

```bash
make build      # Build the binary
make install    # Build + install to ~/.local/bin
make clean      # Remove the binary
```

The version is read from the `VERSION` file and injected at build time.

## Architecture

All code lives in `internal/launcher/`. The design is backend-agnostic -- llama.cpp is the current backend, but the `Backend` interface supports adding others.

Key paths:

| Path | Purpose |
|------|---------|
| `~/.config/llama-launcher/config.yaml` | Configuration |
| `~/.config/llama-launcher/state.json` | Runtime state (PID, active model) |
| `~/.config/llama-launcher/logs/` | Server log files |

## License

See [LICENSE](LICENSE) for details.
