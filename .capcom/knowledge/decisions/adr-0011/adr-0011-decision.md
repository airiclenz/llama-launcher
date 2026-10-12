---
title: "ADR-0011 decision: a minimal public facade over internal/launcher, with callback notices, blocking verbs and one Config per process"
---

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
   by the caller — except that a `Stop` while a `LoadProfile` on the same address is still
   waiting for its server is the supported cancellation (amended 2026-09-24, below).
4. **The documented surface is the contract.** A type alias unavoidably exposes every exported
   method of the aliased type (e.g. `Config`'s TUI-oriented accessors). The compatibility
   promise covers the symbols this ADR and the package documentation name; alias-reachable
   extras are not part of the contract.

The facade ships as a normal minor release (`v1.6.0`, tagged and pushed so a client's `go.mod`
can require it; the Homebrew formula bumps as on every release).
