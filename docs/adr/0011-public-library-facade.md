# A curated public facade makes the launcher importable as a library

The launcher becomes importable as a Go library through a new public package, `launcher/` at the
repository root (import path `github.com/airiclenz/llama-launcher/launcher`). The package is a
deliberately minimal facade over the unchanged `internal/launcher/` implementation: type aliases
for the domain types plus thin wrapper functions for the verbs, nothing more.

The exported surface is the minimal verb set a client needs to drive the launcher's actuation:

- **Config + Profiles** — `LoadConfig(path, notice)`, `DefaultConfigDir()`, `DefaultConfigPath()`,
  the `Config` alias (whose methods include `ResolveProfile`, `ProfileNames`, `IsServerEnabled`),
  `Profile`, `ProfileParams`, `ResolvedProfile`, `ErrConfigNotFound`.
- **Discovery / status** — `DiscoverRunningInstances(cfg)`, `RunningInstance` (with `Addr`,
  `Uptime`, the ADR-0010 `Starting` flag).
- **Lifecycle verbs** — `LoadProfile(cfg, profile, restart, progress, notice)`, `Stop(addr)`,
  `Unload(backend, addr)`, `StopResult`, `ProgressFunc`, `NoticeFunc`, `ErrNotRunning`.

Four contract decisions shape the facade:

1. **Notices are callbacks, never stream writes.** A library must not write to its host's
   stderr — the first client is an alt-screen TUI. The two internal print sites (config warnings
   in `LoadConfig`, the ADR-0007 drift notice in the activation orchestration) become a threaded
   notice sink (`NoticeFunc`, the same shape as the existing `ProgressFunc`). The CLI entry
   points bind the stderr printers and stay byte-identical; the facade hands the sink to the
   client (nil discards).
2. **Verbs block; cancellation is the domain's own verb.** No `context.Context` in v1. The
   facade documents worst-case durations (activation: up to ~30 s of health wait plus stop
   escalation) and clients call from a goroutine. Cancelling an in-flight load is `Stop(addr)`
   on the Starting instance — ADR-0010 made that a first-class operation, so a Go ctx would
   duplicate it while forcing a rewrite of the activation internals.
3. **One Config per process, stated honestly.** Backends live in a process-global registry and
   `LoadConfig` pushes per-server API keys onto them (`applyAPIKeys`); the last load wins. The
   facade documents this instead of pretending instance scoping it does not have. Read verbs
   are safe to call concurrently; lifecycle verbs against the same address must be serialized
   by the caller.
4. **The documented surface is the contract.** A type alias unavoidably exposes every exported
   method of the aliased type (e.g. `Config`'s TUI-oriented accessors). The compatibility
   promise covers the symbols this ADR and the package documentation name; alias-reachable
   extras are not part of the contract.

The facade ships as a normal minor release (`v1.6.0`, tagged and pushed so a client's `go.mod`
can require it; the Homebrew formula bumps as on every release).

## Why

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

## Consequences

- `internal/launcher/` gains exactly two seams, both behaviour-preserving: `LoadConfig` and the
  activation orchestration accept a notice sink, with the CLI binding stderr printers whose
  output is byte-identical to today's. Everything else the facade re-exports untouched.
- The facade is the SemVer surface. From `v1.6.0` on, changing a documented facade symbol is a
  breaking change; adding one is a minor bump. `internal/launcher/` remains free to refactor.
- The MCP adapter (ADR-0008) is unaffected — it shells out to the CLI and does not import
  `internal/launcher`. In-process library and remote MCP control are complementary access
  paths to the same core.
- The launcher stays agent-free: the dependency direction is strictly client → launcher. The
  facade adds no dependencies to `go.mod`.
- Doors left open, all additive: ctx-taking verb variants if a client ever needs Go-level
  cancellation; an instance-scoped handle if a multi-config client ever appears (requires
  de-globalising the backend registry first); exporting the network of narrower helpers
  (`EnsureServer`, `StartServer`, `WaitForHealth`, `FillRuntimeDetails`) if a client asks.
- `Config.Reload` remains CLI-flavoured (it prints warnings to stderr via `LoadConfig`'s
  binding); library clients re-read config by calling the facade's `LoadConfig` again. Recorded
  in the package documentation.
