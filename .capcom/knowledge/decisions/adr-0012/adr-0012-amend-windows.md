---
title: "ADR-0012 amendment of 2026-09-24 (windows stop verbs): the stop verbs now carry ErrUnsupported on windows through requireProcessStop"
---

> **Amended 2026-09-24** (stop-auth-scope and windows-unsupported plan). The windows bullet
> above said **the stop verbs** never produced the sentinel: "`IsProcessAlive` is false on
> windows, so the escalation is never entered, and `Stop`/`Unload` end at the generic 'PID
> could not be determined' … it is a known gap in this decision's precision." That gap is
> closed, and its register entry with it. The process seam gains a sixth function,
> `requireProcessStop()` (nil on unix, a refusal wrapping `ErrUnsupported` on windows), and
> `stopServerAt` wraps its refusal when a stop leaves the server reachable, so `Stop`, an
> `Unload` that reduces to one on a managed backend, and `LoadProfile`'s stop step all carry
> the sentinel on windows. Nothing changes on darwin or linux, where the guard permits and
> every stop outcome and message stays byte-identical. The bullet now describes all four
> refusal paths as preserving the sentinel.
