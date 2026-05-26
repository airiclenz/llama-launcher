# Handoff — Phase 5 done, Phase 6 (optional) next

**Date:** 2026-05-26
**Branch:** `main`
**Prior handoffs:**
- [2026-05-26 - 00 - fit-gap-adrs-vs-code.md](2026-05-26%20-%2000%20-%20fit-gap-adrs-vs-code.md) — phased plan
- [2026-05-26 - 01 - phase progress handoff.md](2026-05-26%20-%2001%20-%20phase%20progress%20handoff.md) — after Phase 2
- [2026-05-26 - 02 - phase 3 complete handoff.md](2026-05-26%20-%2002%20-%20phase%203%20complete%20handoff.md) — after Phase 3
- [2026-05-26 - 03 - phase 4 complete handoff.md](2026-05-26%20-%2003%20-%20phase%204%20complete%20handoff.md) — after Phase 4

## Where we are

| Phase | Status | Commit |
|---|---|---|
| 1. Documentation alignment | ✅ pushed | `f3b82d2` |
| 2. Cross-server `auto_unload` (ADR-0004) | ✅ committed, **not pushed** | `7ef2d9a` |
| 3. `defaults.server` soft-deprecation (ADR-0005) | ✅ committed, **not pushed** | `a131e28` |
| 4. State schema + multi-instance (ADR-0001 + ADR-0006) | ✅ committed, **not pushed** | `17c5288` |
| 5. Drift notice + `--restart` (ADR-0007) | ✅ committed, **not pushed** | `3ec72fd` |
| 6. `Backend` → `LLMServer` rename | ⬜ optional | — |

Five unpushed commits on `main` (`7ef2d9a`, `a131e28`, `7f9b43f`, `17c5288`, `3ec72fd`). User pushes themselves — do not push without an explicit ask.

## Working tree right now

- `git status` shows the same pre-existing unrelated changes that were noted in prior handoffs:
  - `VERSION` is modified locally (`1.2.2` → `1.3.0`) — this was already on disk when Phase 5 started; **leave it alone** unless the user asks to bump.
  - `docs/handoffs/20260526-fit-gap-adrs-vs-code.md` is staged-as-deleted; new filename (`2026-05-26 - 00 - fit-gap-adrs-vs-code.md`) is still untracked. CHANGELOG link to the old name remains broken on `main`. **Leave this alone** unless the user asks to fix it.
- Untracked: `.claude/`, `prompts/`, and all five handoff docs (00–04 including this one).
- `go build ./...`, `go test ./...`, `go vet ./...` all clean at `3ec72fd`.

## What landed in Phase 5 (commit `3ec72fd`)

See the commit and diff for details. Highlights:

Code (`internal/launcher/`):
- `server.go`: `LoadProfile` signature changed to `LoadProfile(cfg, profile, restart bool, progress)`. New idempotency block at the top: if `state.ActiveProfile == profile.Name` and `IsServerAlive(state)` and not `--restart`, compute drift between `state.ResolvedParams` and `profile.ProfileParams` (plus the `ActiveModel` vs `ModelPath` comparison), print a notice to stderr if any drift exists, return `(state, false, nil)`. New helpers `paramDrift`, `formatIntPtr`, `formatBoolPtr`, `formatFloatPtr`, `printDriftNotice`. The duplicate `state.ActiveProfile == profile.Name` short-circuit was removed from `loadProfileManaged` *and* `loadProfileExternal` — necessary so `--restart` actually re-activates.
- `cli.go`: `cmdLoad` now parses `--restart` / `-r` / `--force` and rejects unknown flags / extra positional args. Usage text in `printUsage()` updated to show `[--restart]`.
- `menu.go`: `doLoadProfile` passes `restart: false`.
- `server_test.go`: new `TestParamDrift` (identical params, nil-vs-nil, changed int, set-vs-unset, bool/float, slot-identity ignored).

Docs:
- `CHANGELOG.md`: new bullet at the top of `## 1.3.0` describing the drift notice and `--restart`.
- `TODO.md`: Phase 5 marked `[x]`.
- TDD did not need changes — Phase 1 already pre-documented the target state in §3.2 and §6.1.

## Design notes worth carrying forward

1. **`paramDrift` ignores slot-identity fields** (`Server`, `Host`, `Port`). Drift in those puts the activation in a different address slot, which the idempotency check would not have matched in the first place.
2. **`(unset)` sentinel** is used when one side has a nil pointer and the other doesn't — the user sees `field: 8192 → (unset)` rather than confusing `0` values.
3. **Notice format** is informational, not a warning — exit code stays 0 so scripts that rely on idempotent `load` still work. Notice goes to stderr; stdout is unaffected.
4. **Model drift** is detected via `state.ActiveModel != profile.ModelPath` and appended to the drift list. ADR-0007's prose talks about "resolved parameters" but the chosen rule is more useful if it also catches model-file edits, which is the most common drift scenario.
5. **`auto_unload` guard** was briefly removed from `loadProfileExternal`'s inner unload during Phase 5 implementation and then restored — that's a separate concern (per ADR-0004 same-server unload should probably always run when switching models) and not in scope for Phase 5. Worth revisiting on a future pass if it turns out to be a real bug.

## Phase 6 — optional next step

`Backend` → `LLMServer` mechanical rename, per TDD §5.3 and §11 ("Future Considerations"). The interface name is the only thing that drifted from the domain language in CONTEXT.md. Everything else now lines up.

Scope sketch (not authoritative — re-read TDD §5.3 first):
- Rename the `Backend` Go interface to `LLMServer`. Find a less load-bearing name for `ManagedBackend` (suggested: `ForkableLLMServer` or keep `ManagedLLMServer`).
- Update the `GetBackend(name)` registry function name (e.g. `GetLLMServer`).
- Update all call sites (`internal/launcher/*.go`), tests, and the TDD §5.3 / §5.2 / file responsibility table.
- The user-facing `backend` config term has already been renamed to `server`, so the YAML schema is unaffected.

This is a mechanical rename — best done with `gopls`/IDE refactor support. No behaviour change.

The user has marked Phase 6 as **optional**; do not start it without confirmation.

## Conventions established (carry forward)

1. **TDD describes target state** — don't add "not yet implemented" caveats to existing sections; the ADRs capture the gap.
2. **Commit style** — short imperative subject, optional body. No AI attribution / Co-Authored-By lines.
3. **Don't auto-push.** User pushes themselves.
4. **`.claude/`, `prompts/`, and untracked handoffs are local-only.** Never `git add .` — always name files.
5. **Handoff doc links in CHANGELOG/TODO are intentionally broken** on `main`. Do not "fix" by silently committing handoff files.
6. **Warnings channel** introduced in Phase 3 (`Config.Warnings`, printed by `LoadConfig` to stderr). Use the same channel for any future non-fatal deprecation notices.
7. **`ResolvedParams ProfileParams`** on `ServerState` is now actively consumed by `LoadProfile`'s drift check. Stay backward-compatible with state files that lack it (zero-value `ProfileParams` produces a drift notice naming every set-vs-unset field — annoying but harmless and resolves itself on next activation).

## How to test Phase 5 manually

Short version:

- **No drift**: `llama-launcher load chat-qwen` twice in a row → first call activates, second is a silent no-op.
- **Drift**: load a profile, then edit (say) `context_size` in `config.yaml`, then `llama-launcher load chat-qwen` again → stderr prints `Notice: profile "chat-qwen" already active at 127.0.0.1:8080, but its parameters have drifted:` followed by `context_size: 8192 → 16384` and the `--restart` hint. Exit code 0.
- **Forced restart**: `llama-launcher load chat-qwen --restart` (or `-r` / `--force`) after a drift notice → stop-then-start cycle (or unload-then-load for external backends), new params take effect.
- **Per-address scoping**: same profile activated on `:8080` and `:8081` (different host/port overrides) → neither triggers the other's idempotency check.

## Suggested skills

- **`handoff`** — when wrapping up Phase 6 or the next chunk of work, write the next handoff. Filename will be `2026-05-XX - NN - ...md` (counter resets each day; next on this same day would be `2026-05-26 - 05 - ...md`).
- **`feature-implementation`** — only invoke if the user explicitly asks to do Phase 6. The rename is mechanical and may not need the full read-plan-code-review flow; consider whether `simplify` or a direct edit pass is more appropriate.
- **`coding-standards`** — consult before writing Go code if uncertain about style.
- **`brew-release`** — once the user is happy with the unpushed commits and ready to ship 1.3.0, they may want to push, tag, and release.

## Quick env recap

- Repo: `/Users/airic/Repos/llama-launcher`
- Platform: macOS (Apple Silicon)
- Go module: `github.com/airiclenz/llama-launcher`
- Build: `make build` / `make install`
- Tests: `go test ./...`
- Memory store: `/Users/airic/.claude/projects/-Users-airic-Repos-llama-launcher/memory/`
