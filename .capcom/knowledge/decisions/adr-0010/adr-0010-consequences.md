---
title: "ADR-0010 consequences: RunningInstance gains a Starting state, identifyBackend a second pass, and the error guidance points at stop"
---

- `RunningInstance` carries a Starting state; discovery probes `StartingUp` only when the health check fails (one extra HTTP GET per unresponsive `StartupProber` address). A Starting instance reports no Model list — the server cannot answer — so its active-Model/Profile fields stay empty.
- `identifyBackend` gains the second pass; `ErrNotRunning` is returned only when neither pass identifies. Stop verification treats a 503 answer as still-reachable.
- `stillStartingUpErr` and `startupTimeoutErr` guidance changes from `kill <PID>` to `llama-launcher stop …` (and `--restart` where displacement is what the user wants).
- Only `llamacpp` implements `StartupProber` today. Ollama and LM Studio have no Starting window (API-loaded Models; the server is healthy throughout), so nothing changes for them until a backend with a comparable window implements the interface.
- The TUI, `status`, and `status --json` surface the Starting state; the menu's refresh signature includes it so the Starting→healthy transition is noticed.
- TDD §6.2/§6.5 and the CHANGELOG record the behaviour change.
