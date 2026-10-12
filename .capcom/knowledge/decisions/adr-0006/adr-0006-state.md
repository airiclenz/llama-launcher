---
title: "ADR-0006 how state is stored: one state-{backend}-{port}.json per instance, and the managed field goes in the same pass"
---

One state record per running instance. The file naming convention is `state-{backend}-{port}.json` (host is omitted when `127.0.0.1`; included as `state-{backend}-{host}-{port}.json` otherwise). Examples:

- `state-llamacpp-8080.json`
- `state-llamacpp-8081.json`
- `state-ollama-11434.json`
- `state-lmstudio-1234.json`

`ReadAllStates()` becomes a glob over `state-*.json`. `ReadBackendState(backend)` is replaced by `ReadInstanceState(addr)` or `ReadInstancesForBackend(backend)` (returns a slice).

The `managed` field is removed in the same pass (per ADR-0001) — both this ADR and ADR-0001 touch state file schema, and combining the rewrite is cheaper than two passes. Per-instance PID tracking continues for any LLM Server type that supports it (used for log file association and PID display in `status`), but is not used to decide whether `stop` should kill the process — stop is unconditional per ADR-0001.
