# Handoff — implement `status --json` and `list --json`

**Date:** 2026-05-27
**Branch:** `main`
**Prior handoff:** [2026-05-27 - 00 - external-server-stop-fix.md](2026-05-27%20-%2000%20-%20external-server-stop-fix.md) — describes the stop-for-untracked fix and the state of main going into Phase 6.

## What this session was

Two chunks of work landed:

1. **Phase 6 — `Backend` → `LLMServer` rename.** Committed in [`350c138`](../../). Mechanical interface rename, no behaviour change. Scope decisions are captured in the commit message and the updated TDD §5.3. String fields on persisted records (`ServerState.Backend`, `Profile.Backend` for legacy YAML detection) were intentionally left in place so on-disk state files and config-migration paths stay compatible. The TODO's "Architecture refactor (ADRs 0001–0007)" section is now fully ticked.
2. **Handoff archival drift.** The two previously-untracked handoffs (`… - 05 - phase 5 …`, `… - 00 - external-server-stop-fix`) were committed in `a9cb7aa`. From now on `docs/handoffs/` is the source of truth; only `.claude/` and `prompts/` stay local-only.

## Where we are

| Item | Status | Commit |
|---|---|---|
| Phase 1–5 (architecture refactor) | ✅ committed | various |
| Phase 5b–5d (header fix, version bump, archive) | ✅ committed | `bed18d5`, `fc15905`, `09e84f4` |
| Phase 5e — stop for untracked servers | ✅ committed | `30ba277` |
| Phase 6 — `Backend` → `LLMServer` rename | ✅ committed | `350c138` |
| Handoffs (00, 05) committed | ✅ committed | `a9cb7aa` |
| **Next:** `status --json` / `list --json` | ⬜ TODO line 27 | — |

`go build ./...`, `go vet ./...`, `go test ./...` all clean on `350c138`. The remote moved during the session — current local branch is 1 commit ahead of `origin/main` (the handoffs commit). User pushes themselves.

## The next task

**Goal:** Add `--json` output to `status` and `list` per `TODO.md` line 27 (which is around line 27 after the Phase 6 tick — verify by `grep -n "status --json" TODO.md`).

A complete brief already exists at `prompts/json-output.md` (untracked, local-only). It is the authoritative spec for this feature and is up-to-date with the current codebase **except** for the `Backend` → `LLMServer` rename:

- The brief still says "a JSON array … each element represents one configured backend". JSON **field names** in the spec (`backend`, `active_profile`, `active_model`, `pid`, `uptime_seconds`, etc.) should stay as written — they match the persisted JSON shape on `ServerState` and the YAML domain in the config. The Go-level helper names in the brief (e.g. references to "backends" in prose) are now `LLM Servers` in the code but the brief's *output shape* does not need to change.
- The brief's "JSON schema for `list --json`" has `"backend": "ollama"` — keep that field name; it mirrors the YAML `server:` key by value and `Profile.Backend` by Go field. (CONTEXT.md and TDD §5.3 explain why these JSON/YAML names diverge from the renamed Go interface — see the bullet at the end of CONTEXT.md's "Flagged ambiguities".)

### Implementation pointers (refer to the brief for full detail)

- `cli.go`: change the `case "status":` and `case "list":` branches to pass `args[1:]`; update `cmdStatus` / `cmdList` signatures to accept `args []string` and parse `--json` by iterating args (matches how `load`'s `--restart` / `-r` / `--force` is parsed today — see `cmdLoad` in `cli.go`).
- Define **local** structs for marshalling. Do not serialize `ServerState` directly (`json.MarshalIndent` would leak unexpected fields if the schema changes) and do not marshal `ResolvedProfile` / `ProfileParams` (pointer fields produce `null`s).
- Exit code parity with the human path: `status --json` exits 0 if any server is running, 1 if all are stopped; `list --json` always exits 0.
- `json.MarshalIndent` errors → stderr + exit 2 (defensive; should never trigger).
- Update `printUsage()` to show `status [--json]` and `list [--json]`.

### Spec-vs-state checks before you start

1. The brief's CHANGELOG bullet says "add under `## 1.2.3`". The CHANGELOG is at `## 1.3.0` (see `CHANGELOG.md` line 3). Add the entry under 1.3.0's `### Added` (creating that subheading if it doesn't exist — current 1.3.0 has Fixed/Changed/Architecture but no Added).
2. The brief's `cli.go` row update in the TDD: today `cli.go` is summarised in `llama-launcher.TDD.md` §5.2 — locate that row and append a note about JSON output.
3. The brief mentions a `Backend` field on the output struct. That's a **JSON field name** — fine to keep as `"backend"`. Don't get confused by the recent Go rename.

## Conventions reaffirmed this session

(Unchanged from prior handoffs but worth restating.)

1. **Don't auto-push.** User pushes themselves.
2. **`.claude/` and `prompts/` are local-only.** Handoffs in `docs/handoffs/` are now committed.
3. **Update CHANGELOG, TDD, and README on every behaviour change.** Skill `feature-implementation` enforces this; treat its Step 4 as mandatory.
4. **No Co-Authored-By lines** in commit messages (global preference).
5. **Don't auto-fix or expand scope** when implementing from `prompts/*.md`. Stick to the brief; the user reviews delegated AI output and will redirect if needed (`feedback_ai_delegation`).

## Loose ends / open questions

1. **`auto_unload` guard in `loadProfileExternal`** — still open from handoff 05's open-questions list. Untouched.
2. **`lsof` runtime dependency.** Documented in CHANGELOG but not yet in any README "Requirements" section.
3. **No `--json` test coverage path is prescribed in the brief.** Consider one round-trip test per command that marshals a known input and checks the JSON parses back into the expected shape. Not required, but cheap.

## Suggested skills

- **`feature-implementation`** — the brief at `prompts/json-output.md` is structured for this skill; the next session should invoke it.
- **`coding-standards`** — consult before writing Go.
- **`pr-lifecycle`** — if you want to ship this as a PR rather than a direct push to `main`.
- **`brew-release`** — once `--json` is in, the 1.3.0 release is ready. VERSION is already at 1.3.0.
- **`handoff`** — write the next handoff when wrapping up. Today's counter goes to `02` if you stay on 2026-05-27; resets to `00` tomorrow.

## Quick env recap

- Repo: `/Users/airic/Repos/llama-launcher`
- Platform: macOS (Apple Silicon)
- Go module: `github.com/airiclenz/llama-launcher`
- Build: `make build` / `make install`
- Tests: `go test ./...`
- Memory store: `/Users/airic/.claude/projects/-Users-airic-Repos-llama-launcher/memory/`
- Existing prompt brief: `prompts/json-output.md` (local-only, not committed)
