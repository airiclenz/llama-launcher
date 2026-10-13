---
title: Add test coverage for list --json
status: open
type: task
priority: 3
labels: [from-todo, tests]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-list-json-test-gap]
created: 2026-09-24T07:19:44Z
---

`list --json` has no direct test coverage (`status --json` is covered in `cli_test.go`; a round-trip marshal/unmarshal test would be cheap).

Origin: loose end carried from the 2026-06-08 handoff.
