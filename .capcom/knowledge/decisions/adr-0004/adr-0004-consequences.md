---
title: "ADR-0004 consequences: auto_unload works orthogonally to auto_stop_server, is ignored on llamacpp, and gets no second flag"
---

- `auto_unload` interacts with `auto_stop_server` orthogonally. When `auto_stop_server: true`, other servers stop entirely and their Models go with them — `auto_unload` is only consulted for same-server Model swaps. When `auto_stop_server: false`, other servers stay running and `auto_unload: true` clears their Models; `auto_unload: false` leaves them loaded.
- For `llamacpp`, `auto_unload` is silently ignored: a single `llama-server` holds one Model, and a Profile switch is a server restart (see ADR-0003).
- A future contributor who proposes a second flag for "the cross-server case" should be referred to this ADR before that flag is added.
