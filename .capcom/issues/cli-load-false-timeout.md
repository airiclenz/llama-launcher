---
title: CLI load reports a startup timeout for a server that boots healthy
status: open
type: bug
priority: 2
labels: [cli]
plan: 2026-11-02 - 00 - cli-load-keeps-waiting-plan
created: 2026-11-02T06:34:51Z
---

`llama-launcher load <profile>` on a managed LLM Server (llamacpp, Splash) prints `Error: server at <addr> did not become healthy: no startup progress for 30s …` and exits 3 when the server's start goes quiet for `startup_stall_timeout` (default 30 s) — its log stops growing and its `StartupProber` does not answer yet (a large GGUF paging in from a cold disk, an MLX model loading before Splash binds its port). The server was left running and turns healthy shortly after, so the load actually succeeded.

The interactive menu already handles this: on a managed `startupTimeout` it opens the still-loading popup (`keepWaitingForLoad` / `waitStillLoading` in `internal/launcher/menu.go`) and keeps polling until the server is healthy or gone. The CLI has no such step; the 2026-09-24 progress-aware-startup-wait plan put "CLI `load`/`start` output and exit codes (still exit 3 on timeout)" out of scope.

The same false failure reaches `start --profile` (routed to `load`), the menu run without a terminal (`routeLoadError` returns the error when `!isTerminal()`), and the MCP adapter's `load_profile` tool, which shells out to `llama-launcher load`.

## Why

Scripts, agents (MCP load_profile) and users read exit 3 as a failed load and retry or give up while the model is in fact loading; a retry then hits the still-Starting refusal. The TUI and the CLI disagree about the same event.

## Acceptance

- A managed `load` whose server stalls past `startup_stall_timeout` but turns healthy before `startup_max_wait` (measured from the start of the load) prints the normal success lines and exits 0.
- While it keeps waiting the CLI says so in its progress output (still loading, elapsed time), and Ctrl+C leaves the server running.
- A server that exits or vanishes during the extra wait, or one still not healthy at `startup_max_wait`, still exits 3 with the timeout text (plus a line saying the server is gone, when it is).
- The non-terminal menu load and MCP `load_profile` get the same outcome; the library `LoadProfile` contract (returns `ErrStartupTimeout` at the stall) is unchanged.
- TDD, README and CHANGELOG describe the CLI behaviour.
