---
title: Add test coverage for list --json
status: done
type: task
priority: 3
labels: [from-todo, tests]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-list-json-test-gap]
created: 2026-09-24T07:19:44Z
closed: 2026-10-13T10:02:43Z
reason: Resolved by plan 2026-10-13 - 00 - open-issues-sweep-plan (archived in c081cb7)
---

`list --json` has no direct test coverage (`status --json` is covered in `cli_test.go`; a round-trip marshal/unmarshal test would be cheap).

Origin: loose end carried from the 2026-06-08 handoff.
