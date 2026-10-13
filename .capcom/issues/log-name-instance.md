---
title: Log file names do not show the instance port
status: open
type: feature
priority: 3
labels: [cli]
created: 2026-10-13T11:47:04Z
---

Log files are named `{backend}-{timestamp}.log`, so a log's instance (port) cannot be read from its name. Collisions are already ruled out — the stamp carries milliseconds and the file is created exclusively with a retry (TDD §9) — so this is about readability only.

## Acceptance

- New log files are named `{backend}-{port}-{timestamp}.log`.
- `logs` and log retention still find and prune files under both the old and new names.
- TDD §9 and README describe the new naming.
