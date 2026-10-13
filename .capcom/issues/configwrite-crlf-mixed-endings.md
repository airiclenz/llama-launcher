---
title: Config rewrite gives mixed line endings on CRLF files
status: open
type: bug
priority: 3
labels: [config, from-todo]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-configwrite-crlf-mixed-endings]
created: 2026-09-24T07:19:52Z
---

On a CRLF config the rewritten/inserted key-source line lands with a bare LF, giving mixed line endings (same as apogee's original) (`internal/launcher/configwrite.go:471`).

Origin: deferred from the security-hardening and key-migration plan.
