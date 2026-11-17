---
title: Per-Profile log retention
status: done
type: feature
priority: 3
labels: [config]
plan: 2026-11-17 - 00 - p3-features-sweep-plan
created: 2026-10-13T11:47:04Z
closed: 2026-11-17T07:43:04Z
reason: resolved by plan "2026-11-17 - 00 - p3-features-sweep-plan" (archived in 2eb4448), item 3
---

`log_retention` is global, so a chatty debug Profile cannot keep more history without inflating storage for every Profile.

## Acceptance

- A Profile may set its own `log_retention`, overriding the global value for its logs only.
- Profiles without it keep using the global value.
- TDD §4 and §9 and README's config reference describe the override.
