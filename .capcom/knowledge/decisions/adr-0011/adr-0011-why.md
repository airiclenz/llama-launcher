---
title: "ADR-0011 why: apogee imports the launcher as a library, and a facade keeps the public surface opt-in per symbol"
---

The first client is decided: apogee (the owner's coding agent) integrates the launcher as an
imported library — apogee is the launcher's client, not its port, and apogee grows no process
manager of its own. The division of labour is fixed on apogee's side: the launcher **actuates**
(start/stop/load/unload), apogee's heartbeat **observes** (a profile load is completed by the
next beat binding what it finds). Remote lifecycle control (agent in a container, servers on the
host) stays with the MCP adapter (ADR-0008), configured as an ordinary MCP entry — the two
compose, they do not compete. All launcher code lives in `internal/launcher/`, which other
modules cannot import by Go rule, so *some* export had to exist; the forks were its shape.

Structural shapes considered:

- **A facade package wrapping internal (chosen).** The public surface is opt-in per symbol, the
  implementation home stays single and untouched, and the pattern is proven — apogee's own
  ADR 0010 does exactly this (root aliases over internal packages).
- **Moving `internal/launcher` to a public package wholesale (rejected).** Every currently
  exported symbol — menu helpers, terminal UI, memstats, log cleanup, the backend registry —
  would freeze into public API under SemVer. The opposite of a deliberate surface.
- **Making the repository root the library package (rejected).** Cosmetically the cleanest
  import path, but it moves the CLI entry point: Makefile, Homebrew formula build path, and
  ldflags targets all churn for no functional gain.

Surface width: the minimal verb set is exactly what the client integration needs (config load,
profile listing, discovery, the lifecycle verbs). Widening later is a cheap minor bump;
narrowing is a v2. Deliberately excluded: the `LLMServer` interface and `RegisterLLMServer`
(third-party backends are not a known need, and the interface is the core's most
change-sensitive seam), `TailLog`/log cleanup, memstats/template engine,
`GenerateExampleConfig`, and the live-params query (`QueryLiveParams`) — the client's heartbeat
already observes live server state itself; exporting the launcher's observe half would
duplicate it across the decided actuate/observe boundary.
