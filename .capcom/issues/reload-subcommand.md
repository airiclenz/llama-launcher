---
title: Reload subcommand to apply config to a running instance
status: open
type: feature
priority: 3
labels: [cli]
created: 2026-10-13T11:47:04Z
---

The interactive menu already reloads config automatically, but the CLI has no way to apply edited config values to a running instance short of a manual stop and start.

## Acceptance

- A `reload` subcommand restarts the matching instance with the same Profile, using the current config values.
- With no matching running instance it fails with a clear message and a non-zero exit.
- Shell completions, README usage and the TDD CLI section cover it.
