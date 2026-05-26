# Stop is unconditional, regardless of who started the LLM Server

`llama-launcher` does not distinguish between LLM Servers it started and LLM Servers that were already running. Any `stop` (whether triggered by the user via `llml stop` or implicitly via `auto_stop_server: true` when switching backends) terminates the server using whichever mechanism that LLM Server type supports (signal to PID for `llamacpp`, `lms server stop` for `lmstudio`, `ollama stop` for `ollama`).

## Why

The earlier design carried a `managed` boolean on each state file: `true` if llml had started the process, `false` if llml had merely connected to an already-running server. `managed: false` made `stop` a soft disconnect — the state file was removed but the server was left alive. This forced a two-axis mental model on every operation ("did we start it?" × "what does stop mean here?") and leaked into the user-facing language as "managed vs external" backends.

Removing the distinction collapses the model to one rule: **stop means stop**. The user does not need to remember whether they launched LM Studio via the GUI or via llml — the result of `llml stop` is the same. This is also the only behaviour that makes `auto_stop_server` actually do its job (prevent two LLM Servers from fighting over the same port); a soft disconnect would leave the port held.

## Consequences

- The `managed` field in state files becomes redundant and is removed.
- Per-instance PID tracking for external LLM Servers (Ollama auto-started, etc.) is no longer needed for liveness — health checks already cover that.
- Users who run an LLM Server's GUI alongside llml and don't want llml to stop it must set `auto_stop_server: false`.
- The "keep the other server running but free its memory" case is covered by the existing `auto_unload` flag — see [ADR-0004](0004-auto-unload-is-one-rule.md). No separate cross-server flag exists.
