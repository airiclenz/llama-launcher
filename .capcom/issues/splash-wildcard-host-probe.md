---
title: A Splash server bound to 0.0.0.0 is invisible to the launcher (Host 403)
status: done
type: bug
priority: 1
labels: [cli]
plan: 2026-09-24 - 01 - splash-wildcard-host-probe-plan
aliases: [llama-launcher-splash-wildcard-host-probe]
created: 2026-09-24T09:30:33Z
closed: 2026-09-24T10:09:49Z
reason: "Resolved by plan 2026-09-24 - 01 - splash-wildcard-host-probe-plan (archived aa8bcfb): Splash probes dial loopback for a 0.0.0.0/empty host, LAN hostname auto-added as --allowed-host; IPv6 wildcard tracked in llama-launcher-ipv6-host-address-format"
---

Splash validates the HTTP Host header and never accepts a wildcard: server/server.py FrontendServer builds allowed_hosts from --allowed-host, the bind address, localhost, 127.0.0.1 and ::1, and drops 0.0.0.0 and :: (so --allowed-host 0.0.0.0 cannot fix it). The launcher probes an instance at its configured host:port, so with the stock host: "0.0.0.0" every Splash probe sends Host: 0.0.0.0:<port> and gets 403 Forbidden (Server: Splash).

Observed 2026-09-24 on the live config (defaults host 0.0.0.0, port 1111, profile Qwen3.8-27B-Splash, repo build at 3e07fd6+): curl http://127.0.0.1:1111/ready -> 200, http://0.0.0.0:1111/ready -> 403. Consequences:
- status / the TUI report "No servers running" while Splash is Ready.
- load fails with "server at 0.0.0.0:1111 did not become healthy within 30s" although it is up.
- stop splash cannot identify it; the server had to be stopped with kill -TERM <pid>.
- auto_stop_server cannot sweep it, so a llama.cpp load onto the shared port hits the port-conflict refusal.

Affected: Splash.HealthCheck, Splash.StartingUp and Splash.ListRunningModels (openAIModelList) in internal/launcher/backend_splash.go, called with the configured addr by discovery, the start-path health wait and identifyBackend. llama-server, Ollama and LM Studio don't check Host, so only Splash is affected.

Suggested fix: Splash's HTTP probes dial the loopback address when the configured host is a wildcard (0.0.0.0 -> 127.0.0.1, :: -> [::1]), keeping the instance keyed by its configured address (ADR-0006). Test: httptest server that 403s unless Host is loopback, probed as 0.0.0.0:<port>. The integration test missed this because it binds 127.0.0.1; add a 0.0.0.0 case.

Separate but related: a LAN hostname (e.g. Apollo-II.local) also gets 403 unless passed via extra_args --allowed-host. The live profile now does this; README / example-config Splash notes should mention it.

Related: llama-launcher-splash-loading-invisible (loading window; a different cause).
