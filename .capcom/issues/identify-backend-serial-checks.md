---
title: Parallelise identifyBackend health checks
status: done
type: task
priority: 4
labels: [from-todo, performance]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-identify-backend-serial-checks]
created: 2026-09-24T07:19:45Z
closed: 2026-10-13T10:02:43Z
reason: Resolved by plan 2026-10-13 - 00 - open-issues-sweep-plan (archived in c081cb7)
---

`identifyBackend(addr)` health-checks the backends serially — parallelising would shave latency from `stop <host:port>`.

Origin: loose end carried from the 2026-06-08 handoff.
