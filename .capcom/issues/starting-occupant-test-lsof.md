---
title: TestStopServerAt_StartingOccupant hard-fails without lsof
status: done
type: bug
priority: 3
labels: [from-todo, tests]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-starting-occupant-test-lsof]
created: 2026-09-24T07:19:47Z
closed: 2026-10-13T10:02:43Z
reason: Resolved by plan 2026-10-13 - 00 - open-issues-sweep-plan (archived in c081cb7)
---

`TestStopServerAt_StartingOccupant` depends on a working `nc` listener and fails in containers without it — pre-existing and environment-dependent. It skips when `nc` is missing but hard-fails when `findListeningPID` never matches, which is what a container without `lsof` produces (`internal/launcher/server_test.go:1580`).

Origin: deferred from the security-hardening and key-migration plan.
