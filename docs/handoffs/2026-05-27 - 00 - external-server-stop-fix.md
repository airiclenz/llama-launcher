# Handoff — `stop` works for untracked / externally-started servers

**Date:** 2026-05-27
**Branch:** `main`
**Prior handoff:** [2026-05-26 - 05 - phase 5 plus header fix handoff.md](2026-05-26%20-%2005%20-%20phase%205%20plus%20header%20fix%20handoff.md) — state of `main` after Phases 1–5 + the menu-header fix.

## What this session was

Two unrelated chunks of work landed:

1. **Handoff archive cleanup** — committed in `09e84f4` ("Archive superseded phase handoffs (00–04)"). Five superseded handoffs moved from `docs/handoffs/` → `docs/handoffs/archive/`. Only `2026-05-26 - 05 - …` remains in the active folder. Git recognised all five as pure renames. The archive folder is `archive/` (singular) because that empty folder already existed; the skill's prescribed name is `archived/` (plural) but I went with the existing one for consistency. **Decision worth carrying forward**: if you ever add more archives, keep using `archive/` unless the user wants to rename.

2. **Stop-for-untracked-servers fix** — the live open question from handoff 05's §"Open questions" #1 ("Legacy-state migration is destructive … orphan-process tracking is open"). Code is **uncommitted**; see [Working tree](#working-tree-right-now) below.

## Where we are

| Phase | Status | Commit |
|---|---|---|
| 1. Documentation alignment | ✅ pushed | `f3b82d2` |
| 2. Cross-server `auto_unload` (ADR-0004) | ✅ committed, **not pushed** | `7ef2d9a` |
| 3. `defaults.server` soft-deprecation (ADR-0005) | ✅ committed, **not pushed** | `a131e28` |
| 4. State schema + multi-instance (ADR-0001 + ADR-0006) | ✅ committed, **not pushed** | `17c5288` |
| 5. Drift notice + `--restart` (ADR-0007) | ✅ committed, **not pushed** | `3ec72fd` |
| 5b. Menu-header regression fix | ✅ committed, **not pushed** | `bed18d5` |
| 5c. VERSION 1.3.0 + handoff trail | ✅ committed, **not pushed** | `fc15905` |
| 5d. Archive superseded handoffs | ✅ committed, **not pushed** | `09e84f4` |
| 5e. Stop-for-untracked-servers | ⬜ **uncommitted on disk** | — |
| 6. `Backend` → `LLMServer` rename | ⬜ optional | — |

Eight unpushed commits on `main`. User pushes themselves.

## Working tree right now

Uncommitted, awaiting user review + commit:

- `internal/launcher/server.go`
- `internal/launcher/menu.go`
- `internal/launcher/cli.go`
- `CHANGELOG.md`

Untracked (local-only, do not commit):

- `.claude/`, `prompts/`
- `docs/handoffs/2026-05-26 - 05 - …md` (left untracked deliberately by the prior session)
- `docs/handoffs/2026-05-27 - 00 - external-server-stop-fix.md` (this file)

`go build ./...`, `go vet ./...`, `go test ./...` all clean.

## The bug and the fix

**Symptom.** User reported: the menu showed llama.cpp running (header green ●) but offered no "Stop server" option. After the menu fix, "Stop server" appeared but the actual stop didn't work — the llama-server process kept running.

**Root cause.** Two layered gaps, both downstream of the destructive Phase 4 legacy-state migration (`17c5288`):

1. **Visibility.** `serverStatusLines` was patched in `bed18d5` to fall back to `cfg.ConfiguredBackendAddr(name)` when no state file exists, so the header lights up for externally-started servers. But `detectRunningServers` (`menu.go:273`) still only iterated `ReadAllStates()` — so the "Stop server" menu item, gated on `len(detectRunningServers(cfg)) > 0`, never appeared.
2. **Termination.** Even once visible, `LlamaCpp.TryStop` (`backend_llamacpp.go:46`) is a no-op (`llama-server` has no CLI stop). Without a state-file PID, the launcher had nothing to signal.

**Fix shape.** Three layers:

- `detectRunningServers` (`menu.go`) now mirrors the header's probe strategy: state-file instances + a fallback probe at `cfg.ConfiguredBackendAddr(name)` for every enabled server without a state record.
- New `EnsureStopped(backend, addr, progress)` in `server.go`: calls `Backend.TryStop`, re-probes, and if still healthy looks up the listening PID via `lsof -nP -iTCP@host:port -sTCP:LISTEN -t` (with a port-only fallback for 0.0.0.0 binds) and signals it. Re-uses the existing SIGTERM → 15s grace → SIGKILL escalation, now extracted into `terminatePID(pid, progress)` so `signalAndWait` is just a one-liner wrapper.
- `stopInstance` itself calls `EnsureStopped` after the state-PID signal step — so even tracked instances benefit when `Backend.TryStop` is a no-op AND the recorded PID has gone stale.
- `cmdStop` in `cli.go` gained the same fallback via a new `resolveUntrackedStopTarget(cfg, target)` helper that mirrors `resolveStopTarget`'s selection rules (host:port, backend name, single-reachable auto-select) but probes configured addresses.

**Diff scope:** ~150 lines added across the three Go files. See the working-tree diff for details; the conversation has the full reasoning.

## Conventions reaffirmed this session

(Unchanged from handoff 05 but worth restating since they came up.)

1. **Don't auto-push.** User pushes themselves.
2. **`.claude/`, `prompts/`, untracked handoffs are local-only.** Always name files; never `git add .`.
3. **CHANGELOG/TDD/README on every behaviour change.** This session updated CHANGELOG; TDD and README didn't need touching (no surface-API change beyond the new `lsof` runtime dep, noted in the CHANGELOG bullet).
4. **Archive folder is `docs/handoffs/archive/`** (singular), per the existing empty folder. The skill's docs say `archived/`; we diverged for consistency.

## Loose ends / open questions

1. **`lsof` is a new runtime dependency.** Standard on macOS and most Linux distros, but worth flagging — `findListeningPID` returns a clear error when `lsof` is absent. Consider mentioning in README's "Requirements" if there is one (none exists yet).
2. **`auto_unload` guard in `loadProfileExternal`** — still open from handoff 05 §"Open questions" #2. Untouched this session.
3. **Phase 6 (`Backend` → `LLMServer` rename)** — still optional, untouched.
4. **`stopInstance`'s extra `EnsureStopped` call** — this is now unconditional after the signalAndWait step. For ollama/lmstudio this means an extra `b.TryStop` invocation that was already happening in the old code path; net cost is one HealthCheck. Acceptable, but worth noting if anyone is auditing stop latency.
5. **No new tests.** The fix touches behaviour reachable only with a live `lsof` and an actual listening process — awkward to unit-test without mocking. The existing `TestWriteAndReadInstanceState` etc. still pass. If you want regression coverage, a TestEnsureStopped could spin up a TCP listener in-process and verify `findListeningPID` returns its PID.

## How to test the fix manually

1. **External llama.cpp, no state file** — start `llama-server` manually (or do anything that leaves a healthy `127.0.0.1:8080` and no state file in `~/.config/llama-launcher/`). Launch the TUI. Header should show `● llama.cpp …`. Menu should include "Stop server". Selecting it should actually terminate the process (SIGTERM, then SIGKILL after 15s if needed). Re-probe header → `○ stopped`.
2. **Same via CLI** — `llama-launcher stop` (or `llama-launcher stop llamacpp` / `llama-launcher stop 127.0.0.1:8080`) should print `Stopped llama.cpp at 127.0.0.1:8080` and the process should be gone.
3. **Tracked instance whose PID has gone stale** — start managed, manually `kill -9` the PID outside the launcher, restart the same llama-server manually on the same port, then `llama-launcher stop` — `EnsureStopped` should find the new PID via lsof and terminate it.
4. **Ollama / LM Studio external** — still work via `Backend.TryStop` without needing PID discovery. Sanity-check that the new code path doesn't regress this.

## Suggested skills

- **`pr-lifecycle`** — if the user wants to push the eight commits + commit the uncommitted fix + open a PR. There's no remote PR workflow established here so a direct push to `main` is more likely.
- **`brew-release`** — natural next step once the fix is committed and the user is ready to ship 1.3.0. VERSION is already at 1.3.0.
- **`coding-standards`** — consult before touching the Go code.
- **`feature-implementation`** — only if starting Phase 6 (rename); the user has flagged Phase 6 as optional and not yet greenlit.
- **`handoff`** — write the next handoff when wrapping up the next chunk. Today's counter so the next one is `2026-05-27 - 01 - …md`; resets to `00` tomorrow.

## Quick env recap

- Repo: `/Users/airic/Repos/llama-launcher`
- Platform: macOS (Apple Silicon)
- Go module: `github.com/airiclenz/llama-launcher`
- Build: `make build` / `make install`
- Tests: `go test ./...`
- Memory store: `/Users/airic/.claude/projects/-Users-airic-Repos-llama-launcher/memory/`
