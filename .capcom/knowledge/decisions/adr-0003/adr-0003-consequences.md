---
title: "ADR-0003 consequences: LoadModel and UnloadModel are no-ops on llamacpp, and auto_unload is ignored there"
---

- `Backend.LoadModel` and `Backend.UnloadModel` are no-ops on `llamacpp` (Model swap goes through stop-and-restart, not the API).
- `auto_unload` is meaningful only for LLM Servers that load Models via API (Ollama, LM Studio). For `llamacpp`, restart implies unload; the flag is silently ignored on `llamacpp` Profiles.
- The hardware-param-conflict warning that existed for the never-shipped multi-Model path is removed — under restart-per-Profile no conflict is possible.
- LM Studio and Ollama may still hold multiple Models at once (their multi-Model implementations work). This ADR is `llamacpp`-specific.
