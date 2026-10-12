---
title: A loading Splash server is invisible to Starting detection and stop
status: done
type: bug
priority: 2
labels: [cli]
aliases: [llama-launcher-splash-loading-invisible]
created: 2026-09-24T08:03:09Z
closed: 2026-09-24T09:39:16Z
reason: "Fixed in b2df60a: loading Splash found by session-leader process-table match (ADR-0015)"
---

Splash binds its port only after the model loads (manual probe during the splash-backend plan's item 6: connection refused on /ready, then /ready 200, never a 503), so the plan's "/ready 503 while loading" fact does not hold. A loading Splash is invisible to address-keyed Starting detection and Stop (ADR-0010): Splash.StartingUp (internal/launcher/backend_splash.go:65) never answers true, so discovery's Starting fallback (internal/launcher/discovery.go:138), the start path's still-starting refusal (internal/launcher/server.go:78) and the stop path's Starting check (internal/launcher/server.go:254) do not see it.

Origin: deferred from docs/plans/archived/2026-09-24 - 00 - splash-backend-plan.md (item 6).
