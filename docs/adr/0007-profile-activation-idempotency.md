# Profile activation is idempotent by name within an address slot

When `llml load <profile>` is invoked against an address (`host:port`) that already has the requested Profile's Model loaded, the launcher does nothing by default — even if the Profile's resolved parameters in the config have changed since activation. If the resolved parameters differ from what is actually running, the launcher prints a notice naming the divergent fields and points the user to `llml load <profile> --restart`, which forces re-activation with the current parameters.

## Why

Two failure modes were considered and rejected:

- **Pure name-based with no notice.** Silently runs a stale configuration. A user who edits `context_size` and re-runs `llml load chat-qwen` would wrongly believe the new value took effect; the running server still has the old one. This is exactly the kind of trust-eroding silent staleness this ADR exists to prevent.
- **Fingerprint-based auto-restart.** Compute a hash of the resolved Profile (Model + parameters); if it differs, restart automatically. Solves staleness but creates a new surprise — a stray edit (a comment, a re-ordered key, a parameter the user did not realise was active) triggers a server restart and a Model reload, which on `llamacpp` is expensive. Activation should not silently kill a running server.

The chosen rule — load-time idempotency, plus a notice on parameter drift, plus an explicit `--restart` verb — keeps the predictable "load means no-op if it's already loaded" intuition while closing the silent-stale-config gap. The user is informed; the decision to act remains theirs.

## Consequences

- Idempotency is derived **live**, not from persisted state. The launcher probes the target address, asks the LLM Server which Model is loaded, and compares to the resolved Profile's Model. There is no state file (see [removed: state file](../../CHANGELOG.md)).
- The drift check compares the running server's parameters against the freshly resolved Profile — but only the parameters the server actually reports live. For `llamacpp`, these are read from `GET /props` and the diffable subset is exactly `context_size` (from `default_generation_settings.n_ctx`, reported per slot and scaled by `total_slots` back to the Profile's total value) and `parallel` (from `total_slots`). Sampling settings (temperature, top_k, top_p, min_p, repeat_penalty) are deliberately not diffed even though they are passed as launch flags: they only set the server's request defaults, which any API request overrides per call, and `/props` reports floats that need not round-trip the configured values exactly — diffing them would raise spurious drift notices. For Ollama and LM Studio, no equivalent endpoint exists and the drift check is therefore narrower still: model-name match alone is sufficient for the idempotency no-op and no per-parameter drift notice is emitted.
- `--restart` (or `--force`) on `llml load` forces a stop-then-start cycle even when the same Model is already active. For API-loaded LLM Servers, this is an unload-then-load; for `llamacpp`, a server restart.
- The "same Profile" check is **per-address**: activating Profile `chat-qwen` on `:8080` does not collide with the same Profile activated on `:8081` — those are two distinct instances by [ADR-0006](0006-instances-are-keyed-by-address.md).
- The drift notice is informational, not a warning — it does not change exit codes or block scripting use cases.
