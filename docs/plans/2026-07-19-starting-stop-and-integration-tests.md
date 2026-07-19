# Plan: Starting-instance stop/visibility (ADR-0010) + integration-test layer

**Date:** 2026-07-19 · **Source:** `docs/handoffs/2026-07-19 - 02 - still-loading-stop-design-and-integration-tests.md`
**Design authority:** [ADR-0010](../adr/0010-starting-instances-are-visible-and-stoppable.md) (written in the design session that produced this plan) and the **Starting** term in [CONTEXT.md](../../CONTEXT.md). All design branches were resolved with the user; no `needs-design-call` items remain.

**Conventions for every code item:** follow the `coding-standards` skill; no AI attribution anywhere; CHANGELOG + TDD + README updated with any behaviour change (Items 9, 16, 17 collect these — individual items note what they owe).

**Resolved decisions (context for implementers):**

1. A Starting (503) `llama-server` is stoppable by explicit `stop`; `unload` on a managed backend reduces to that stop.
2. Visibility is discovery-wide: `RunningInstance` gains a Starting state; `status`/TUI/target-resolution all see it.
3. Identification stays mandatory: `identifyBackend` gets a `StartingUp` second pass (weaker 503 discrimination consciously accepted); signal-whatever-listens was rejected (ADR-0006 foreign-occupant refusal survives).
4. `load` displaces a Starting occupant **only** with `--restart`; plain load refuses with guidance pointing at `llml stop` / `--restart` (no more `kill <PID>`).
5. The `auto_stop_server: true` sweep stops Starting instances at other addresses.
6. Stop verification counts a surviving 503 answer as still-reachable (closes a false-success gap).
7. Integration tests: implementer verifies compile + fake/httptest suites only; **the user runs `make test-integration` on the host** and reports back.
8. `make install` stays a deliberate brew pointer; README/TDD claims about `~/.local/bin` are the bug (Item 16).

---

## Part A — ADR-0010: Starting instances (do first; Part B Item 12 depends on it)

- [x] **1. Discovery reports Starting instances** — `internal/launcher/discovery.go` — ✅ DONE (2026-07-19)
  - Add `Starting bool` to `RunningInstance` (field comment: see ADR-0010).
  - `probeInstance` (discovery.go:112): when `HealthCheck` fails, check `b.(StartupProber)` and `StartingUp(addr)`; on true return an instance with `Starting: true`, skipping `ListRunningModels` (the server cannot answer) — `ActiveModel`/`ActiveProfile` stay empty (`matchProfileName` with an empty model and several profiles sharing the address is ambiguous anyway; empty is correct).
  - `instancesSignature` (discovery.go:146) must include the Starting flag so the menu's refresh tick notices the Starting→healthy transition.
  - Unit test with an httptest server answering 503 `/health` (Layer-1 style, `addrFromURL` helper already exists in the package).

- [x] **2. Stop path identifies and verifies Starting occupants** — `internal/launcher/server.go` — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): tests are not "via the ADR-0009 seam" as the item says — `identifyBackend`/`stopServerAt` are the real mechanics *beneath* the seam (the seam's `stop` op is `StopInstance` itself), so the seam cannot reach them. They are tested in the package's established Layer-1 style instead: an httptest 503 server for the identification second pass, and a registry stub (`startingStopServer`) plus a real signalled child listener (`nc`, skipped if absent) for the verification rule; the PID assertion runs the real lsof/SIGTERM path. Also introduced the package helper `startingUp(b, addr)` (type-assert-and-probe) shared by both passes — Item 3's `realOps.starting` can delegate to it.
  - `identifyBackend` (server.go:183): keep the healthy pass; add a second pass over registered backends implementing `StartupProber`, returning the first whose `StartingUp(addr)` is true. Iterate in sorted-name order in both passes (map order is random; today only llamacpp implements the interface, but determinism is free).
  - `stopServerAt` (server.go:198): the post-stop "did it die" check is currently `b.HealthCheck(addr) != nil` → success. A survived Starting server also fails the health check (still 503) and would be reported as stopped. New rule: stopped ⇔ not healthy **and** not `StartingUp` (only consult `StartingUp` when the backend implements `StartupProber`).
  - Fake-driven tests via the ADR-0009 seam (`server_test.go`): stop of a Starting instance succeeds and reports the PID; a survived-503 server yields the "still reachable" error.

- [x] **3. Orchestration seam learns "starting"** — `internal/launcher/server.go` — ✅ DONE (2026-07-19)
  - Add `starting(b LLMServer, addr string) bool` to `activationOps` (server.go:331) with a one-line `realOps` delegation (per ADR-0009, `realOps` stays logic-free — put the type-assert-and-probe in a package function).
  - Extend the fake in `server_test.go` accordingly.

- [x] **4. Activation: refuse plain load onto a Starting occupant, displace with `--restart`** — `internal/launcher/server.go` — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): `restart` is passed into `loadProfileManaged` alongside `healthy`/`starting` — the `starting && !restart` refusal lives there, so the flag has to travel too (the item's signature note only names `starting`). `startupTimeoutErr` got the `llama-launcher stop <backend>` guidance but **no** `--restart` hint: after a timeout of one's own load, displacement is not the likely intent (the item's "where displacement is the likely intent" conditional), and a plain retry-once-healthy is the documented path. Pre-ADR-0010 tests asserting the old behaviour were updated, not just added to: `TestLoadProfile_RefusesDoubleSpawnWhileStartingUp` now pins plain-load refusal plus `--restart` stop-first with the survived-occupant race backstop (its recorded stop leaves the 503 server alive, so `startManagedServer`'s guard fires), and the two `kill 4242` message assertions now expect `llama-launcher stop llamacpp`. TDD/CHANGELOG/README updates deferred to Item 8 (owns the ADR-0010 docs pass).
  - `loadProfile` (server.go:412) computes `starting := ops.starting(b, targetAddr)` alongside `healthy` and passes it into `loadProfileManaged` (signature change; external path is unaffected — no external backend has a Starting window).
  - `loadProfileManaged` (server.go:675): if `starting && !restart` → return the refusal (hoisted, testable orchestration decision); if `starting && restart` → stop first (same branch as `healthy`), then start. The in-`startManagedServer` `StartingUp` guard (server.go:50) **stays** as the race backstop.
  - Message updates: `stillStartingUpErr` (server.go:765) and `startupTimeoutErr` (server.go:755) drop `kill <PID>` in favour of `llama-launcher stop <backend|host:port>`, plus "`--restart` to replace it" where displacement is the likely intent.
  - Auto-stop sweep: no code change expected — `ops.discover` now returns Starting instances and the existing loop stops them (decision 5). The same-addr-same-backend skip rule keeps the target occupant out of the sweep; its fate is the `loadProfileManaged` branch above.
  - Fake-driven tests: plain load (same profile / different profile) onto a Starting addr refuses; `--restart` stops-then-starts; `auto_stop_server: true` sweeps a Starting instance at another address; `auto_stop_server: false` + `auto_unload: true` does **not** try to unload a Starting instance (`shouldCrossServerUnload` already requires `ActiveModel != ""`, which is empty on Starting instances — add the regression test, not code).

- [x] **5. CLI surfaces** — `internal/launcher/cli.go` — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): ADR-0010's consequences line says "`list --json`" surfaces the Starting state — implemented on `status --json` (the new `"starting"` key) instead, because `list --json` enumerates configured profiles and carries no runtime state; the item's "`list --json` (profiles) is unaffected" is the resolved reading of that ADR slip. The details block renders for Starting instances with a `Starting: <backend>` lead segment (the item only says it "works"; a Starting instance has no profile/model to put after `Active:`). `cmdStop`/`resolveTargetInstance` verified by CLI-level test as required — no code change was needed. TDD/CHANGELOG/README updates (incl. the §3 `status --json` key-list row, which now owes `starting`) deferred to Item 8 per Item 4 precedent.
  - `cmdStatus` (cli.go:361): Starting instances render as `starting…` instead of `running` (agreed mock: `● llama.cpp  starting…  127.0.0.1:8080`); the details block (PID/log via `fillRuntimeDetails`) works — lsof finds a Starting server's PID.
  - `cmdStatusJSON` (cli.go:495): keep `running` meaning healthy; add `"starting"` bool to the entry struct. `list --json` (profiles) is unaffected.
  - `cmdStop`/`resolveTargetInstance` (cli.go:281,326): should need no change — discovery now surfaces the instance. Verify with a CLI-level test rather than assuming.
  - `cmdUnload` (cli.go:147): the `ActiveModel == ""` gates currently exclude Starting instances. A Starting **managed** instance is a valid unload target (unload = stop, ADR-0003/0004): accept it in both the profile-arg path (cli.go:158) and the no-arg enumeration (cli.go:166), labelled `(starting…)` in the multiple-targets listing.
  - Exit-code semantics unchanged.

- [x] **6. TUI menu** — `internal/launcher/menu.go` — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): the item's cited sub-lists got the `starting…` label (header via `serverStatusLines`, stop sub-menu via the new `stopTargetItems` helper) plus one site beyond the cited lines: `runIdleMenuSimple`'s `Status:` line, which otherwise mislabels a Starting primary as "running (no model)". The LoadProfile-refusal "verify, don't assume" is pinned by a test (`TestDoLoadProfile_RefusesStartingOccupant` — menu funnel `doLoadProfile` against a real 503 httptest server), not just code trace. `doUnloadModel` (menu.go) deliberately keeps enumerating only model-loaded instances: the item names stop as the menu verb for Starting instances and its "Unload model" entry only exists in the loaded menu; CLI unload parity was Item 5's scope. TDD/CHANGELOG/README updates deferred to Item 8 per Item 4/5 precedent.
  - The running-instances header/sub-lists (menu.go:27,99,285) must show Starting instances (`starting…` label) and offer **stop** for them; model-swap/load actions against a Starting address go through the same refusal as the CLI (they call `LoadProfile`, so this should fall out of Item 4 — verify, don't assume).
  - Menu refresh: covered by the `instancesSignature` change in Item 1.

- [x] **7. MCP adapter passthrough check** — `cmd/llama-launcher-mcp/` — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): passthrough confirmed — the adapter parses nothing (exit-code-keyed `run`, unchanged by Item 5's "exit-code semantics unchanged"); no behavioural code change. But the "no code change" expectation didn't fully hold: four tool Description strings in `main.go` contradicted the new state and were reworded per the item's own "adjust wording where it does" branch — `server_status` (a Starting entry, `running=false` + non-empty address/PID, would read as an "idle enabled backend" under the old text; key list now includes `starting`), `stop_server` and `tail_log` ("a running server"/"exactly one server is running" denied Starting targets and mis-stated the auto-select set, which is discovery-wide since Item 5), and `unload_model` ("optional when only one model is loaded" is false both ways now: one loaded model + one Starting instance is ambiguous, one Starting instance alone auto-selects). One-word comment fix in `validate.go` ("running" → "discovered" instance) rides along. TDD §15 needs nothing (its tool table carries no state semantics); CHANGELOG mention of the description updates owed to Item 8's ADR-0010 entry, per Items 4–6 precedent (cf. 1.4.6's `start_server`-description line).

- [x] **8. ADR-0010 docs pass** — `llama-launcher.TDD.md`, `CHANGELOG.md`, `README.md` — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): CHANGELOG entries sit under `## Unreleased`, not a next-version header — Item 18 gates naming the version until the host integration run is green. README contained no `kill <PID>` text to update; it instead gained a Starting-state paragraph after the CLI-commands block (Items 4–6's deferred README debt). ADR-0010's consequences line "`list --json`" corrected to "`status --json`" (Item 5's recorded slip). The TDD pass went beyond the item's cited §6.2/§6.5/table-row: §3.1 (TUI `starting…`), §3.2 rows (load/unload/stop/status incl. the `starting` JSON key), §5.3 (`StartupProber` fallback uses), §6.1 (probe/sweep/displacement steps), §6.4, §6.6, §7.1–7.2 (discovery `Starting` field/fallback), and §15's "single running instance" → "single discovered instance" — carrying Items 4–7's deferred doc debts.
  - TDD §6.2 (start refusal, ~line 592) and §6.5 (stop sequence, line 619): document the Starting state, the second identification pass, the stop-verification rule, and the `--restart` displacement; update the error-handling table row at TDD:765 (guidance no longer says "kill the PID" — it says `llml stop`).
  - CHANGELOG: behaviour-change entry under the next version ("a still-loading llama-server is now visible in status and can be stopped/replaced; stop verification hardened").
  - README: update any stop/status examples or troubleshooting text that mention the `kill <PID>` workaround.

## Part B — Integration-test layer (Layer 2 of `backend-tests-plan.md`)

- [x] **9. Re-point the stale plan doc** — `backend-tests-plan.md` — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): the replacement span is wider than the literal "## Layer 2" section — the trailing "Makefile Changes", "Files to Create/Modify", and "Verification" sections were the same superseded Layer-2 spec (Items 14–15 and this plan's Verification own them now) and folded into the pointer; the Layer-1 rows they carried were already fully listed in the kept Layer-1 section. A one-line Status blockquote was also added under the H1 so the doc is re-pointed at first glance.
  - Its Layer-2 section (2026-05-20) predates the `Backend`→`LLMServer` rename, ADR-0009, and the unified `Stop`/`Unload` entry points. Replace that section with a short pointer to this plan (Items 10–15 are the validated spec). Leave Layer 1 (shipped) as historical record.

- [x] **10. Shared integration helpers** — `internal/launcher/integration_test.go` (new, `//go:build integration`) — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): the item names the helpers but not their semantics; the resolved readings are recorded here. `waitForUnhealthy` applies the ADR-0010 stop-verification rule — gone ⇔ not healthy **and** not `startingUp` (a surviving 503 answer counts as still reachable), reusing the package's `startingUp` helper. `mustFindBinary` returns the LookPath-resolved path; `freePort` returns an `int` port (callers compose the `127.0.0.1:<port>` addr). The per-test `t.TempDir()` log-dir and no-`t.Parallel()` rules are conventions for Items 11–13's files, documented in this file's package comment. Layer-2 CHANGELOG/TDD debt stays with Item 15.
  - `mustFindBinary(t, name)` (LookPath + `t.Skip`), `freePort(t)`, `waitForHealthy(t, b, addr, timeout)`, `waitForUnhealthy(...)` — all `t.Helper()`. Per-test `t.TempDir()` log dirs. No `t.Parallel()` anywhere in the integration files.

- [x] **11. llamacpp lifecycle** — `internal/launcher/integration_llamacpp_test.go` (new, build-tagged) — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): resolved readings beyond the item's literal text. Stop-while-Starting **skips** (not fails) when the model turns healthy before a 503 is ever observed — a too-fast model leaves nothing to test and the item doesn't name that case; the skip message says to use a larger model. Cleanup SIGKILLs the process group as well as the PID (Setsid gives the child PGID == PID — a superset of the item's "SIGKILL by PID", matching `terminatePID`). Port release is asserted by an explicit `net.Listen` re-bind probe after `waitForUnhealthy`. The lifecycle log dir is the parent test's `t.TempDir()` (still per-test per Item 10's convention) because the server outlives the `start` subtest; later steps are chained on each subtest's result so a failed step aborts the rest. Layer-2 CHANGELOG/TDD debt stays with Item 15 per Item 10's note.
  - Requires `INTEGRATION_MODEL_LLAMACPP` (absolute .gguf path); skip the whole test if unset.
  - **Delta from the 2026-05-20 plan:** drive the real in-package start path (`StartServer` with a constructed `*Config` + `ResolvedProfile`) instead of re-implementing `exec.Command` — this exercises Setsid, the reaper goroutine, and the startup-grace crash detection for real.
  - Subtests in order: start → `waitForHealthy` → `Stop(addr)` → `waitForUnhealthy`.
  - **New scenario (depends on Part A): stop-while-Starting** — start with the real model, poll until `StartingUp(addr)` is true (llama-server answers 503 while loading), call `Stop(addr)` mid-load, assert success and port release. This is the flagship test connecting both tasks.
  - `t.Cleanup`: best-effort `Stop(addr)` then SIGKILL by PID.

- [x] **12. Ollama lifecycle** — `internal/launcher/integration_ollama_test.go` (new, build-tagged) — ✅ DONE (2026-07-19)
  - NOTES (2026-07-19): the stop step is the unified `Stop(addr)`, not a literal `b.TryStop` call — Ollama's `TryStop` is deliberately a no-op (backend_ollama.go: the address-scoped lsof/PID path in `stopServerAt` does the stopping), so a bare `TryStop` could never make `waitForUnhealthy` pass; `Stop` still runs the `TryStop` hook inside the real stop sequence. Addr binding verified per the item's instruction: `TryStart` exports `OLLAMA_HOST=<addr>` into the child env, so the `freePort` loopback addr isolates the suite from a real instance at 11434 (documented in the file comment). Resolved readings beyond the literal text: the model steps assert their effect (`/api/ps` lists the model after load, with `:latest` normalisation for bare names, and drops it after unload via a bounded poll); step chaining aborts on failure per Item 11's convention; cleanup reuses Item 11's `killServerOnCleanup` (its comment generalised from "llama-server" to "server" — both backends spawn with Setsid). Layer-2 CHANGELOG/TDD debt stays with Item 15 per Item 10's note.
  - `mustFindBinary(t, "ollama")`; sequential subtests: `TryStart` → `HealthCheck` → (`LoadModel` + `ListRunningModels` + `UnloadModel` iff `INTEGRATION_MODEL_OLLAMA` set, model pre-pulled) → `TryStop` → `waitForUnhealthy`.
  - During implementation, verify how `TryStart` binds the requested addr (OLLAMA_HOST env vs default) and use a `freePort` addr so the user's real instance at the configured port is never touched.

- [ ] **13. LM Studio lifecycle** — `internal/launcher/integration_lmstudio_test.go` (new, build-tagged)
  - Same shape via `lms`. Document in a file comment: `lms server start` drives the LM Studio **app instance** — running this suite can interfere with an interactive LM Studio session; that is accepted for a manually-invoked host-side suite.

- [ ] **14. Makefile targets** — `Makefile`
  - Add `test` (`go test ./...`), `test-integration` (`go test -tags=integration -timeout 5m -v ./internal/launcher/`), `test-all` (both); extend `.PHONY`. The `install` target is **deliberately** a failing brew pointer — do not touch it (decision 8).

- [ ] **15. Layer-2 docs pass** — `llama-launcher.TDD.md`, `README.md`, `CHANGELOG.md`
  - TDD §12 (Testing): document the two layers, the build tag, the `INTEGRATION_MODEL_*` env vars, and the "user runs on host" convention.
  - Fix the `make install` doc bug: README:33 and README:512 (`# Build + install to ~/.local/bin`) and TDD:844/866 → point at `brew install airiclenz/tap/llama-launcher` / `brew upgrade llama-launcher`, with `make build` for local testing.
  - CHANGELOG entry for the new test targets.

## Part C — Approved one-liner doc fixes (independent, any time)

- [ ] **16. Four approved fixes**
  - TDD line 6: "ADRs 0001–0007" → "ADRs 0001–0010".
  - TDD §11 future-work list (line 780): drop/mark-shipped the "Homebrew formula" bullet (shipped in 1.4.4; §13 already documents it).
  - ADR-0009 (docs/adr/0009-activation-operations-seam.md:19): "The unified `Unload`/`Stop` entry points planned next build on the same seam" → past tense (they exist: `server.go:847,866`).
  - `cmdConfig` usage (cli.go:708): advertise all three subcommands (`validate`, `init`, `reset`), matching the switch at cli.go:715.

## Verification

- [ ] **17. Implementer-side verification (every code item, and finally overall)**
  - `go build ./... && go vet ./... && go test ./...` — the integration files must be invisible to the untagged build (tag check: `go vet -tags=integration ./internal/launcher/` compiles).
  - Fake/httptest coverage listed in Items 1–6 all green.
- [ ] **18. USER-GATED: host integration run** — the user runs `make test-integration` (optionally with `INTEGRATION_MODEL_*` set) on the host and reports results. Do not mark Part B done, tag a release, or update the CHANGELOG's version header before this comes back green. Implementer must **not** start real servers (decision 7).
