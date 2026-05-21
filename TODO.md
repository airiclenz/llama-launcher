
# TODO

## Feature ideas

- [x] `config validate` subcommand
  - Dedicated command to verify config YAML is correct after editing
  - Currently validation only happens implicitly on load
  - Useful since "Edit config" opens the file externally with no feedback loop
  - Reports all validation errors at once (missing servers, unknown backends, deprecated fields, missing models)

- [x] `--profile` flag on `start` subcommand
  - `llama-launcher start --profile <name>` as an alias for `load`
  - More intuitive for users who think in terms of "start the server with this model"
  - Keeps plain `start` behavior unchanged (starts default server without a model)

- [ ] Shell completions (bash/zsh/fish)
  - Tab-complete profile names for `load`, `unload`, and the new `start --profile`
  - Tab-complete backend names for `stop` and `logs`
  - Tab-complete subcommand names
  - Data is already available from config; generate completion scripts via a `completions` subcommand

- [ ] `config init` / `config reset` subcommand
  - Regenerate the example config on demand (currently only auto-generated on first run)
  - `config init` creates if missing, `config init --force` overwrites existing
  - Helpful when the user has mangled their config and wants a fresh starting point

- [ ] Log cleanup / rotation
  - Log files accumulate in `~/.config/llama-launcher/logs/` with no cleanup
  - Option A: `logs clean` subcommand that deletes logs older than N days (default 7)
  - Option B: `log_retention` config option (e.g. `log_retention: 7d`) with automatic cleanup on server start
  - Should report how many files and how much space was freed

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
