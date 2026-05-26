# Handoff — Phase 4 done, Phase 5 next

**Date:** 2026-05-26
**Branch:** `main`
**Prior handoffs:**
- [2026-05-26 - 00 - fit-gap-adrs-vs-code.md](2026-05-26%20-%2000%20-%20fit-gap-adrs-vs-code.md) — phased plan (untracked locally)
- [2026-05-26 - 01 - phase progress handoff.md](2026-05-26%20-%2001%20-%20phase%20progress%20handoff.md) — after Phase 2
- [2026-05-26 - 02 - phase 3 complete handoff.md](2026-05-26%20-%2002%20-%20phase%203%20complete%20handoff.md) — after Phase 3

## Where we are

| Phase | Status | Commit |
|---|---|---|
| 1. Documentation alignment | ✅ pushed | `f3b82d2` |
| 2. Cross-server `auto_unload` (ADR-0004) | ✅ committed, **not pushed** | `7ef2d9a` |
| 3. `defaults.server` soft-deprecation (ADR-0005) | ✅ committed, **not pushed** | `a131e28` |
| 4. State schema + multi-instance (ADR-0001 + ADR-0006) | ✅ committed, **not pushed** | `17c5288` |
| 5. Drift notice + `--restart` (ADR-0007) | ⬜ **next** | — |
| 6. `Backend` → `LLMServer` rename | ⬜ optional | — |

Four unpushed commits on `main` (`7ef2d9a`, `a131e28`, `7f9b43f`, `17c5288`). User pushes themselves — do not push without an explicit ask.

## Working tree right now

- `git status` shows the same pre-existing staged-unrelated deletion: `docs/handoffs/20260526-fit-gap-adrs-vs-code.md` (file was renamed locally; new name is still untracked, CHANGELOG link to the old filename is therefore broken on `main`). **Leave this alone** unless the user asks to fix it.
- Untracked: `.claude/`, `prompts/`, the three handoff files in `docs/handoffs/2026-05-26 - ...md` (00, 01, 02), and this file (03).
- `go build ./...`, `go test ./...`, `go vet ./...` all clean at `17c5288`.

## What landed in Phase 4 (commit `17c5288`)

See the commit message and the diff for details. Highlights:

Code (`internal/launcher/`):
- `server.go`: `ServerState.Managed` removed; `ResolvedParams ProfileParams` added. New filename helper `instanceStatePath(backend, host, port)` — loopback omits host. New API `ReadInstanceState(addr)`, `ReadInstancesForBackend(backend)`, `writeInstanceState`, `removeInstanceState`. `StopBackendServer` → `StopInstance(addr, progress)`; `UnloadBackendModel` → `UnloadInstanceModel(addr, progress)`. The managed/external stop split is gone — `stopInstance` signals PID if alive then calls `Backend.TryStop`. `IsServerAlive` always uses `HealthCheck`. `migrateOldState` now deletes legacy `state.json` and `state-{backend}.json` files (split out into `removeLegacyStateFiles(dir)` for testability).
- `cli.go`: all `state.Managed` branches gone; `stop [target]` and `logs [target]` accept either `host:port` or backend name via new `resolveStopTarget`; `cmdStatus` iterates per-instance state files; usage text updated.
- `menu.go`: `state.Managed` → `state.PID > 0`; `detectRunningServers` and `serverStatusLines` enumerate per-instance via `ReadAllStates`; renders a single `○ stopped` line when no instances exist; pickers (stop/unload) are address-keyed.
- `server_test.go`: `TestBackendStatePath` → `TestInstanceStatePath` (loopback/non-loopback cases). `TestWriteAndReadBackendState` → `TestWriteAndReadInstanceState` (drops Managed assertion). New `TestStateMigration` covering legacy file removal.

Docs:
- `CHANGELOG.md`: four new bullets under `## 1.3.0` (per-instance state, unconditional stop, resolved_params, legacy-file removal).
- `TODO.md`: Phase 4 marked `[x]`.
- TDD did not need changes — Phase 1 already pre-documented the target state in §7, §6.5, §3.2.

## Phase 5 — next up

Phase 5 is **ADR-0007**: idempotent profile activation with a drift notice.

Notes from the fit-gap (see [2026-05-26 - 00 - fit-gap-adrs-vs-code.md](2026-05-26%20-%2000%20-%20fit-gap-adrs-vs-code.md) §Phase 5):
- Read `state.ResolvedParams` at the start of `LoadProfile`. Phase 4 already writes it on activation, so the input is there.
- If the same profile name is already active at the target address and `ResolvedParams` matches the freshly resolved profile → exit silently (already a no-op today via the `state.ActiveProfile == profile.Name` check; tighten the wording in §6.1 step 4).
- If the profile name matches but params have drifted → print a notice to stderr naming the divergent fields and pointing to `--restart`. Exit silently otherwise (still a no-op).
- Add `--restart` / `-r` / `--force` flag to `load` (and to `start --profile`?). When present, fall through past the idempotency check.
- TDD §3.2 and §6.1 already describe the target behaviour. The `Error Handling` table in §10 also lists the drift case.

The comparison function will need a parameter-by-parameter diff that's user-friendly (group like fields, drop nil-vs-nil). Watch out for the `Server` and `Host`/`Port` fields which are the slot identity — if those drift the activation wouldn't have hit the idempotency check in the first place.

## Conventions established (carry forward)

1. **TDD describes target state** — don't add "not yet implemented" caveats to existing sections; the ADRs capture the gap.
2. **Commit style** — short imperative subject, optional body. No AI attribution / Co-Authored-By lines.
3. **Don't auto-push.** User pushes themselves.
4. **`.claude/`, `prompts/`, and untracked handoffs are local-only.** Never `git add .` — always name files.
5. **Handoff doc links in CHANGELOG/TODO are intentionally broken** on `main`. Do not "fix" by silently committing handoff files.
6. **Warnings channel** introduced in Phase 3 (`Config.Warnings`, printed by `LoadConfig` to stderr). Use the same channel for any future non-fatal deprecation notices.
7. **Phase 4 added** `ResolvedParams ProfileParams` to `ServerState` for the upcoming drift check. It's snapshotted at activation in `loadProfileManaged` / `loadProfileExternal` and cleared by `UnloadInstanceModel`.

## How to test Phase 4 manually

Short version:
- **Multi-instance llamacpp**: configure two profiles on different ports (e.g. 8080 / 8081) with `auto_stop_server: false`, load both — both `state-llamacpp-8080.json` and `state-llamacpp-8081.json` should appear in `~/.config/llama-launcher/`. `status` lists both. `stop 127.0.0.1:8080` stops only one.
- **Stop is unconditional**: start `lms server start` manually, then `llama-launcher load <lmstudio-profile>` then `llama-launcher stop lmstudio` — the LM Studio server should actually stop (previously `Managed: false` would have soft-disconnected).
- **Migration**: drop a legacy `state-llamacpp.json` (or `state.json`) into `~/.config/llama-launcher/` then run any command — file should be gone afterwards.

## Suggested skills

- **`feature-implementation`** — same skill used for Phases 1–4. Continue using it for Phase 5. The user is following its Read → Plan → Code → Docs → Self-review structure.
- **`coding-standards`** — consult before writing Go code if uncertain about style.
- **`handoff`** — when wrapping up Phase 5, write the next handoff. Filename will be `2026-05-XX - NN - ....md` (counter resets each day; if same day, next is `2026-05-26 - 04 - ...md`).

## Quick env recap

- Repo: `/Users/airic/Repos/llama-launcher`
- Platform: macOS (Apple Silicon)
- Go module: `github.com/airiclenz/llama-launcher`
- Build: `make build` / `make install`
- Tests: `go test ./...`
- Memory store: `/Users/airic/.claude/projects/-Users-airic-Repos-llama-launcher/memory/`
