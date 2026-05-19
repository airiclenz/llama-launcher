# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
make build          # builds ./llama-launcher binary
make install        # builds + copies to ~/.local/bin, adds to PATH if needed
make clean          # removes binary

go test ./...       # run all tests
go test ./internal/launcher/ -run TestMergeParams  # run a single test
go vet ./...        # static analysis
```

## Architecture

Go CLI tool that manages llama.cpp (and future LLM server backends) through named YAML configuration profiles. Starts the server as a detached background process, loads/unloads models via the server's HTTP API (router mode), then exits immediately — zero resident memory between invocations.

All application code lives in `internal/launcher/`. The `main.go` entry point delegates to `launcher.Run()`.

### Key components

- **cli.go** — `Run()` entry point, subcommand dispatch (`load`, `unload`, `start`, `stop`, `status`, `list`, `logs`), and first-run config generation. No arguments → interactive menu.
- **config.go** — YAML config loading, three-tier parameter merge (profile → defaults → fallback). All numeric/bool params are pointer types to distinguish "not set" from zero.
- **backend.go** — `Backend` interface + global registry. New backends: implement the interface in `backend_<name>.go`, register via `init()`.
- **backend_llamacpp.go** — llama.cpp backend: builds CLI flags, resolves model file paths, registered as `"llamacpp"`.
- **server.go** — Backend-agnostic process lifecycle: fork/detach via `Setsid`, PID tracking, state file (JSON) with atomic writes, health polling, SIGTERM→SIGKILL escalation.
- **menu.go** — Interactive menus for three states (stopped / running with model / running idle), plus non-terminal simple fallbacks.
- **ui.go** — Raw terminal mode via `golang.org/x/term`, ANSI escape rendering, arrow-key `selectMenu()` component.

### State & config paths

- Config: `~/.config/llama-launcher/config.yaml` (override with `--config` or `LLAMA_LAUNCHER_CONFIG`)
- State: `~/.config/llama-launcher/state.json` (tracks PID, backend, active model)
- Logs: `~/.config/llama-launcher/logs/<backend>-<timestamp>.log`

### Exit codes

0 = success, 1 = not running / expected condition, 2 = config error, 3 = process/API error.

## Coding standards

Follow `skills/coding-standards/SKILL.md` when writing or modifying code. Read the base references and the Go-specific extensions before making changes.

## After changing code

1. Update `CLAUDE.md` if the change affects architecture, components, commands, or conventions documented here.
2. Update `llama-launcher.TDD.md` if the change affects behavior, configuration schema, subcommands, error handling, or any other aspect covered by the design spec.
3. Run `make install` to build and install the updated binary.

## Design document

`llama-launcher.TDD.md` is the full technical design spec. Consult it for detailed behavior (router mode, model load/unload flow, hardware param conflicts, stale PID handling).
