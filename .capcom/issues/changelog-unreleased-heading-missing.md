---
title: "Add ## [Unreleased] heading to CHANGELOG.md and fold in pending entries"
status: done
type: chore
priority: 1
labels: [docs, from-todo, release]
aliases: [llama-launcher-changelog-unreleased-heading-missing]
created: 2026-09-24T07:19:46Z
closed: 2026-09-24T08:04:32Z
reason: "## Unreleased heading added with pending entries folded in; resolved by splash-backend plan (docs/plans/archived/2026-09-24 - 00 - splash-backend-plan.md), 4d8f7fa"
---

`CHANGELOG.md` has no `## [Unreleased]` heading, so the security-hardening and key-migration changes carry no changelog entry yet; the release step must fold them in under the new version heading (`CHANGELOG.md:3`).

Origin: deferred from the security-hardening and key-migration plan.

## Log

- 2026-09-24 beads: the heading is `## Unreleased` (CHANGELOG headings carry no brackets: `## 1.7.0`). The splash-backend plan's closeout adds it (docs/plans/2026-09-24 - 00 - splash-backend-plan.md, Standing requirements) and folds in llama-launcher-changelog-extra-args-claim and llama-launcher-changelog-log-redaction-entry.
