---
title: Test keystore entry names containing a single quote
status: open
type: task
priority: 3
labels: [from-todo, keystore, tests]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-keystore-quote-name-test]
created: 2026-09-24T07:19:50Z
---

No test covers a keystore entry name containing a single quote — the `shellWord` quoting that `ReadCmd` relies on (`internal/keystore/keystore.go:271`; the `ReadCmd` spelling tests are at `internal/keystore/keystore_test.go:631`).

Origin: deferred from the security-hardening and key-migration plan.
