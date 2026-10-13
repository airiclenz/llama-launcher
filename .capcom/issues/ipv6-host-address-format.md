---
title: An IPv6 host builds an unparseable address (host:port without brackets)
status: done
type: bug
priority: 3
labels: [config]
plan: 2026-10-13 - 00 - open-issues-sweep-plan.md
aliases: [llama-launcher-ipv6-host-address-format]
created: 2026-09-24T09:41:49Z
closed: 2026-10-13T10:02:43Z
reason: Resolved by plan 2026-10-13 - 00 - open-issues-sweep-plan (archived in c081cb7)
---

Every address is built as fmt.Sprintf("%s:%d", host, port) — internal/launcher/discovery.go (RunningInstance.Addr, probeInstance), config.go, server.go (start path, still-starting refusal, LoadProfile target), cli.go. An IPv6 host such as "::" or "::1" yields ":::8000" / "::1:8000", which net.SplitHostPort rejects and an http URL cannot carry, so every probe, lsof lookup and stop against an IPv6-configured instance misbehaves. Fix: net.JoinHostPort at every producer. Found by the regression check of docs/plans/2026-09-24 - 01 - splash-wildcard-host-probe-plan.md, which scopes its wildcard fix to 0.0.0.0 and an empty host because of this.
