---
title: "ADR-0007 why: a silent no-op runs stale config and a fingerprint auto-restart kills servers on stray edits, so both were rejected"
---

Two failure modes were considered and rejected:

- **Pure name-based with no notice.** Silently runs a stale configuration. A user who edits `context_size` and re-runs `llml load chat-qwen` would wrongly believe the new value took effect; the running server still has the old one. This is exactly the kind of trust-eroding silent staleness this ADR exists to prevent.
- **Fingerprint-based auto-restart.** Compute a hash of the resolved Profile (Model + parameters); if it differs, restart automatically. Solves staleness but creates a new surprise — a stray edit (a comment, a re-ordered key, a parameter the user did not realise was active) triggers a server restart and a Model reload, which on `llamacpp` is expensive. Activation should not silently kill a running server.

The chosen rule — load-time idempotency, plus a notice on parameter drift, plus an explicit `--restart` verb — keeps the predictable "load means no-op if it's already loaded" intuition while closing the silent-stale-config gap. The user is informed; the decision to act remains theirs.
