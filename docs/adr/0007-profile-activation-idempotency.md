# Profile activation is idempotent by name within an address slot

When `llml load <profile>` is invoked against an address (`host:port`) that already has a Profile of the same name active, the launcher does nothing by default — even if the Profile's resolved parameters in the config have changed since activation. If the resolved parameters differ from what is actually running, the launcher prints a notice naming the divergent fields and points the user to `llml load <profile> --restart`, which forces re-activation with the current parameters.

## Why

Two failure modes were considered and rejected:

- **Pure name-based with no notice.** Silently runs a stale configuration. A user who edits `context_size` and re-runs `llml load chat-qwen` would wrongly believe the new value took effect; the running server still has the old one. This is exactly the kind of trust-eroding silent staleness this ADR exists to prevent.
- **Fingerprint-based auto-restart.** Compute a hash of the resolved Profile (Model + parameters); if it differs, restart automatically. Solves staleness but creates a new surprise — a stray edit (a comment, a re-ordered key, a parameter the user did not realise was active) triggers a server restart and a Model reload, which on `llamacpp` is expensive. Activation should not silently kill a running server.

The chosen rule — name-based idempotency, plus a notice on parameter drift, plus an explicit `--restart` verb — keeps the predictable "load means no-op if it's already loaded" intuition while closing the silent-stale-config gap. The user is informed; the decision to act remains theirs.

## Consequences

- The state file's `active_profile` and the resolved parameters at activation time are both recorded. The drift check compares the live resolved Profile against the recorded parameters.
- `--restart` (or `--force`) on `llml load` forces a stop-then-start cycle even when the same Profile name is already active. For API-loaded LLM Servers, this is an unload-then-load; for `llamacpp`, a server restart.
- The "same Profile" check is **per-address**: activating Profile `chat-qwen` on `:8080` does not collide with the same Profile activated on `:8081` — those are two distinct instances by [ADR-0006](0006-instances-are-keyed-by-address.md).
- The drift notice is informational, not a warning — it does not change exit codes or block scripting use cases.
