
# TODO

## Architecture refactor (ADRs 0001–0007)

- [ ] Implement the fit-gap from [docs/handoffs/20260526-fit-gap-adrs-vs-code.md](docs/handoffs/20260526-fit-gap-adrs-vs-code.md)
  - [x] Phase 1: documentation alignment (TDD, README, CHANGELOG)
  - [x] Phase 2: cross-server `auto_unload` (ADR-0004)
  - [x] Phase 3: `defaults.server` soft-deprecation (ADR-0005)
  - Phase 4: state schema + multi-instance — combined `Managed`-removal and addr-keyed instances (ADR-0001 + ADR-0006)
  - Phase 5: idempotency drift notice + `--restart` flag (ADR-0007)
  - Phase 6 (optional): rename `Backend` → `LLMServer` interface

## Feature ideas

- [ ] Add `sort_alphabetically` to the config (default: `true`).
  - When `true`, profiles are sorted alphabetically in menus and `list` output (current behaviour).
  - When `false`, profiles appear in the order they are defined in the config file.
  - `is_favourite: true` Profiles always float to the top regardless of this setting (favourite > sort mode).

- [ ] Shell completions (bash/zsh/fish)
  - Tab-complete profile names for `load`, `unload`, and the new `start --profile`
  - Tab-complete backend names for `stop` and `logs`
  - Tab-complete subcommand names
  - Data is already available from config; generate completion scripts via a `completions` subcommand

- [ ] `status --json` and `list --json` output
  - Structured JSON output for scripting and integration with tools like `jq`
  - `status --json`: backend name, running state, address, active profile/model, PID, uptime
  - `list --json`: profile name, description, backend, model path, key parameters
  - Keeps the default human-readable output unchanged

- [ ] Config diff on profile switch
  - When switching models in the interactive menu, show what parameters will change
  - Display differences between current and target profile (gpu_layers, context_size, backend, etc.)
  - `formatProfileParams` already exists; this is mostly UI wiring to show a side-by-side or delta view
  - Helps users confirm they're switching to the right configuration before waiting for the server restart
