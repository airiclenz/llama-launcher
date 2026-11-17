---
title: Log file names do not show the instance port
status: done
type: feature
priority: 3
labels: [cli]
plan: 2026-11-17 - 00 - p3-features-sweep-plan
created: 2026-10-13T11:47:04Z
closed: 2026-11-17T07:43:03Z
reason: resolved by plan "2026-11-17 - 00 - p3-features-sweep-plan" (archived in 2eb4448), item 2
---

Log files are named `{backend}-{timestamp}.log`, so a log's instance (port) cannot be read from its name. Collisions are already ruled out — the stamp carries milliseconds and the file is created exclusively with a retry (TDD §9) — so this is about readability only.

## Acceptance

- New log files are named `{backend}-{port}-{timestamp}.log`.
- `logs` and log retention still find and prune files under both the old and new names.
- TDD §9 and README describe the new naming.
