# Handoff — ADRs 0001–0007 implementation progress

**Date:** 2026-05-26
**Branch:** `main`
**Prior session focus:** Documentation alignment + first behaviour change of the fit-gap from [docs/handoffs/20260526-fit-gap-adrs-vs-code.md](20260526-fit-gap-adrs-vs-code.md).

## Where we are in the plan

The phased plan is in [`20260526-fit-gap-adrs-vs-code.md`](20260526-fit-gap-adrs-vs-code.md) — read it before doing anything else. Six phases; we have landed two:

| Phase | Status | Commit | Notes |
|---|---|---|---|
| 1. Documentation alignment | ✅ committed + pushed | `f3b82d2` | Adds ADRs 0001–0007 + CONTEXT.md; rewrites TDD/README/CHANGELOG to describe target architecture. Note: TDD now describes some behaviour that the code does not yet implement (Phases 3–5). |
| 2. Cross-server `auto_unload` (ADR-0004) | ✅ committed, **not pushed** | `7ef2d9a` | Adds `shouldCrossServerUnload` helper + new `else if` branch in `LoadProfile`. Unit-tested. |
| 3. `defaults.server` soft-deprecation (ADR-0005) | ⬜ next | — | See [Next-up notes](#next-up-phase-3) below. |
| 4. State schema + multi-instance (ADR-0001 + ADR-0006) | ⬜ | — | Combined per fit-gap. Biggest of the remaining phases. |
| 5. Drift notice + `--restart` (ADR-0007) | ⬜ | — | Depends on Phase 4 (adds a `resolved_params` snapshot to the state schema). |
| 6. `Backend` → `LLMServer` rename (optional) | ⬜ | — | Mechanical sweep; defer until last. |

## Working state right now

- Working tree is clean except for two untracked dirs (`.claude/`, `prompts/`) — local working notes, **do not commit**.
- `git status`: branch is 1 commit ahead of `origin/main` (the Phase 2 commit `7ef2d9a` is unpushed).
- `go build ./...`, `go test ./...`, `go vet ./...` all clean as of `7ef2d9a`.

## Decisions and conventions established this session

These are user-confirmed and should be carried forward without re-asking:

1. **TDD describes target state per ADRs** — even when code hasn't caught up yet. The fit-gap doc and ADRs together capture the "is vs should-be" gap; the TDD does not need to caveat every section that's ahead of the code. (Phase 1 question.)
2. **TDD section numbering** — the user wanted clean renumbering after dropping the old §9 (Model Management API) and §12 (Known Limitations). Internal cross-refs were already audited; ADR-0005 and ADR-0006's back-references to "TDD §4.6" and "TDD §7" still resolve correctly.
3. **Cross-server `auto_unload` skips managed backends** — implemented via `shouldCrossServerUnload`. Documented in TDD §6.8.
4. **Commit message style** — short imperative subject, optional 1-paragraph body, no AI attribution / Co-Authored-By lines (per global `~/.claude/CLAUDE.md`).
5. **Handoff doc was deliberately left untracked** — when the user committed Phase 1 they excluded `docs/handoffs/20260526-fit-gap-adrs-vs-code.md` even though CHANGELOG and TODO link to it. The link is intentionally broken on `main` for now. Do **not** "fix" that by silently committing the handoff.
6. **Untracked dirs to leave alone:** `.claude/` (local Claude config), `prompts/` (local notes), `docs/architecture-reviews/` (generated artifact).

## Next up: Phase 3

ADR-0005 says: every Profile must declare `server:` explicitly. Auto-detect still applies when only one server is enabled. When multiple servers are enabled and a Profile omits `server:`, fall back to `defaults.server` **with a deprecation warning**. `defaults.server` is removed in a later release.

From the fit-gap §"ADR-0005":

| What needs to change | Where |
|---|---|
| Emit a warning when a Profile lacks `server:` and 2+ enabled servers exist | `config.go:282-326` (`ResolveProfile`), `config.go:201-258` (`validateAll`) |
| `cmdStart` no longer assumes `cfg.Defaults.Server` is set | `cli.go:206-210` |
| Example config drops `defaults.server` | `internal/launcher/defaults/config.yaml:97` |
| `ProfileNames` sort no longer ranks by "default backend first" | `config.go:339-358` |
| Auto-detection when only one server enabled is preserved | `config.go:195-197`, `config.go:229-231` |

Notes on scope:

- Need a non-fatal warning channel. `validateAll` already collects problems. The fit-gap suggests a separate `warnings []string` channel, or printing to stderr at load time. Decide and document.
- `cmdStart` (`cli.go:206-210`) currently errors with "no default server configured" when `Defaults.Server` is unset. New behaviour: auto-resolve when one server enabled; otherwise require `--profile`.
- `ProfileNames` sort: the current `serverRank` ties profiles to `defaultServer`; without `defaults.server`, sort falls back to alphabetical-by-server which is fine — just drop the ranking branch.
- Don't touch `defaults/config.yaml` and forget to update `config_test.go` fixtures — there are tests that load the embedded example.
- Phase 1 already pre-documented this in TDD §4.6, §10 (Error Handling), and example schema in §4.2. So the TDD changes for Phase 3 are minimal — mostly verifying the docs still match.

## Files to expect touching in Phase 3

- `internal/launcher/config.go` — `ResolveProfile`, `validateAll`, `Validate`, `ProfileNames`/sort, and probably a small `Warnings` slice on `Config` or a return-value addition to `LoadConfig`.
- `internal/launcher/cli.go` — `cmdStart` graceful handling of missing default.
- `internal/launcher/defaults/config.yaml` — remove `defaults.server`, leave a comment.
- `internal/launcher/config_test.go` — new tests for the deprecation warning; update existing fixtures that rely on `defaults.server`.
- `CHANGELOG.md` — entry under `## Unreleased` → `### Changed`.
- `TODO.md` — mark Phase 3 sub-bullet `[x]`.

## Suggested skills

- **`/feature-implementation`** — same skill used for Phases 1 and 2; the user is following its step structure (Read → Plan → Code → Docs → Self-review). Continue using it for Phase 3.
- **`coding-standards`** — referenced by the global `CLAUDE.md`; consult before writing code if uncertain about style. The Go file already follows these conventions; recent edits should match.

## Quick environment recap

- Repo: `/Users/airic/Repos/llama-launcher`
- Platform: macOS (Apple Silicon)
- Go module: `github.com/airiclenz/llama-launcher`
- Build: `make build` / `make install`
- Tests: `go test ./...`

## Cautions for the next agent

1. **Don't re-litigate the Phase 1 doc style choices** — the user already chose "target state per ADRs" and "renumber sections." Just follow.
2. **Don't auto-push.** The user prefers to push themselves. Commit when asked; push only on explicit request.
3. **Phase 2 is committed but unpushed.** If the user asks for "the current state of `main`" or similar, mention this.
4. **`.claude/` and `prompts/` are local-only.** Never `git add .` — always name files explicitly.
5. **Memory store path:** `/Users/airic/.claude/projects/-Users-airic-Repos-llama-launcher/memory/`. Current memories include doc-update workflow, shared-port design, review scope, AI delegation. Read those if a question touches them.
