---
title: Config rewrite gives mixed line endings on CRLF files
status: done
type: bug
priority: 3
labels: [config, from-todo]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-configwrite-crlf-mixed-endings]
created: 2026-09-24T07:19:52Z
closed: 2026-10-13T10:02:43Z
reason: Resolved by plan 2026-10-13 - 00 - open-issues-sweep-plan (archived in c081cb7)
---

On a CRLF config the rewritten/inserted key-source line lands with a bare LF, giving mixed line endings (same as apogee's original) (`internal/launcher/configwrite.go:471`).

Origin: deferred from the security-hardening and key-migration plan.
