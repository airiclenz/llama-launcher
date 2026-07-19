# A Starting LLM Server is visible and stoppable; only `--restart` displaces it

A Starting instance — process up, address bound, health check not yet passing (`llama-server` answers `/health` with 503 for the whole Model load) — is a first-class running instance:

- **Discovery reports it.** When a backend's health check fails, discovery falls back to the `StartupProber` probe (`StartingUp`); a positive answer yields an instance in the Starting state. `status`, the TUI, and stop/unload target resolution therefore see it (displayed as "starting…") instead of pretending nothing is there.
- **An explicit stop stops it.** ADR-0001's "stop means stop" extends into the Starting window, and `unload` on a managed LLM Server reduces to the same stop (ADR-0003/0004). Identification stays mandatory (ADR-0006): the stop path first runs the healthy-identification pass, then a `StartingUp` pass; an address where neither identifies anything still refuses with "no server running".
- **The `auto_stop_server` sweep includes it.** Activating a Profile with `auto_stop_server: true` stops a Starting instance at another address like any other instance.
- **Activation does not displace it — except with `--restart`.** A plain `load` (same or a different Profile) targeting a Starting address refuses, and the refusal points at `llml stop` or `--restart` instead of `kill <PID>`. With `--restart`, the Starting occupant is treated like a healthy one: stopped, then replaced.

## Why

Previously a Starting server was invisible and untouchable: every stop path identifies the occupant via a *passing* health check, so a 503 yielded "no server running" and the error text told the user to `kill <PID>` by hand. That guidance (`c183c0d`) was deliberate — but it conflated two different intents. On a **start timeout**, leaving the server alone is right: killing it would throw away a legitimately slow Model load (a 30–70 GB GGUF on a cold disk), and that protection stays. An **explicit user `stop`** is the opposite intent — the user is saying "kill it", and refusing only outsources the exact signal the launcher exists to own.

Three subordinate choices, made deliberately:

- **Identification over signal-whatever-listens.** Skipping identification and signalling the lsof PID would be simpler, but abandons the foreign-occupant refusal that protects a non-LLM process bound at a configured address. Instead identification is *extended*: the `StartingUp` pass accepts a weaker discrimination signal (a bare 503 on `/health`, versus the `{"status":"ok"}` body the healthy check requires). A foreign server answering 503 at an address the config claims for `llamacpp` would be misidentified as a Starting instance — accepted as a marginal risk confined to addresses the user already assigned to the launcher.
- **`--restart`-only displacement.** ADR-0006's "the new Profile takes the slot" is deliberately narrowed for Starting occupants: a mistyped or absent-minded `load` must not silently kill a 40-minute Model load, so plain activation refuses and the one-command override is the ADR-0007 force verb, whose documented meaning is already "stop, then re-activate".
- **The sweep is not exempted.** `auto_stop_server: true` is a standing commitment to "one server at a time", and a Starting server already consumes the memory that setting protects. Exempting it would also preserve today's silent hole in which a loading server escapes the sweep and two servers end up fighting. Users who want in-flight loads protected set `auto_stop_server: false`.

The stop verification gap closes in the same pass: "stopped" previously meant "health check fails", which a *survived* Starting server also satisfies (it still answers 503). Stopped now means neither healthy nor Starting.

## Consequences

- `RunningInstance` carries a Starting state; discovery probes `StartingUp` only when the health check fails (one extra HTTP GET per unresponsive `StartupProber` address). A Starting instance reports no Model list — the server cannot answer — so its active-Model/Profile fields stay empty.
- `identifyBackend` gains the second pass; `ErrNotRunning` is returned only when neither pass identifies. Stop verification treats a 503 answer as still-reachable.
- `stillStartingUpErr` and `startupTimeoutErr` guidance changes from `kill <PID>` to `llama-launcher stop …` (and `--restart` where displacement is what the user wants).
- Only `llamacpp` implements `StartupProber` today. Ollama and LM Studio have no Starting window (API-loaded Models; the server is healthy throughout), so nothing changes for them until a backend with a comparable window implements the interface.
- The TUI, `status`, and `list --json` surface the Starting state; the menu's refresh signature includes it so the Starting→healthy transition is noticed.
- TDD §6.2/§6.5 and the CHANGELOG record the behaviour change.
