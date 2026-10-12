---
title: "ADR-0001 consequences: the managed field goes, and a user who wants a server kept alive sets auto_stop_server false"
---

- The `managed` field in state files becomes redundant and is removed.
- Per-instance PID tracking for external LLM Servers (Ollama auto-started, etc.) is no longer needed for liveness — health checks already cover that.
- Users who run an LLM Server's GUI alongside llml and don't want llml to stop it must set `auto_stop_server: false`.
- The "keep the other server running but free its memory" case is covered by the existing `auto_unload` flag — see ADR-0004. No separate cross-server flag exists.
