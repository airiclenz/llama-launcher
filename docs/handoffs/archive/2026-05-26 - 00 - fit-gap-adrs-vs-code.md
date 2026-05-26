# Fit-gap: ADRs 0001–0007 vs. current code

Snapshot date: 2026-05-26. Source of truth for "should-be-this": [CONTEXT.md](../../CONTEXT.md) and [docs/adr/](../adr/) (ADRs 0001–0007). Source of truth for "is-this": `internal/launcher/` as of commit `88f7f84`.

This document lists each gap as a discrete change with file/line citations, groups them into implementation phases, and recommends an order. Each phase is independently shippable.

---

## Gap-by-gap analysis

### ADR-0001 — Stop is unconditional; drop `managed`

| What needs to change | Where | Current behaviour |
|---|---|---|
| Remove `Managed bool` field | `server.go:29` (`ServerState`) | Field present, drives all stop/display branching |
| Stop always goes through one path | `server.go:171-241` (`StopBackendServer`, `stopManagedServer`, `disconnectExternalServer`) | Branches on `state.Managed`; "external" path is a soft disconnect (process left alive) |
| Liveness check uses HealthCheck universally | `server.go:50-59` (`IsServerAlive`) | Uses `IsProcessAlive(PID)` for `Managed`, HealthCheck for external |
| State write omits Managed | `server.go:108`, `server.go:156` | Both write `Managed: true/false` |
| CLI output drops "Connected to / Disconnected from" framing | `cli.go:119-123`, `cli.go:286-291`, `cli.go:511-515` | Branches on `Managed` for output strings |
| Menu output drops Managed branches | `menu.go:41-45`, `menu.go:260-264` | Same |
| Status display always shows PID if PID > 0 | `cli.go:396-399` | Currently gated on `Managed` |

PID is kept as a property: if we know it, we show it. The stop logic is: call the backend's stop hook (`TryStop` for Ollama/LM Studio's CLI commands; for llamacpp, signal PID and reap). The launcher does not refuse to act based on who started it.

### ADR-0002 — Not a router

Code is already compliant. llml exposes no HTTP server; `Backend.LoadModel`/`UnloadModel` on `llamacpp` are no-ops (`backend_llamacpp.go:43-44`); orchestration for llamacpp goes through `loadProfileManaged` (stop+restart), never the API. **No code change needed.** Documentation only.

### ADR-0003 — llamacpp restart-per-Profile

Code is already compliant. `loadProfileManaged` (`server.go:300-337`) does stop-and-restart with the Model in args. `auto_unload` is naturally ignored on this path because `ShouldAutoUnload()` is only consulted by `loadProfileExternal`. **No code change needed.** Documentation only.

*One sanity check*: the TDD §11 mentions a "hardware param conflict warning." A grep for `hardware|conflict|warnHardware` in `internal/launcher/*.go` returns nothing. The warning was never implemented or has already been removed. No code to delete; just TDD cleanup.

### ADR-0004 — `auto_unload` is one rule (covers cross-server too)

| What needs to change | Where | Current behaviour |
|---|---|---|
| When `auto_stop_server: false` and `auto_unload: true`, iterate other-server states and unload their Models | `server.go:282-292` (`LoadProfile`) | The `!auto_stop_server` branch does nothing for other servers — leaves their Models loaded indefinitely |

Roughly 15 lines: after the existing auto-stop loop, add a parallel loop guarded by `!cfg.ShouldAutoStopServer() && cfg.ShouldAutoUnload()` that calls `UnloadBackendModel` (or the backend's `UnloadModel` directly) for every other still-alive instance that has an `ActiveModel`. Trivial test: two backends running, one has a Model loaded; activate the other with `auto_stop_server: false, auto_unload: true`; assert the first backend's Model is gone but the server is still up.

### ADR-0005 — Profile must name its LLM Server; soft-deprecate `defaults.server`

| What needs to change | Where | Current behaviour |
|---|---|---|
| Emit a warning when a Profile lacks `server:` and 2+ enabled servers exist | `config.go:282-326` (`ResolveProfile`), `config.go:201-258` (`validateAll`) | Falls back to `defaults.server` silently |
| `cmdStart` no longer assumes `cfg.Defaults.Server` is set | `cli.go:206-210` | Errors with "no default server configured" — needs to either auto-detect-when-one or require `--profile` |
| Example config drops `defaults.server` | `internal/launcher/defaults/config.yaml:97` | Currently has `server: llamacpp` in defaults |
| `ProfileNames` sort no longer ranks by "default backend first" | `config.go:339-358` | `serverRank` uses `defaultServer`; without it, sort falls back to alphabetical-by-server, which is fine |
| Auto-detection when only one server enabled is preserved | `config.go:195-197`, `config.go:229-231` | Already correct; keep |

Warning channel: `validateAll` already collects problems for `config validate`. Single-shot warnings during normal `Reload` / `LoadConfig` should also surface — current `validate` is fast-fail and only returns errors. Add a separate `warnings []string` channel or print to stderr at load time.

### ADR-0006 — Instances keyed by `host:port`; multi-instance supported

This is the load-bearing refactor. The state model changes from "one record per backend type" to "one record per running instance, keyed by address."

| What needs to change | Where | Current behaviour |
|---|---|---|
| State file naming includes port (and host when not loopback) | `server.go:424-426` (`backendStatePath`) | `state-{backend}.json` |
| Replace `ReadBackendState(backend)` with `ReadInstanceState(addr)` and `ReadInstancesForBackend(backend) []*ServerState` | `server.go:428-447` | Single-lookup by backend name |
| `writeBackendState` / `removeBackendState` take an instance key (addr) | `server.go:476-498` | Take backend name |
| `migrateOldState` extended to migrate `state-{backend}.json` → `state-{backend}-{port}.json` | `server.go:500-522` | Migrates legacy `state.json` → `state-{backend}.json` |
| `LoadProfile`/`loadProfileManaged`/`loadProfileExternal`/`EnsureServer` look up state by addr | `server.go:244-377` | Use `ReadBackendState(profile.Backend)` |
| `auto_stop_server` loop scope changes from "other backend" to "other instance" | `server.go:282-292` | Compares `s.Backend != profile.Backend` — should compare `s.Addr() != profile.Addr()` |
| `cmdStop` enumerates instances, not backends | `cli.go:246-293` | `cmdStop [backend]` — needs to also accept addr, and the auto-detect list shows addr-disambiguated entries |
| `cmdUnload` enumerates instances | `cli.go:130-184` | Same |
| `cmdStatus` shows one row per running instance | `cli.go:295-411` | One row per *enabled backend*, with `stateMap[backend]` lookup |
| `cmdLogs` enumerates instances | `cli.go:459-527` | Iterates ReadAllStates but assumes one-per-backend implicitly |
| `RunInteractiveMenu` drops `primaryState`; the three-menu split (`runStoppedMenu`, `runLoadedMenu`, `runIdleMenu`) becomes either one unified menu or stays as three but enumerates instances | `menu.go:19-69` | Picks a `primaryState` and routes to one of three menus — see [the "no global active" flagged ambiguity in CONTEXT.md](../../CONTEXT.md#flagged-ambiguities) |
| `detectRunningServers` returns one entry per *instance*, not per *backend* | `menu.go:277-327` | Iterates enabled backends, one HealthCheck per backend |
| `doStopServer` sub-list items disambiguate by addr (already pretty close) | `menu.go:329-378` | Items show `backendDisplayName(s.name)` + addr in description — minor polish to ensure same-backend duplicates are distinguishable |

This is also the natural point to drop `Managed` (ADR-0001) — both changes touch the same struct and the same read/write helpers. Doing them in one pass is materially cheaper than two.

### ADR-0007 — Idempotency by name + drift notice + `--restart`

| What needs to change | Where | Current behaviour |
|---|---|---|
| Drift comparison in same-name early-exit | `server.go:302-304` (managed) and `server.go:341-343` (external) | Returns early if `state.ActiveProfile == profile.Name` — no parameter check |
| State file records enough resolved-profile fields to compare (or a hash) | `server.go:27-39` (`ServerState`) | Records `ContextSize`, `GPULayers` only |
| `cmdLoad` accepts `--restart` (or `--force`) | `cli.go:96-128` | No such flag |
| Drift notice printed to stderr; does not change exit code | (new code in same paths) | n/a |

Recommended: store a small `ResolvedSnapshot` struct (or a hash of the resolved `ProfileParams`) in the state file at activation time; the drift check compares against the live resolved profile. A hash is forward-compatible — it doesn't constrain which fields trigger drift.

### CONTEXT.md naming consequence (low priority, optional)

- Rename `Backend` interface to `LLMServer` and the `ManagedBackend` to something less load-bearing now that the "managed" distinction is gone (`ForkLoadedServer`? `RestartLoadedServer`?). Cosmetic; touches every backend file. Defer until everything else lands.
- Update inline comments and error messages that still say "backend" — these are mostly user-facing strings ("unknown backend %q", "auto-stopping %s") and can stay through grep-and-replace.

---

## Recommended execution order

Six phases, each shippable and independently testable. Earlier phases are smaller / lower-risk; the schema-touching phase is consolidated to avoid touching the state file twice.

### Phase 1 — Documentation alignment (no code)

- Rewrite `llama-launcher.TDD.md` against ADRs 0001–0007: drop "router mode" framing, drop §9 (Model Management API table), drop "managed/external" language, rewrite §7 (State Files) for per-instance schema, drop §11 hardware-conflict warning row, document the unified `auto_unload` rule and the `--restart` flag.
- Update `README.md` for the same items it covers.
- Update `CHANGELOG.md` with a forward-looking "Architecture" entry pointing to the new ADRs.

*Risk: zero. Output: design and code now agree on paper.*

### Phase 2 — Cross-server `auto_unload` (ADR-0004)

- Add the cross-server unload loop in `LoadProfile`.
- One unit test, one integration test (two-backend setup).

*Risk: low. Pure additive; no schema change.*

### Phase 3 — `defaults.server` soft-deprecation (ADR-0005)

- Add validation warning channel for non-fatal config issues.
- Remove `defaults.server` from `defaults/config.yaml`; add a comment noting the deprecation in the same place.
- Fix `cmdStart` to handle the missing default gracefully (auto-detect when one server enabled, else require `--profile`).
- Update affected tests.

*Risk: low. Behaviour preserved for single-backend users; warning surfaces real config issues.*

### Phase 4 — State schema rewrite: drop `Managed`, key by `addr` (ADR-0001 + ADR-0006 combined)

The big one. Combine because both touch `ServerState`, the state file naming, and every read/write helper.

Substeps in suggested order:

1. **State file plumbing first.** New naming convention `state-{backend}-{port}.json`. Rewrite `backendStatePath` → `instanceStatePath(addr)`. Update `ReadBackendState`, `writeBackendState`, `removeBackendState` signatures. Extend `migrateOldState` to also migrate `state-{backend}.json` → `state-{backend}-{port}.json` (taking host/port from the file contents).
2. **`ServerState` cleanup.** Remove `Managed` field. Update `IsServerAlive` to always use HealthCheck.
3. **Lifecycle paths.** Update `LoadProfile`, `EnsureServer`, `loadProfileManaged`, `loadProfileExternal`, `StartServer`, `connectExternalServer`, `startManagedServer` to operate on instance keys (addr). Collapse `stopManagedServer` + `disconnectExternalServer` into one `stopServer` that signals the PID if known + alive, then calls `backend.TryStop`.
4. **Auto-stop semantics.** Update the `auto_stop_server` loop to compare addresses, not backend names.
5. **CLI surface.** `cmdStatus`, `cmdStop`, `cmdUnload`, `cmdLogs` updated to enumerate instances.
6. **TUI surface.** `RunInteractiveMenu` drops `primaryState`; `detectRunningServers` returns instances; the three sub-menus either unify or each gains an "instance selector" front-end consistent with `doStopServer`.
7. **Tests.** Heavy: existing `ServerState{Managed: ...}` test fixtures all need updating; new tests for multi-instance + migration.

*Risk: high. This is the change most likely to surface integration bugs. Recommended to do it on a branch with extra manual smoke-test coverage (start two llamacpp on different ports; verify status, stop, switch all behave per spec).*

### Phase 5 — Idempotency drift notice and `--restart` (ADR-0007)

- Add resolved-params snapshot or hash to `ServerState` (additive field; safe migration via `omitempty`).
- Add drift check before early-exit in both `loadProfile*` paths; print a stderr notice when divergent.
- Add `--restart` / `--force` flag to `cmdLoad`; threaded through `LoadProfile` (new parameter or wrapper).
- Tests for the three cases: identical → no-op silently; same name, different params → no-op with notice; with `--restart` → re-activate.

*Risk: low. Additive; behaviour change is informational + opt-in.*

### Phase 6 — Cosmetic naming (optional, defer until others land)

- `Backend` → `LLMServer` interface rename.
- `ManagedBackend` → `ForkLoadedServer` (or similar; pick a name once the multi-instance refactor has shaken out the dust).
- Update CONTEXT.md flagged ambiguities to reflect that the code now matches the language.

*Risk: low but noisy. Best done in one mechanical pass after the behavioural work is settled.*

---

## What's *not* on this list

- Log file naming under multi-instance (`{backend}-{port}-{timestamp}.log` vs current `{backend}-{timestamp}.log`). Multiple instances can already coexist without log collision because timestamps differ at second resolution. Revisit only if collisions become real.
- Port-collision handling for two Profiles defaulting to the same port. Today: second activation overwrites the first's state file silently. Under Phase 4 it becomes "same address → same instance slot → second Profile takes over" per ADR-0006. No port auto-allocation; that would be a separate feature.
- `status --json` / `list --json` from TODO.md — orthogonal to this work; existing entries stand.
- `sort_alphabetically` flag from TODO.md — orthogonal.
- Shell completions — orthogonal.

---

## Dependency graph

```
Phase 1 (docs) ── independent ──┐
Phase 2 (auto_unload x-server) ── independent ──┤
Phase 3 (defaults.server) ── independent ──┤
                                                ├── all four can land in any order
Phase 4 (state schema + multi-instance) ────────┤
                                                │
Phase 5 (drift notice + --restart) ─── depends on Phase 4 (state file holds resolved-snapshot) ──┤
                                                                                                  │
Phase 6 (renames) ── depends on Phase 4 (then a mechanical sweep) ─────────────────────────────────
```

Phases 1–4 can ship in parallel branches if multiple people are working. Phase 5 must follow Phase 4 because it adds a new state file field. Phase 6 is a mechanical rename and should be last so it isn't a moving target.
