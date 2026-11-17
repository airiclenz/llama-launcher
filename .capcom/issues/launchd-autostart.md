---
title: Generate a launchd plist for auto-start on login
status: done
type: feature
priority: 3
labels: [cli]
plan: 2026-11-17 - 01 - vllm-mouse-autostart-plan
created: 2026-10-13T11:47:04Z
closed: 2026-11-17T09:22:50Z
reason: Resolved by plan "2026-11-17 - 01 - vllm-mouse-autostart-plan" (archived at cbeed77), items 9-10
---

There is no supported way to start the launcher's server at login on macOS.

## Acceptance

- The launcher can generate a launchd plist (and say where to install it) that starts the chosen Profile at login.
- The generated plist sets a `PATH` that includes the wrapper's directory (see README's launchd note).
- README documents generating, installing and removing it.
