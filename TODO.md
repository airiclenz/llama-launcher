
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

## Deferred from the security-hardening and key-migration plan

- [ ] `CHANGELOG.md` has no `## [Unreleased]` heading, so the security-hardening and key-migration changes carry no changelog entry yet; the release step must fold them in under the new version heading (`CHANGELOG.md:3`)
- [ ] `TestStopServerAt_StartingOccupant` depends on a working `nc` listener and fails in containers without it — pre-existing and environment-dependent. It skips when `nc` is missing but hard-fails when `findListeningPID` never matches, which is what a container without `lsof` produces (`internal/launcher/server_test.go:1580`)
- [ ] llama.cpp appends `--api-key` values rather than replacing, so an `extra_args` override likely makes both keys valid rather than the override strictly winning; confirm against `common/arg.cpp` before the release. The comment asserting that the override wins is at `internal/launcher/backend_llamacpp.go:153`
  - 2026-09-24: settled empirically on llama-server b10851 (commit 67672dc5b) by `TestLlamaServerAPIKey` (`internal/launcher/server_integration_test.go`, `make test-integration`): llama.cpp **appends** — with `LLAMA_API_KEY` from the launcher and `--api-key` from `extra_args`, **both keys are valid** (each answers 200 on `/v1/models`, a wrong key 401). An override therefore never revokes the configured key; this is the fact the unconditional log redaction guards against. The same run found neither key echoed into llama-server's own log file. The `backend_llamacpp.go` comment, README, and TDD §4.2 are corrected; the released 1.7.0 CHANGELOG entry still carries the old claim.
- [ ] TDD §5.2's Source Files table gains no rows for `internal/keystore/*` (`llama-launcher.TDD.md:444`)
- [ ] The same TDD file map omits `cli.go` and `frame.go` (pre-existing gap) (`llama-launcher.TDD.md:444`)
- [ ] No test covers a keystore entry name containing a single quote — the `shellWord` quoting that `ReadCmd` relies on (`internal/keystore/keystore.go:271`; the `ReadCmd` spelling tests are at `internal/keystore/keystore_test.go:631`)
- [ ] `Config.Reload()` runs on every menu probe tick, so with `api_key_cmd` set an entry's command re-runs every refresh interval while the menu is open — contradicting the "one subprocess per configured entry per run" wording; caching or a per-open resolve may be wanted (`internal/launcher/menu.go:874`)
- [ ] README's per-backend `api_key` effect table documents only the literal key; whether `api_key_cmd` deserves a row there is untouched (`README.md:448`)
- [ ] On a CRLF config the rewritten/inserted key-source line lands with a bare LF, giving mixed line endings (same as apogee's original) (`internal/launcher/configwrite.go:471`)
