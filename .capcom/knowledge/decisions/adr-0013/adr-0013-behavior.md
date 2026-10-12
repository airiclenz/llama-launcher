---
title: "ADR-0013 behavior: single-host v1 with a priority queue, an interactive stickiness window and side-by-side under declared budgets"
---

- **v1 is single-host.** The Gateway activates Profiles on this machine via the library. Multi-host arrives later as **federation** — the routing table gains remote Gateway entries beside local Profiles — never as remote actuation (no SSH surface).
- **Priority.** Requests carry an `interactive` | `background` hint; absent means `interactive` (an unhinted client is assumed to be a human's tool). The queue orders by priority, then FIFO. apogee's daemon marks its Firings `background`.
- **Eviction: interactive stickiness window.** A Profile that served an interactive request within the last N minutes (config) cannot be displaced by a background request — the background request waits until the window lapses or capacity allows side-by-side. A lapsed window means the human walked away; the background work proceeds. Background-vs-background displacement is plain demand order.
- **Side-by-side under declared budgets.** Profiles may declare a memory footprint and the Gateway config a global budget; activation is side-by-side when the arithmetic fits (concurrent instances are native — ADR-0006), displacement otherwise. An undeclared Profile is treated as consuming the whole budget, which degrades to today's one-at-a-time behavior. No estimation code: auto-detected footprints may later replace declarations without changing the policy, and a wrong guess OOMing a serving host is the failure this avoids.
