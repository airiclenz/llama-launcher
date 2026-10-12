---
title: "ADR-0010 decision: a Starting instance is discovered, stopped by an explicit stop and the sweep, and displaced only by --restart"
---

A Starting instance — process up, address bound, health check not yet passing (`llama-server` answers `/health` with 503 for the whole Model load) — is a first-class running instance:

- **Discovery reports it.** When a backend's health check fails, discovery falls back to the `StartupProber` probe (`StartingUp`); a positive answer yields an instance in the Starting state. `status`, the TUI, and stop/unload target resolution therefore see it (displayed as "starting…") instead of pretending nothing is there.
- **An explicit stop stops it.** ADR-0001's "stop means stop" extends into the Starting window, and `unload` on a managed LLM Server reduces to the same stop (ADR-0003/0004). Identification stays mandatory (ADR-0006): the stop path first runs the healthy-identification pass, then a `StartingUp` pass; an address where neither identifies anything still refuses with "no server running".
- **The `auto_stop_server` sweep includes it.** Activating a Profile with `auto_stop_server: true` stops a Starting instance at another address like any other instance.
- **Activation does not displace it — except with `--restart`.** A plain `load` (same or a different Profile) targeting a Starting address refuses, and the refusal points at `llml stop` or `--restart` instead of `kill <PID>`. With `--restart`, the Starting occupant is treated like a healthy one: stopped, then replaced.
