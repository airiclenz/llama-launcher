---
title: "CHANGELOG: add the log-redaction Security entry"
status: done
type: chore
priority: 1
labels: [docs, from-todo, release, security]
deps: [changelog-unreleased-heading-missing]
aliases: [llama-launcher-changelog-log-redaction-entry]
created: 2026-09-24T07:19:53Z
closed: 2026-09-24T08:04:34Z
reason: "Log-redaction Security entry added under ## Unreleased; resolved by splash-backend plan (docs/plans/archived/2026-09-24 - 00 - splash-backend-plan.md), 4d8f7fa"
---

The log-redaction change has no changelog entry because `CHANGELOG.md` has no `## [Unreleased]` heading (`CHANGELOG.md:3`); the release step must add it under Security:

- **Log output masks API keys** — `llama-launcher logs`, the menu's *Show log*, the start-crash log tail, and the MCP `tail_log` tool now replace the backend's configured key and any `--api-key` value with `[redacted]`. Unconditional defense in depth: no tested llama-server build echoes its keys, but a future build or wrapper script that does can no longer leak them through a log view. Log files on disk are unchanged.

Origin: deferred from the api-key residue and doc-fixes plan.
