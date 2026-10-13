---
title: Config diff on profile switch
status: done
type: feature
priority: 3
labels: [from-todo, menu]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-profile-switch-config-diff]
created: 2026-09-24T07:19:43Z
closed: 2026-10-13T10:02:43Z
reason: Resolved by plan 2026-10-13 - 00 - open-issues-sweep-plan (archived in c081cb7)
---

- When switching models in the interactive menu, show what parameters will change
- Display differences between current and target profile (gpu_layers, context_size, backend, etc.)
- `formatProfileParams` already exists; this is mostly UI wiring to show a side-by-side or delta view
- Helps users confirm they're switching to the right configuration before waiting for the server restart
