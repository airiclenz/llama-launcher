---
title: "ADR-0010 why: an explicit stop means kill it, identification is extended rather than skipped, and only --restart may displace a load"
---

Previously a Starting server was invisible and untouchable: every stop path identifies the occupant via a *passing* health check, so a 503 yielded "no server running" and the error text told the user to `kill <PID>` by hand. That guidance (`c183c0d`) was deliberate — but it conflated two different intents. On a **start timeout**, leaving the server alone is right: killing it would throw away a legitimately slow Model load (a 30–70 GB GGUF on a cold disk), and that protection stays. An **explicit user `stop`** is the opposite intent — the user is saying "kill it", and refusing only outsources the exact signal the launcher exists to own.

Three subordinate choices, made deliberately:

- **Identification over signal-whatever-listens.** Skipping identification and signalling the lsof PID would be simpler, but abandons the foreign-occupant refusal that protects a non-LLM process bound at a configured address. Instead identification is *extended*: the `StartingUp` pass accepts a weaker discrimination signal (a bare 503 on `/health`, versus the `{"status":"ok"}` body the healthy check requires). A foreign server answering 503 at an address the config claims for `llamacpp` would be misidentified as a Starting instance — accepted as a marginal risk confined to addresses the user already assigned to the launcher.
- **`--restart`-only displacement.** ADR-0006's "the new Profile takes the slot" is deliberately narrowed for Starting occupants: a mistyped or absent-minded `load` must not silently kill a 40-minute Model load, so plain activation refuses and the one-command override is the ADR-0007 force verb, whose documented meaning is already "stop, then re-activate".
- **The sweep is not exempted.** `auto_stop_server: true` is a standing commitment to "one server at a time", and a Starting server already consumes the memory that setting protects. Exempting it would also preserve today's silent hole in which a loading server escapes the sweep and two servers end up fighting. Users who want in-flight loads protected set `auto_stop_server: false`.

The stop verification gap closes in the same pass: "stopped" previously meant "health check fails", which a *survived* Starting server also satisfies (it still answers 503). Stopped now means neither healthy nor Starting.
