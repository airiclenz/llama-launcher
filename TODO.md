
# TODO

## Feature ideas

- [ ] Shell completions (bash/zsh/fish)
  - Tab-complete profile names for `load`, `unload`, and the new `start --profile`
  - Tab-complete backend names for `stop` and `logs`
  - Tab-complete subcommand names
  - Data is already available from config; generate completion scripts via a `completions` subcommand

- [ ] `config init` / `config reset` subcommand
  - Regenerate the example config on demand (currently only auto-generated on first run)
  - `config init` creates if missing, `config init --force` overwrites existing
  - Helpful when the user has mangled their config and wants a fresh starting point

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
