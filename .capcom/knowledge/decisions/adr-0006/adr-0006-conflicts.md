---
title: "ADR-0006 conflict resolution: different addresses both run, the same Profile is a no-op, and a different Profile takes the slot"
---

- **Different `host:port`s** → two distinct instances, both run.
- **Same `host:port`, same Profile** → no-op (already running).
- **Same `host:port`, different Profile** → the new Profile takes the slot. For `llamacpp` this means stop-and-restart with new args; for Ollama / LM Studio it depends on `auto_unload`: `true` unloads the previous Model and loads the new one, `false` keeps both loaded on the same instance.
