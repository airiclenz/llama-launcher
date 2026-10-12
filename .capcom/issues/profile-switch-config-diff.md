---
title: Config diff on profile switch
status: open
type: feature
priority: 3
labels: [from-todo, menu]
aliases: [llama-launcher-profile-switch-config-diff]
created: 2026-09-24T07:19:43Z
---

- When switching models in the interactive menu, show what parameters will change
- Display differences between current and target profile (gpu_layers, context_size, backend, etc.)
- `formatProfileParams` already exists; this is mostly UI wiring to show a side-by-side or delta view
- Helps users confirm they're switching to the right configuration before waiting for the server restart
