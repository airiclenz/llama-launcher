---
title: "ADR-0003 decision: activating a llamacpp Profile restarts llama-server with the Profile's Model and hardware flags, never /models/load"
---

When a user activates a Profile that targets `llamacpp`, llml stops the existing `llama-server` (if any) and starts a fresh one with that Profile's Model and hardware parameters baked into the start arguments (`--model`, `-c`, `-ngl`, ...). llml does *not* use `llama-server`'s `/models/load` HTTP API, even though that API exists and would allow a single `llama-server` to hold several Models simultaneously.
