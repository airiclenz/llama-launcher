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

- [ ] **2. Stop path identifies and verifies Starting occupants** — `internal/launcher/server.go`
  - `identifyBackend` (server.go:183): keep the healthy pass; add a second pass over registered backends implementing `StartupProber`, returning the first whose `StartingUp(addr)` is true. Iterate in sorted-name order in both passes (map order is random; today only llamacpp implements the interface, but determinism is free).
  - `stopServerAt` (server.go:198): the post-stop "did it die" check is currently `b.HealthCheck(addr) != nil` → success. A survived Starting server also fails the health check (still 503) and would be reported as stopped. New rule: stopped ⇔ not healthy **and** not `StartingUp` (only consult `StartingUp` when the backend implements `StartupProber`).
  - Fake-driven tests via the ADR-0009 seam (`server_test.go`): stop of a Starting instance succeeds and reports the PID; a survived-503 server yields the "still reachable" error.

- [ ] **3. Orchestration seam learns "starting"** — `internal/launcher/server.go`
  - Add `starting(b LLMServer, addr string) bool` to `activationOps` (server.go:331) with a one-line `realOps` delegation (per ADR-0009, `realOps` stays logic-free — put the type-assert-and-probe in a package function).
  - Extend the fake in `server_test.go` accordingly.

- [ ] **4. Activation: refuse plain load onto a Starting occupant, displace with `--restart`** — `internal/launcher/server.go`
  - `loadProfile` (server.go:412) computes `starting := ops.starting(b, targetAddr)` alongside `healthy` and passes it into `loadProfileManaged` (signature change; external path is unaffected — no external backend has a Starting window).
  - `loadProfileManaged` (server.go:675): if `starting && !restart` → return the refusal (hoisted, testable orchestration decision); if `starting && restart` → stop first (same branch as `healthy`), then start. The in-`startManagedServer` `StartingUp` guard (server.go:50) **stays** as the race backstop.
  - Message updates: `stillStartingUpErr` (server.go:765) and `startupTimeoutErr` (server.go:755) drop `kill <PID>` in favour of `llama-launcher stop <backend|host:port>`, plus "`--restart` to replace it" where displacement is the likely intent.
  - Auto-stop sweep: no code change expected — `ops.discover` now returns Starting instances and the existing loop stops them (decision 5). The same-addr-same-backend skip rule keeps the target occupant out of the sweep; its fate is the `loadProfileManaged` branch above.
  - Fake-driven tests: plain load (same profile / different profile) onto a Starting addr refuses; `--restart` stops-then-starts; `auto_stop_server: true` sweeps a Starting instance at another address; `auto_stop_server: false` + `auto_unload: true` does **not** try to unload a Starting instance (`shouldCrossServerUnload` already requires `ActiveModel != ""`, which is empty on Starting instances — add the regression test, not code).

- [ ] **5. CLI surfaces** — `internal/launcher/cli.go`
  - `cmdStatus` (cli.go:361): Starting instances render as `starting…` instead of `running` (agreed mock: `● llama.cpp  starting…  127.0.0.1:8080`); the details block (PID/log via `fillRuntimeDetails`) works — lsof finds a Starting server's PID.
  - `cmdStatusJSON` (cli.go:495): keep `running` meaning healthy; add `"starting"` bool to the entry struct. `list --json` (profiles) is unaffected.
  - `cmdStop`/`resolveTargetInstance` (cli.go:281,326): should need no change — discovery now surfaces the instance. Verify with a CLI-level test rather than assuming.
  - `cmdUnload` (cli.go:147): the `ActiveModel == ""` gates currently exclude Starting instances. A Starting **managed** instance is a valid unload target (unload = stop, ADR-0003/0004): accept it in both the profile-arg path (cli.go:158) and the no-arg enumeration (cli.go:166), labelled `(starting…)` in the multiple-targets listing.
  - Exit-code semantics unchanged.

- [ ] **6. TUI menu** — `internal/launcher/menu.go`
  - The running-instances header/sub-lists (menu.go:27,99,285) must show Starting instances (`starting…` label) and offer **stop** for them; model-swap/load actions against a Starting address go through the same refusal as the CLI (they call `LoadProfile`, so this should fall out of Item 4 — verify, don't assume).
  - Menu refresh: covered by the `instancesSignature` change in Item 1.

- [ ] **7. MCP adapter passthrough check** — `cmd/llama-launcher-mcp/`
  - The adapter shells out to the CLI, so `stop_server` gains the new capability automatically and `status` output now may contain `starting…`/`"starting"`. Verify no adapter-side parsing or TDD §12-adjacent tool description contradicts the new state; adjust wording only where it does. Expected: no code change.

- [ ] **8. ADR-0010 docs pass** — `llama-launcher.TDD.md`, `CHANGELOG.md`, `README.md`
  - TDD §6.2 (start refusal, ~line 592) and §6.5 (stop sequence, line 619): document the Starting state, the second identification pass, the stop-verification rule, and the `--restart` displacement; update the error-handling table row at TDD:765 (guidance no longer says "kill the PID" — it says `llml stop`).
  - CHANGELOG: behaviour-change entry under the next version ("a still-loading llama-server is now visible in status and can be stopped/replaced; stop verification hardened").
  - README: update any stop/status examples or troubleshooting text that mention the `kill <PID>` workaround.

## Part B — Integration-test layer (Layer 2 of `backend-tests-plan.md`)

- [ ] **9. Re-point the stale plan doc** — `backend-tests-plan.md`
  - Its Layer-2 section (2026-05-20) predates the `Backend`→`LLMServer` rename, ADR-0009, and the unified `Stop`/`Unload` entry points. Replace that section with a short pointer to this plan (Items 10–15 are the validated spec). Leave Layer 1 (shipped) as historical record.

- [ ] **10. Shared integration helpers** — `internal/launcher/integration_test.go` (new, `//go:build integration`)
  - `mustFindBinary(t, name)` (LookPath + `t.Skip`), `freePort(t)`, `waitForHealthy(t, b, addr, timeout)`, `waitForUnhealthy(...)` — all `t.Helper()`. Per-test `t.TempDir()` log dirs. No `t.Parallel()` anywhere in the integration files.

- [ ] **11. llamacpp lifecycle** — `internal/launcher/integration_llamacpp_test.go` (new, build-tagged)
  - Requires `INTEGRATION_MODEL_LLAMACPP` (absolute .gguf path); skip the whole test if unset.
  - **Delta from the 2026-05-20 plan:** drive the real in-package start path (`StartServer` with a constructed `*Config` + `ResolvedProfile`) instead of re-implementing `exec.Command` — this exercises Setsid, the reaper goroutine, and the startup-grace crash detection for real.
  - Subtests in order: start → `waitForHealthy` → `Stop(addr)` → `waitForUnhealthy`.
  - **New scenario (depends on Part A): stop-while-Starting** — start with the real model, poll until `StartingUp(addr)` is true (llama-server answers 503 while loading), call `Stop(addr)` mid-load, assert success and port release. This is the flagship test connecting both tasks.
  - `t.Cleanup`: best-effort `Stop(addr)` then SIGKILL by PID.

- [ ] **12. Ollama lifecycle** — `internal/launcher/integration_ollama_test.go` (new, build-tagged)
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
