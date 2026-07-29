
# TODO

## Feature ideas

- [ ] Shell completions (bash/zsh/fish)
  - Tab-complete profile names for `load`, `unload`, and the new `start --profile`
  - Tab-complete backend names for `stop` and `logs`
  - Tab-complete subcommand names
  - Data is already available from config; generate completion scripts via a `completions` subcommand

- [ ] Config diff on profile switch
  - When switching models in the interactive menu, show what parameters will change
  - Display differences between current and target profile (gpu_layers, context_size, backend, etc.)
  - `formatProfileParams` already exists; this is mostly UI wiring to show a side-by-side or delta view
  - Helps users confirm they're switching to the right configuration before waiting for the server restart

## Loose ends

Carried from the 2026-06-08 handoff when it was archived; small and independent.

- [ ] README "Requirements" doesn't mention runtime host binaries — `lsof` in particular (used to find the listening PID for stop), documented only in the 1.3.0 CHANGELOG entry
- [ ] `list --json` has no direct test coverage (`status --json` is covered in `cli_test.go`; a round-trip marshal/unmarshal test would be cheap)
- [ ] `identifyBackend(addr)` health-checks the backends serially — parallelising would shave latency from `stop <host:port>`
- [ ] `ErrUnsupported` does not survive two Windows paths (TDD §16.6): `connectExternalServer` replaces Ollama's `TryStart` refusal with its own "not reachable … start it manually" message, and the stop verbs never reach a seam function (`IsProcessAlive` is false there), ending at "PID could not be determined". Propagating the sentinel would be additive and would not change macOS behaviour — but there is no Windows host to prove it against
