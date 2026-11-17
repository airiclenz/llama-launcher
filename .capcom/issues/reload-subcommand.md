---
title: Reload subcommand to apply config to a running instance
status: done
type: feature
priority: 3
labels: [cli]
plan: 2026-11-17 - 00 - p3-features-sweep-plan
created: 2026-10-13T11:47:04Z
closed: 2026-11-17T07:43:04Z
reason: resolved by plan "2026-11-17 - 00 - p3-features-sweep-plan" (archived in 2eb4448), item 4
---

The interactive menu already reloads config automatically, but the CLI has no way to apply edited config values to a running instance short of a manual stop and start.

## Acceptance

- A `reload` subcommand restarts the matching instance with the same Profile, using the current config values.
- With no matching running instance it fails with a clear message and a non-zero exit.
- Shell completions, README usage and the TDD CLI section cover it.
