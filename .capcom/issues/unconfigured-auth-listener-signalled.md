---
title: Stop(addr) signals a 401/403 listener at an unconfigured address
status: done
type: bug
priority: 3
labels: [security]
aliases: [llama-launcher-unconfigured-auth-listener-signalled]
created: 2026-09-24T17:17:07Z
closed: 2026-09-24T18:36:27Z
reason: "Resolved by plan 2026-09-24 - 03 - stop-auth-scope-and-windows-unsupported (item 1, archived b9f12f2): 401/403 stop carve-out now requires a configured address"
---

identifyBackend's third pass (internal/launcher/server.go:302-303) accepts ErrAuthFailed from any registered backend's health check at whatever addr it is handed, with no check that addr is a configured backend address. StopInstance (internal/launcher/server.go:239) then signals the listening PID, so the facade Stop(addr) (launcher/launcher.go:141, internal/launcher/server.go:1311) and any raw-addr path through it SIGTERM any listener answering 401/403 on an LLM API path, even at an address no config names.

The ADR-0010 amendment (docs/adr/0010-starting-instances-are-visible-and-stoppable.md:33) scopes the carve-out to 'a 401/403 answer on an LLM API path at a configured address'; the code does not enforce that scope. The CLI and MCP stop paths are bounded by discovery (resolveTargetInstance, internal/launcher/cli.go:334), which probes configured addresses only.

Origin: deferred from plan 2026-09-24 - 02 (code-audit fixes), item 6 (commit 4e9691c).
