---
title: Per-Profile log retention
status: open
type: feature
priority: 3
labels: [config]
created: 2026-10-13T11:47:04Z
---

`log_retention` is global, so a chatty debug Profile cannot keep more history without inflating storage for every Profile.

## Acceptance

- A Profile may set its own `log_retention`, overriding the global value for its logs only.
- Profiles without it keep using the global value.
- TDD §4 and §9 and README's config reference describe the override.
