# Handoff — Phases 1–3 done, Phase 4 next

**Date:** 2026-05-26
**Branch:** `main`
**Prior handoffs:**
- [2026-05-26 - 00- fit-gap-adrs-vs-code.md](2026-05-26%20-%2000-%20fit-gap-adrs-vs-code.md) — phased plan (untracked locally)
- [2026-05-26 - 01 - phase progress handoff.md](2026-05-26%20-%2001%20-%20phase%20progress%20handoff.md) — state after Phase 2

## Where we are

| Phase | Status | Commit |
|---|---|---|
| 1. Documentation alignment | ✅ pushed | `f3b82d2` |
| 2. Cross-server `auto_unload` (ADR-0004) | ✅ committed, **not pushed** | `7ef2d9a` |
| 3. `defaults.server` soft-deprecation (ADR-0005) | ✅ committed, **not pushed** | `a131e28` |
| 4. State schema + multi-instance (ADR-0001 + ADR-0006) | ⬜ **next** | — |
| 5. Drift notice + `--restart` (ADR-0007) | ⬜ | — |
| 6. `Backend` → `LLMServer` rename | ⬜ optional | — |

Two unpushed commits on `main` (`7ef2d9a`, `a131e28`). User pushes themselves — do not push without an explicit ask.

## Working tree right now

- `git status` shows one staged-unrelated change: `docs/handoffs/20260526-fit-gap-adrs-vs-code.md` is deleted (the file was renamed locally to `2026-05-26 - 00- fit-gap-adrs-vs-code.md`, but the new name is still untracked). The `CHANGELOG.md` link to the old filename is therefore broken on `main`. **Leave this alone** unless the user asks to fix it — Phase 1's handoff established that the broken link was deliberate.
- Untracked dirs to never commit: `.claude/`, `prompts/`, `docs/handoffs/2026-05-26 - 00- ...md` (handoff), `docs/handoffs/2026-05-26 - 01 - ...md` (handoff), `docs/handoffs/2026-05-26 - 02 - ...md` (this file).
- `go build ./...`, `go test ./...`, `go vet ./...` all clean at `a131e28`.

## What landed in Phase 3 (commit `a131e28`)

Code (`internal/launcher/`):
- `config.go`: added `Warnings []string` field on `Config`; new `defaultsServerFallbackWarnings` helper; `validate()` populates `c.Warnings`; `validateAll()` appends the same warnings to its problems list; `LoadConfig` prints warnings to stderr (`warning: ...`); `ProfileNames` sort dropped `serverRank` — now plain alphabetical-by-server.
- `cli.go`: `cmdStart` error message rewritten — single-server case is handled by validate's auto-assign, so the branch now only fires under multi-server and asks for `--profile`.
- `defaults/config.yaml`: removed `server: llamacpp` from `defaults:`; example `example:` profile now sets `server: llamacpp` explicitly; surrounding comments updated.
- `config_test.go`: added `TestValidate_DefaultsServerFallbackWarning`, `TestValidate_NoFallbackWarning_SingleServer`, `TestValidateAll_DefaultsServerFallbackWarning`; updated comment in `TestProfileNames_FavouritesFirst` to describe alphabetical-by-server.

Docs:
- `CHANGELOG.md`: new `## Unreleased / ### Changed` entry. **See open question below about version heading.**
- `README.md`: sample config dropped `defaults.server`, each profile sets `server:` explicitly; sort-description prose updated.
- `TODO.md`: Phase 3 marked `[x]`.

TDD did not need changes — Phase 1 already pre-documented this in §4.6, §4.3, §10, §5.2.

## Open question carried into next session

The user asked: **"should the Unreleased section be gathered under a not-yet-published version instead?"** I offered three choices (1.3.0 / 1.2.3 / leave as Unreleased) via `AskUserQuestion` but the user rejected the prompt before answering. `VERSION` is still `1.2.2`. Phases 4–6 are not yet implemented, so the question of which version receives all this work is still open. **Ask the user before doing anything else CHANGELOG-related.**

## Phase 4 — next up

Phase 4 combines:
- **ADR-0001** — remove the `Managed` distinction; `stop` is unconditional.
- **ADR-0006** — instances keyed by `host:port`; per-instance state files `state-{backend}-{port}.json` (or `state-{backend}-{host}-{port}.json` for non-loopback).

Largest of the remaining phases. Notes from the fit-gap (see [2026-05-26 - 00- fit-gap-adrs-vs-code.md](2026-05-26%20-%2000-%20fit-gap-adrs-vs-code.md) for details):
- New filename scheme + migration from current `state-{backend}.json`.
- Drop `Managed` field from `ServerState`; update everywhere it's read.
- Add `resolved_params` snapshot to the state schema (Phase 5 depends on it).
- Update `ReadInstanceState(addr)`, `ReadInstancesForBackend(backend)`, `ReadAllStates`. Some of these names already appear in TDD §7.1 but the code still uses the per-backend form.
- TUI/CLI surfaces that disambiguate by backend need to disambiguate by `host:port` instead — see TDD §3.1 and §3.2 `stop [target]` / `logs [target]`.
- Drift detection (Phase 5) reads `resolved_params`, so add the field even if drift logic comes later.

The TDD already describes the target state (§5.2 lists `state-{backend}-{port}.json`, etc.), so docs work is mostly verifying the prose matches the new code rather than rewriting it.

## Conventions established (carry forward)

1. **TDD describes target state** — don't add "not yet implemented" caveats to existing sections; the ADRs already capture the gap.
2. **Commit style** — short imperative subject, optional body. No AI attribution / Co-Authored-By lines (global `~/.claude/CLAUDE.md`).
3. **Don't auto-push.** User pushes themselves.
4. **`.claude/`, `prompts/`, and untracked handoffs are local-only.** Never `git add .` — always name files.
5. **Handoff doc links in CHANGELOG/TODO are intentionally broken** on `main`. Do not "fix" by silently committing handoff files.
6. **Warnings channel** introduced in Phase 3 (`Config.Warnings`, printed by `LoadConfig` to stderr). Use the same channel for any future non-fatal deprecation notices.

## How to test Phases 2 & 3 manually

Already explained in the last assistant turn — see conversation. Short version:
- **Phase 2**: two enabled servers (llamacpp + ollama), `auto_stop_server: false`, load an ollama profile, then load a llamacpp profile, then `status` — ollama should now be running with no model.
- **Phase 3**: multi-server config + a profile missing `server:` + `defaults.server: llamacpp` → stderr prints `warning: profile "..." missing 'server:' — falling back to defaults.server ...`. Single-server config → no warning. `start` without `--profile` under multi-server → exits with `Error: multiple servers enabled and no default — specify --profile <name>`.

## Suggested skills

- **`/feature-implementation`** — same skill used for Phases 1–3. Continue using it for Phase 4. The user is following its Read → Plan → Code → Docs → Self-review structure.
- **`coding-standards`** — consult before writing Go code if uncertain about style.
- **`/handoff`** — when wrapping up Phase 4, write the next handoff. Filename will be `2026-05-XX - NN - ....md` (counter resets each day).

## Quick env recap

- Repo: `/Users/airic/Repos/llama-launcher`
- Platform: macOS (Apple Silicon)
- Go module: `github.com/airiclenz/llama-launcher`
- Build: `make build` / `make install`
- Tests: `go test ./...`
- Memory store: `/Users/airic/.claude/projects/-Users-airic-Repos-llama-launcher/memory/`
