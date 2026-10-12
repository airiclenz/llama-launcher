---
title: "ADR-0005 consequences: validation warns on the fallback, single-server users are unaffected, and only server leaves defaults"
---

- `Config.Validate` emits a warning when a Profile lacks `server:` and `defaults.server` is being used as a fallback, naming the Profile.
- Single-LLM-Server users are unaffected: auto-detection from §4.6 covers them.
- `defaults.server` is removed in a future release; the warning is the deprecation notice.
- Operational parameters (`gpu_layers`, `threads`, `context_size`, etc.) remain in `defaults` — those *are* parameters with sensible defaults. Only `server` is removed from `defaults`.
