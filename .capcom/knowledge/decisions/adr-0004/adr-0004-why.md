---
title: "ADR-0004 why: same-server and cross-server unloading are one operation on different targets, so two flags would draw an artificial line"
---

It is tempting to split this into two flags — one for "unload the previous Model on the same server when swapping" and one for "unload Models on other servers when switching backends with `auto_stop_server: false`." They look like different situations, but they are the same operation against different target sets: an HTTP `UnloadModel` call on every Model that is no longer doing useful work. A single rule, stated once, captures both.

The two-flag version forces users to reason about an artificial distinction (same-server vs cross-server) and creates a confusing near-collision in naming (`auto_unload` vs `auto_unload_models`). The one-flag version states the actual rule and leaves no ambiguity about what the launcher will do.
