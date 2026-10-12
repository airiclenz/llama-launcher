---
title: Propagate ErrUnsupported on the two Windows paths
status: done
type: bug
priority: 4
labels: [from-todo, windows]
aliases: [llama-launcher-windows-err-unsupported-lost]
created: 2026-09-24T07:19:46Z
closed: 2026-09-24T18:36:28Z
reason: "Resolved by plan 2026-09-24 - 03 - stop-auth-scope-and-windows-unsupported (item 2, archived b9f12f2): stop verbs wrap ErrUnsupported via requireProcessStop; connectExternalServer path (8e5ae5e) pinned by test"
---

`ErrUnsupported` does not survive two Windows paths (TDD §16.6): `connectExternalServer` replaces Ollama's `TryStart` refusal with its own "not reachable … start it manually" message, and the stop verbs never reach a seam function (`IsProcessAlive` is false there), ending at "PID could not be determined". Propagating the sentinel would be additive and would not change macOS behaviour — but there is no Windows host to prove it against.

Origin: loose end carried from the 2026-06-08 handoff.
