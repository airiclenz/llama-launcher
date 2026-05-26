# `auto_unload` is one rule covering both same-server and cross-server cases

llml exposes a single `auto_unload` flag (default `true`). It means: when activating a Profile, unload any Model that is no longer in active use, on any LLM Server that is still running. "In active use" means the Model the activated Profile loads. Everything else gets unloaded.

## Why

It is tempting to split this into two flags — one for "unload the previous Model on the same server when swapping" and one for "unload Models on other servers when switching backends with `auto_stop_server: false`." They look like different situations, but they are the same operation against different target sets: an HTTP `UnloadModel` call on every Model that is no longer doing useful work. A single rule, stated once, captures both.

The two-flag version forces users to reason about an artificial distinction (same-server vs cross-server) and creates a confusing near-collision in naming (`auto_unload` vs `auto_unload_models`). The one-flag version states the actual rule and leaves no ambiguity about what the launcher will do.

## Consequences

- `auto_unload` interacts with `auto_stop_server` orthogonally. When `auto_stop_server: true`, other servers stop entirely and their Models go with them — `auto_unload` is only consulted for same-server Model swaps. When `auto_stop_server: false`, other servers stay running and `auto_unload: true` clears their Models; `auto_unload: false` leaves them loaded.
- For `llamacpp`, `auto_unload` is silently ignored: a single `llama-server` holds one Model, and a Profile switch is a server restart (see [ADR-0003](0003-llamacpp-restart-per-profile.md)).
- A future contributor who proposes a second flag for "the cross-server case" should be referred to this ADR before that flag is added.
