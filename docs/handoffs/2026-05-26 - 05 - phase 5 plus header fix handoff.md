# Handoff — Phase 5 + header regression fix + housekeeping

**Date:** 2026-05-26
**Branch:** `main`
**Prior handoffs:**
- [2026-05-26 - 00 - fit-gap-adrs-vs-code.md](2026-05-26%20-%2000%20-%20fit-gap-adrs-vs-code.md) — phased plan
- [2026-05-26 - 01 - phase progress handoff.md](2026-05-26%20-%2001%20-%20phase%20progress%20handoff.md) — after Phase 2
- [2026-05-26 - 02 - phase 3 complete handoff.md](2026-05-26%20-%2002%20-%20phase%203%20complete%20handoff.md) — after Phase 3
- [2026-05-26 - 03 - phase 4 complete handoff.md](2026-05-26%20-%2003%20-%20phase%204%20complete%20handoff.md) — after Phase 4
- [2026-05-26 - 04 - phase 5 complete handoff.md](2026-05-26%20-%2004%20-%20phase%205%20complete%20handoff.md) — after Phase 5 (before the regression fix)

This doc supersedes 04 for "what's on `main` right now." 04 was written before the menu-header regression surfaced.

## Where we are

| Phase | Status | Commit |
|---|---|---|
| 1. Documentation alignment | ✅ pushed | `f3b82d2` |
| 2. Cross-server `auto_unload` (ADR-0004) | ✅ committed, **not pushed** | `7ef2d9a` |
| 3. `defaults.server` soft-deprecation (ADR-0005) | ✅ committed, **not pushed** | `a131e28` |
| 4. State schema + multi-instance (ADR-0001 + ADR-0006) | ✅ committed, **not pushed** | `17c5288` |
| 5. Drift notice + `--restart` (ADR-0007) | ✅ committed, **not pushed** | `3ec72fd` |
| 5b. Menu-header regression fix | ✅ committed, **not pushed** | `bed18d5` |
| 5c. VERSION 1.3.0 + handoff trail housekeeping | ✅ committed, **not pushed** | `fc15905` |
| 6. `Backend` → `LLMServer` rename | ⬜ optional | — |

Seven unpushed commits on `main`. User pushes themselves.

## Working tree right now

`git status` is clean except for two intentionally-untracked local-only directories:

- `.claude/`
- `prompts/`

`go build ./...`, `go test ./...`, `go vet ./...` all clean at `fc15905`.

## What changed since the Phase 5 handoff (04)

Two commits landed after `3ec72fd` was written up in handoff 04:

### `bed18d5` — Restore per-server status header

Phase 4 (commit `17c5288`) rewrote `serverStatusLines` in `internal/launcher/menu.go` to iterate per-instance state files only. Side effect: when no state file exists for an enabled server type, the header showed nothing for it. With zero state files (e.g. after the legacy-state migration cleared them, or before the first activation), the entire header collapsed to a single `○ stopped` line — hiding the configured server list and any externally-started server that happened to be reachable at its configured address.

The fix iterates enabled servers from `cfg.Servers` again (alphabetical), probing each:
- If state files exist for that backend, probe each instance's address.
- Otherwise, probe `cfg.ConfiguredBackendAddr(name)` so externally-started servers (Ollama, LM Studio) light up without needing re-activation.
- Healthy probes render a green `●` row per instance; if no probe for that server type is healthy, a single `○ {name} stopped` row is emitted.

Net behaviour matches 1.2.2 for the single-instance case and gracefully extends for multi-instance (each healthy instance gets its own row).

CHANGELOG got a new `### Fixed` block at the top of `## 1.3.0` documenting this.

### `fc15905` — VERSION 1.3.0 + handoff trail

Housekeeping commit that had been deferred per the "don't touch pre-existing changes" convention from earlier handoffs:
- `VERSION`: `1.2.2` → `1.3.0`.
- Renames `docs/handoffs/20260526-fit-gap-adrs-vs-code.md` to `docs/handoffs/2026-05-26 - 00 - fit-gap-adrs-vs-code.md` (the new convention used by 01-04).
- Adds the four per-phase handoffs (01-04) to the repo so the architecture-refactor narrative is reviewable from `main` rather than from local-only files. The broken CHANGELOG link to the old filename is now resolved.

## Open questions / loose ends

These came up in conversation but were intentionally left out of scope:

1. **Legacy-state migration is destructive** — the user noticed that the new binary deletes legacy `state.json` / `state-{backend}.json` files on first access. If a server was running under the old format, its state record disappears (the process keeps running, but the launcher no longer tracks it). ADR-0006 explicitly chose this — "treat legacy records as stale" — and the `bed18d5` fix mitigates the visibility part (the header now probes the configured address). The orphan-process tracking question (do we want a one-shot upgrade that parses the old file into the new schema?) is open. User asked **not** to kill old processes; recovery is to re-activate the profile (external) or stop the orphan and re-activate (managed). Worth raising as its own TODO if it bites again.
2. **`auto_unload` guard in `loadProfileExternal`** — during Phase 5 work I briefly removed the `cfg.ShouldAutoUnload()` guard around the same-server unload-before-load, then restored it. The semantics: per ADR-0004, `auto_unload` governs *other* instances. The current code still guards the same-instance unload with `auto_unload`, which means switching profiles on the same external server with `auto_unload: false` would try to LoadModel on top of the existing one. Likely a real bug but explicitly **out of scope for Phase 5** and not the user's current concern. Flag for a future pass.

## Phase 6 — optional next step

Mechanical rename `Backend` → `LLMServer`. See handoff 04 for the scope sketch; nothing has changed since. User has flagged this as **optional**; do not start without confirmation.

## Conventions still in force

(Unchanged from prior handoffs — repeating for the agent who picks up cold.)

1. **TDD describes target state.** Don't add "not yet implemented" caveats.
2. **Commit style** — short imperative subject, optional body. No AI attribution / Co-Authored-By lines.
3. **Don't auto-push.** User pushes themselves.
4. **`.claude/` and `prompts/`** are local-only; never `git add .`, name files explicitly.
5. **Warnings channel** (`Config.Warnings` printed to stderr by `LoadConfig`) is the canonical place for non-fatal deprecation notices. The Phase 5 drift notice uses a separate `printDriftNotice` helper because it's emitted from `LoadProfile` rather than config load — same destination (stderr), different call site.

## Suggested skills

- **`handoff`** — when wrapping up the next chunk, write the next handoff. Today's counter would be `06` (this one is `05`); resets to `00` tomorrow.
- **`brew-release`** — natural next step if the user wants to ship 1.3.0. Seven unpushed commits + `VERSION` already at 1.3.0 means the next push + tag + release is teed up.
- **`feature-implementation`** — only if the user explicitly asks to do Phase 6 (the rename). For a mechanical rename, `simplify` or a direct edit pass may be more appropriate than the full structured flow.
- **`coding-standards`** — consult before writing Go code if uncertain about style.

## Quick env recap

- Repo: `/Users/airic/Repos/llama-launcher`
- Platform: macOS (Apple Silicon)
- Go module: `github.com/airiclenz/llama-launcher`
- Build: `make build` / `make install`
- Tests: `go test ./...`
- Memory store: `/Users/airic/.claude/projects/-Users-airic-Repos-llama-launcher/memory/`
