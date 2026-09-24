# API-key residue & doc fixes plan

**Goal:** settle the two open API-key questions from the v1.7.0 security-hardening residue, add defense-in-depth key redaction to every log-surfacing path, land the three approved first-run documentation fixes as a committed change, and produce a Gateway go/no-go decision brief from ADR-0013.

**Date:** 2026-09-03
**Status:** unexecuted
**Sized for:** ~200k-context host
**Authoritative sources:**
- `docs/adr/0013-gateway-data-plane-sibling.md` (Gateway design, decided 2026-08-05)
- `TODO.md:31` (append-vs-replace question; the comment it names is `internal/launcher/backend_llamacpp.go:153-158`)
- `docs/reviews/code-review-2026-07-06.md:128-129` and `docs/handoffs/2026-07-19 - 02 - still-loading-stop-design-and-integration-tests.md:75-77` (the log-echo residue)
- llama.cpp behaviour authority: `common/arg.cpp` of the llama.cpp build under test; doc-verified builds are b10068 (`llama-launcher.TDD.md:754`) and b10176 (`CHANGELOG.md:29`)

**Ratified design calls:**
- **Log redaction:** always redact known keys in log output, unconditionally — masks the resolved profile key and any `--api-key` values wherever log text is shown; closes the residue permanently regardless of llama.cpp build. (User, 2026-09-03.)
- **Gateway scope:** decision brief only — the plan ends with a one-page go/no-go brief written from ADR-0013; no Gateway code. (User, 2026-09-03.)
- **Doc fixes:** the three first-run corrections (README Quick start, TDD §4.1, TDD §11 error table) are already present as uncommitted working-tree edits, verified against `internal/launcher/cli.go:37-58`; item 5 commits them. (Refocus run verification, 2026-09-03.)

**Regression check (2026-09-03, 1647f7b4):**
- 1: recast — owns every stale api-key-semantics prose site (grep guard `override still wins|only when no --api-key`); second skip condition (`INTEGRATION_MODEL_LLAMACPP`); vet now `-tags=integration`; handoff edit local-only, TODO.md the tracked record.
- 2: recast — keys from the instance's own backend, `[redacted]` re-implemented in redact.go, `TailLog` streams through a redaction-interposing `tailLogTo`; supersedes `docs/plans/archived/code-review-fixes-2026-07-06.md:339-345`.
- 3: guard folded — depends on item 2 per writer decision (the brief cites item 2's redaction module as prior art).
- 4: guard folded — depends on item 1 per writer decision; porcelain acceptance reworded (untracked plan file expected); guard extended to the §3.3 "missing file" row.
- 1 (recheck): guard folded — writer decision: skip/build-detect extracted into untagged package-level helpers in `internal/launcher/server_integration_unit_test.go`; the integration test and the untagged unit test both call the helpers, neither references `mustFindBinary`/`integrationLlamaCppModel` directly.
- 2 (recheck): guard folded — writer decision: `TailLog` gains a trailing `keys []string` parameter, every call site passes `cfg.APIKeyFor(inst.Backend)`, no no-keys wrapper survives (replaces the round-1 `tailLogTo` shape).

**Standing requirements:**
- skills: coding-standards
- Any authorized deviation from item text lands as a dated NOTES line under the item.

**Out of scope:** Gateway implementation; multi-host/federation; version bumps or release tags; changes to ADR-0002 or the CLI listener rule; any changes to how keys are stored or fetched.

## 1. Empirically settle the two llama-server API-key questions — ✅ DONE (2026-09-24)

NOTES (2026-09-24): observed on llama-server b10851 (commit 67672dc5b) with INTEGRATION_MODEL_LLAMACPP set to the LM Studio bundled nomic-embed-text-v1.5.Q4_K_M.gguf: (a) no echo of the LLAMA_API_KEY value or an --api-key argv value in the server log; (b) append — both keys answer 200 on /v1/models, a wrong key 401. Matches common/arg.cpp (env handlers run first, CLI --api-key handler push_back()s onto the same api_keys vector).
NOTES (2026-09-24): deferred — CHANGELOG.md:17 (released 1.7.0 Security entry) still says "override still wins": the implementer protocol forbids editing the CHANGELOG file; the correction is carried by this sidecar's CHANGELOG entry for the closeout to apply.
NOTES (2026-09-24): the test reports answers (a) and (b) via t.Logf (visible with -v) and asserts only the invariants the launcher relies on (configured key 200, extra_args key 200, wrong key 401, env key beside an override either 200 or 401); a future build that echoes keys or switches to replace would not fail the suite — item 2's unconditional redaction covers the echo case.
NOTES (2026-09-24): the tagged test reuses the tagged suite helpers freePort, waitForHealthy, killServerOnCleanup, llamaCppBackend and llamaCppHealthyTimeout; it does not reference mustFindBinary or integrationLlamaCppModel.
NOTES (2026-09-24): TODO.md:31 left unticked (dated note added under it; the item is listed under CLOSES for the closeout instead of ticking the register).
NOTES (2026-09-24): the docs/handoffs/ 2026-07-19 - 02 handoff residue line was updated in place (local-only, gitignored — not in FILES).

**What:** Recast at the regression check (2026-09-03). Add one integration test (build tag `integration`, following the existing `make test-integration` convention, skipped when no `llama-server` on PATH) that starts a llama-server via the launcher's own launch path and answers: (a) does the server's own log file contain the `LLAMA_API_KEY` value or a `--api-key` argv echo — feed a sentinel key and grep the log; (b) when `extra_args` supplies `--api-key OTHER`, are BOTH keys valid (llama.cpp appends) or only the extra-args key (env ignored)? Assert against live behaviour, not the comment's claim. Write the answers as dated notes into `TODO.md:31` (append-vs-replace) and under the residue line the 2026-07-19 handoff points at, replacing "possibly-open" with the observed result and the tested build string (read the build from `llama-server --version` at test time; never hard-code one). If the build under test appends, state explicitly in the TODO note that both keys are valid — this is the fact item 2's redaction guards against.

**Files:** `internal/launcher/server_integration_test.go`, `internal/launcher/server_integration_unit_test.go`, `internal/launcher/backend_llamacpp.go`, `internal/launcher/backend_llamacpp_test.go`, `TODO.md`, `README.md`, `CHANGELOG.md`, `llama-launcher.TDD.md`
**Tests:** the new integration test; `go test -tags=integration -count=1 -run TestLlamaServerAPIKey -timeout 5m ./internal/launcher/`; the untagged skip/build-detection unit test (runs in the plain `go test ./...` unit pass)
**Acceptance:** `go vet -tags=integration ./internal/launcher/` (plain `go vet` does not type-check the tagged file); the test skips cleanly (not fails) when `llama-server` is absent or `INTEGRATION_MODEL_LLAMACPP` is unset; its skip/build-detection path covered by a unit test that runs without the tag.
**Commit:** `test(llamacpp): settle api-key log-echo and append-vs-replace questions empirically`
**Depends on:** nothing.
**Regression guard.** Item 1 owns every stale api-key-semantics prose site, not just TODO.md — update the override-wins comment at internal/launcher/backend_llamacpp.go:153-155, the doc comment in backend_llamacpp_test.go (~:232-235), README.md:127, CHANGELOG.md:17, and llama-launcher.TDD.md:371 per the observed result; guard by grep rule `override still wins|only when no --api-key` across *.go, README.md, CHANGELOG.md, llama-launcher.TDD.md — any site left unupdated is a dated NOTES deferral, and **Files:** grows accordingly. Second skip condition: also skip when INTEGRATION_MODEL_LLAMACPP is unset, via the extracted untagged helper (not `integrationLlamaCppModel` directly). Acceptance's vet must be `go vet -tags=integration ./internal/launcher/` (plain go vet does not type-check the tagged file). The docs/handoffs/ edit is local-only by convention (gitignored); TODO.md is the tracked record. Extract the skip/build-detect decisions into untagged package-level helpers hosted in `internal/launcher/server_integration_unit_test.go` (untagged package symbols are visible to `//go:build integration` files) — the new integration test and the untagged unit test both call those helpers; neither test references `mustFindBinary` or `integrationLlamaCppModel` directly. The launcher's own launch path must still be exercised, not a hand-rolled `exec`: assert the test starts the server through `startManagedServer` (or the same `BuildServerArgs`/`BuildServerEnv` composition); existing `backend_llamacpp_test.go:218-228` already guards that `BuildServerArgs` never emits `--api-key` on its own — keep it green.

## 2. Redact known API keys in all log-surfacing paths — ✅ DONE (2026-09-24)

NOTES (2026-09-24): the cmdLogs-level and TUI-seam tests live in `internal/launcher/redact_test.go` (a **Files:** entry) rather than cli_test.go/menu_test.go, which the item does not list; the TUI seam is a new `showInstanceLog(cfg, inst, follow)` in menu.go that the three Show log sites call — it sources keys from `inst.Backend`, it is not a no-keys wrapper.
NOTES (2026-09-24): `TailLog` now pipes `tail` through `RedactLogText` line by line (`copyRedactedLines`), so in follow mode a partial line the server has not yet terminated with a newline appears once its newline lands.
NOTES (2026-09-24): the flag-pair pattern also covers `--api-key=V`, quoted and `"--api-key", "V"` list-element spellings, and deliberately leaves `--api-key-file PATH` alone; keys are masked longest first.
NOTES (2026-09-24): MCP `tail_log` verified to shell to `logs [target]` (cmd/llama-launcher-mcp/main.go) and so inherits cmdLogs' redaction — no adapter change.
NOTES (2026-09-24): consequential edit — README.md: made necessary by the new user-visible log redaction (api_key section).
NOTES (2026-09-24): consequential edit — llama-launcher.TDD.md: made necessary by redact.go (file map rows for redact.go/redact_test.go, §6.2 step 8 crash tail, :371 key-semantics sentence, server_test.go row).

**What:** Recast at the regression check (2026-09-03). One deep module — `internal/launcher/redact.go`, func `RedactLogText(text string, keys []string) string` — that masks (a) every resolved profile key passed in and (b) any value following a `--api-key` flag occurrence, re-implementing the `[redacted]` masking convention of `internal/keystore/keystore.go:234` (`redactKey` is unexported — do not import it) and the flag-pair logic of `internal/launcher/backend_http.go:159-172` (`redactAPIKeyArgs`). Wire it at every point log TEXT reaches a user (grep rule: every caller of `TailLog` or `readLastLines`): `cmdLogs` (`internal/launcher/cli.go:711`), the three TUI log views (`internal/launcher/menu.go:298`, `:343`, `:1025`), the start-crash tail (`internal/launcher/server.go:143-144`), and the MCP `tail_log` path (`cmd/llama-launcher-mcp/main.go:106-113`, which shells to the CLI and inherits the fix — verify, do not re-implement). Callers source the resolved key from the instance's own backend (`cfg.APIKeyFor(inst.Backend)` / `profile.Backend`; empty list when none) — never the literal `"llamacpp"`, since `menu.go:318` offers Show log for any backend. Binding call: redaction is unconditional (user, 2026-09-03).

**Files:** `internal/launcher/redact.go`, `internal/launcher/redact_test.go`, `internal/launcher/cli.go`, `internal/launcher/menu.go`, `internal/launcher/server.go`, `internal/launcher/server_test.go`
**Tests:** unit tests for `RedactLogText` (key in plain text, in a `--api-key X` pair, multi-line, empty-keys no-op scoped to text with no flag pair, plus a case asserting keys=[] still masks the value after `--api-key`); a `cmdLogs`-level test asserting the sentinel key string never appears in output; a `readLastLines` caller test for the crash-tail path; one test at a TUI log-view seam asserting the same. Existing `TestLlamaCppBuildServerEnv` (`backend_llamacpp_test.go:236`) must stay green.
**Acceptance:** `go test ./internal/launcher/ ./cmd/llama-launcher-mcp/`; `go vet ./internal/launcher/ ./cmd/llama-launcher-mcp/`.
**Commit:** `feat(logs): redact known api keys on every log-surfacing path`
**Depends on:** nothing (item 1's TODO note documents why this is permanent).
**Regression guard.** RedactLogText re-implements the `[redacted]` masking convention in redact.go — internal/keystore redactKey is unexported and must not be imported. Callers source keys from the instance's own backend (inst.Backend / profile.Backend passed to cfg.APIKeyFor(backend)), never the literal "llamacpp" — menu.go:318 offers Show log for any backend. `TailLog` gains a trailing `keys []string` parameter; `cli.go:711` and `menu.go:298/343/1025` pass `cfg.APIKeyFor(inst.Backend)` (empty slice when none); `readLastLines` callers likewise; fold or delete any no-keys wrapper so no unredacted entry point survives. Redaction still sits in `RedactLogText`; call sites only pass keys. `readLastLines` redacts at its one production call site (`internal/launcher/server.go:143`). The flag-pair rule stays unconditional regardless of keys: scope the "empty-keys no-op" test to text with no `--api-key` pair and add a case asserting keys=[] still masks the value after `--api-key`; pin one marker — `[redacted]` for both resolved keys and flag-pair values (`internal/keystore/keystore.go:236`; `backend_http.go`'s `***` stays display-only). This deliberately supersedes `docs/plans/archived/code-review-fixes-2026-07-06.md:339-345` (2026-07-06: tail_log "no leak, no code change", empirical against build 9870). Any NEW caller of `TailLog`/`readLastLines` added later must redact: state the rule in a comment at each of the two functions ("callers must pass the profile's resolved keys; redaction sits in RedactLogText") and add a grep-able marker `// redaction-required` next to each, so the rule is findable.

## 3. Gateway go/no-go decision brief — ✅ DONE (2026-09-24)

NOTES (2026-09-24): the brief recommends "Go, gated on design" (settle API-key handling on the proxy path and defaults for stickiness window N and the memory budget before code) and offers Go / No-go / Defer as the explicit decision request; the recommendation is the implementer's judgment and awaits the user's decision.
NOTES (2026-09-24): no CHANGELOG entry — docs-only internal decision brief, no user-facing behaviour change.

**What:** Write `docs/adr/0013-gateway-decision-brief.md` — one page, prose, no code: what the Gateway is (per ADR-0013), the concrete cost (new resident binary, queueing/stickiness config surface, ADR-0002 exception scope), the concrete benefit (OpenAI-compatible access for ordinary AI tools through Profiles), the open items ADR-0013 leaves to implementation (stickiness window N, memory budget, per-Profile footprint declaration, ADR-0008 allowlist as prior art for non-loopback trust — and note ADR-0013 is silent on API-key handling for the proxy path, cross-referencing item 2's redaction module as prior art), a recommendation, and an explicit decision request. Statement of the launcher's current alternative (MCP adapter) for comparison. No version numbers, no roadmap dates.

**Files:** `docs/adr/0013-gateway-decision-brief.md`
**Tests:** none (docs-only).
**Acceptance:** the brief exists, is ≤ 1 page rendered, cites ADR-0013 by path, and its claims about the MCP adapter match `cmd/llama-launcher-mcp/validate.go:38-43`.
**Commit:** `docs(adr): gateway go/no-go decision brief from ADR-0013`
**Depends on:** item 2 (execute item 2 first — the brief cites its redaction module as prior art).
**Regression guard.** Depends on item 2 — the brief cites item 2's redaction module as prior art, so it must exist first. The brief must not contradict ADR-0013: every design claim traces to a line in `docs/adr/0013-gateway-data-plane-sibling.md`; guard by re-reading the brief against the ADR before commit.

## 4. Verify and commit the first-run documentation fixes

**What:** The three approved first-run fixes already sit uncommitted in the working tree (verified against `internal/launcher/cli.go:37-58` during the 2026-09-03 refocus run): `README.md:43,47` (Quick start — first run reloads and opens the menu), `llama-launcher.TDD.md:194` (§4.1 — continues, not exits), `llama-launcher.TDD.md:808` (§11 error table — no exit). Verify each edited line still matches current code behaviour (re-read `internal/launcher/cli.go:37-58`), then commit exactly these two files and nothing else.

**Files:** `README.md`, `llama-launcher.TDD.md`
**Tests:** none (docs-only).
**Acceptance:** `git status --porcelain` shows no modified tracked files after commit (the untracked plan file remains until closeout commits it); the committed diff contains only these two files and only the first-run wording changes.
**Commit:** `docs: first run reloads config and continues instead of exiting`
**Depends on:** item 1 (item 1's prose updates touch README.md and llama-launcher.TDD.md; item 4 commits only the first-run hunks remaining after item 1's commit).
**Regression guard.** Depends on item 1 — item 1's prose updates touch README.md and llama-launcher.TDD.md; item 4 commits only the first-run hunks remaining after item 1's commit. Porcelain acceptance: `git status --porcelain` shows no modified tracked files; the untracked plan file remains until closeout commits it. The §11 error-table row must stay consistent with §4.1's wording: grep both files for "exit 2" after the edit, and also `grep -n "missing file" llama-launcher.TDD.md` — the §3.3 code-2 row (llama-launcher.TDD.md:183) names "missing file" as exit 2 without the literal "exit 2"; amend that row to drop the first-run claim (e.g. "Configuration error (parse error, unknown profile; first-run missing file continues — §4.1)"). Any remaining first-run exit claim is a missed site, not out of scope.
