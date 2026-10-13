---
title: api_key_cmd re-runs on every menu probe tick
status: done
type: bug
priority: 2
labels: [from-todo, menu, security]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-api-key-cmd-reruns]
created: 2026-09-24T07:19:50Z
closed: 2026-10-13T10:02:43Z
reason: Resolved by plan 2026-10-13 - 00 - open-issues-sweep-plan (archived in c081cb7)
---

`Config.Reload()` runs on every menu probe tick, so with `api_key_cmd` set an entry's command re-runs every refresh interval while the menu is open — contradicting the "one subprocess per configured entry per run" wording; caching or a per-open resolve may be wanted (`internal/launcher/menu.go:874`).

Origin: deferred from the security-hardening and key-migration plan.
