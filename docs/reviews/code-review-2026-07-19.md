# Code Review — Merge reconciliation `f17d0e2` + `6e577ab` — 2026-07-19

> **Resolution (same day):** all twelve findings were fixed in `34c5de0` (the four doc
> corrections) and `2bfb1fe` (the three production hardenings and the five test
> gaps/fixes). The full verify suite including `go test -race` is green after the fixes.

**Scope:** the merge of origin/main (v1.4.5) into local main — resolution diff `55ececd..HEAD` cross-checked against both parents, focusing on hand-edited files.
**Mission:** llama-launcher manages local LLM servers (llama.cpp, Ollama, LM Studio) on a shared address with stateless live discovery; an MCP adapter exposes lifecycle control to an assumed-prompt-injectable client behind an IP allowlist.
**Files reviewed:** 22 changed files (13 Go, 9 docs) plus both parents for cross-checking.

## Executive Summary

The reconciliation is sound. Four independent audit angles each verified the merge's core obligations: the published 1.4.5 CHANGELOG section is byte-identical to the release, every origin-only fix survived (reap goroutine, MCP timeouts + output cap, list alignment, no-op TryStop, address-AND-backend matching), every local-superset variant is intact (allowlist validation on all five tools, 512 KB bounded reads, string sanitisation, seam-based orchestration), no scenario from the two deleted incoming test files lost its coverage, and no deleted symbol is referenced anywhere live. All twelve findings below are Medium: four documentation-accuracy residues in the hand-merged docs, two pre-existing security gaps that both remediation streams missed, one pre-existing reaping asymmetry the merge's own rationale now highlights, and five test-side gaps (one flake risk, one latent deadlock trap, three missing assertions/compositions).

## Intent & Architecture Findings

### Medium — Dead ADR-0007 link in the TDD `[Intent]`
- **Where:** `llama-launcher.TDD.md:593`
- **What:** Step 8 of the managed start path links `docs/adr/0007-idempotent-load-with-drift-notice.md`, which does not exist; the file is `docs/adr/0007-profile-activation-idempotency.md`. The broken name came from the local parent; origin used the correct name in all five of its links.
- **Why it matters:** A reader following the managed-start contract lands on a 404; the ADR is the authority for the idempotency behaviour the step describes.
- **Fix:** Point the link at `docs/adr/0007-profile-activation-idempotency.md`.

### Medium — Unreleased entry mischaracterizes 1.4.5's `status --json` order `[Intent]`
- **Where:** `CHANGELOG.md:13`
- **What:** The entry claims 1.4.5 "emitted running instances in discovery order", but 1.4.5's `DiscoverRunningInstances` already sorted instances by (backend, addr) — its output was fully deterministic. The actual delta is only the grouping rule (idle entries interleaved per backend rather than appended after all running entries).
- **Why it matters:** The Unreleased section's contract is to describe deltas vs 1.4.5 accurately; "discovery order" implies nondeterminism that never shipped.
- **Fix:** Reword to describe the grouping change and drop the determinism claim.

### Medium — Three stale `-m` claims contradict the corrected flag table `[Intent]`
- **Where:** `llama-launcher.TDD.md:588`, `CONTEXT.md:25`, `docs/adr/0003-llamacpp-restart-per-profile.md:3`
- **What:** All three say the Model is baked into llamacpp start arguments via `-m`, while `BuildServerArgs` emits `--model`, TDD §8 says `--model`, and the CHANGELOG claims the docs were corrected.
- **Why it matters:** The merged docs now contradict both the code and their own changelog claim; anyone grepping `ps` output for `-m` finds nothing.
- **Fix:** Change the three sites to `--model`.

### Medium — ADR-0008 overclaims a universal `--json` mapping `[Intent]`
- **Where:** `docs/adr/0008-mcp-control-plane-adapter.md:15`
- **What:** "maps each MCP tool to a `llama-launcher <subcommand> --json` invocation" — only `list_profiles` and `server_status` pass `--json`; the other five tools invoke plain subcommands and the result mapping is exit-code-keyed text.
- **Why it matters:** Pre-existing in both parents, but this is one of the five hand-merged docs and the claim was left standing next to a corrected paragraph.
- **Fix:** "maps each MCP tool to a `llama-launcher` subcommand invocation (`--json` where the CLI offers it)".

## Critical & High Findings

None. No finding above Medium survived verification in any category.

## Medium Findings

### Medium — MCP listener accepts an unbounded request body and sets no ReadTimeout `[Security]`
- **Where:** `cmd/llama-launcher-mcp/main.go:67-73`
- **What:** The `http.Server` sets header/write/idle timeouts but no `ReadTimeout` and no request-body size cap, and the go-sdk streamable handler ingests the POST body with an uncapped `io.ReadAll` (buffering ~2× the body in the stateless path). The 1 MiB `limitedWriter` cap guards subprocess *output* only.
- **Why it matters:** A prompt-injected MCP client from an allowlisted IP can OOM the host-side adapter with one multi-gigabyte POST, or pin connections with a slow-drip body after the 10 s header window. Pre-existing in both remediation streams.
- **Fix:** Wrap the handler so `r.Body = http.MaxBytesReader(w, r.Body, 1<<20)` before it reaches the MCP handler (control-plane calls are tiny), and set `ReadTimeout` (~30 s).

### Medium — `sanitizeServerString` passes Unicode bidi/directional-override characters `[Security]`
- **Where:** `internal/launcher/backend_http.go:37-44`
- **What:** The filter strips C0/DEL/C1 but passes every rune ≥ 0x20, so directional overrides (U+202A–U+202E, U+2066–U+2069, U+200E/U+200F, U+061C — the Trojan-Source class) survive in a server-reported model name and reach the terminal.
- **Why it matters:** A hostile process squatting a configured port can visually reorder or mask the loaded-model display — the screen-spoof outcome the sanitiser exists to prevent — without a single control byte. Pre-existing in both streams.
- **Fix:** Map the bidi/isolate ranges to `-1` in the `strings.Map` predicate and add a `TestSanitizeServerString` case.

### Medium — `Ollama.TryStart` never reaps its child, stalling menu-driven stops ~21 s `[Correctness]`
- **Where:** `internal/launcher/backend_ollama.go:106-117` vs the reap rationale at `internal/launcher/server.go:85-93`
- **What:** The merge imported the "a zombie still satisfies kill(pid, 0)" rationale for managed servers, but launcher-started `ollama serve` (`cmd.Start()` with no `Wait`) still zombifies in the long-lived menu process.
- **Why it matters:** In the TUI, start Ollama then stop it: `terminatePID` SIGTERMs the listener, the zombie keeps `IsProcessAlive` true, and the stop burns the full 15 s SIGTERM window + SIGKILL + 5 s poll — ~21 s stall. Pre-existing in both parents, but the graft fixes exactly this failure mode for llamacpp while documenting it as fixed generally.
- **Fix:** Add `go func() { _ = cmd.Wait() }()` after `cmd.Start()` in `TryStart` (result discardable — TryStart's contract has no crash detection window).

### Medium — Ported binary-not-found test probes a real hardcoded port before its target path `[Correctness + Tests]`
- **Where:** `internal/launcher/server_test.go:1271-1287`
- **What:** `TestStartServer_BinaryNotFound` uses `127.0.0.1:18080`, and `startManagedServer`'s `StartupProber` probe runs a real `GET /health` (2 s timeout) *before* the `LookPath` the test targets. Cross-validated by two independent audit angles.
- **Why it matters:** Any local service answering 503 on 18080 — a reverse proxy with no upstream, or an actual llama-server mid-load, this repo's own use case — flips the test into the double-spawn refusal; any listener adds up to 2 s of wait.
- **Fix:** Derive a provably-closed port: listen on `127.0.0.1:0`, record the port, close the listener, use that port.

### Medium — `runCLI` drains stderr concurrently but not stdout `[Correctness]`
- **Where:** `internal/launcher/cli_test.go:308-345`
- **What:** The ported helper guards the stderr pipe against filling but reads stdout only after `Run` returns, so any command writing past the ~64 KiB pipe buffer deadlocks the test.
- **Why it matters:** Current callers emit a few hundred bytes, so nothing fails today — it is a latent trap for the next `TestRun_*` case (`list` on a large config, `logs`).
- **Fix:** Drain `outR` into a buffer in a goroutine, join after `Run` returns — mirroring the stderr pattern.

### Medium — No test composes the exit ≥ 2 error path with the output cap in the adapter's `run()` `[Tests]`
- **Where:** `cmd/llama-launcher-mcp/config.go:186-198`, `config_test.go:153`
- **What:** The merge combined the local exit-code discriminator with origin's `limitedWriter` cap, but the cap test covers exit 0 only and the exit-3 tests use tiny outputs. The error branch concatenates capped stderr + capped stdout — the path a failing model load with a runaway log hits.
- **Fix:** Fake CLI floods both streams past `maxCapturedOutput` and exits 3 → assert `IsError`, both truncation notices, total size bounded near 2×(cap+notice).

### Medium — `fakeOps.waitErr` exists but nothing sets it — health-wait failure is untested through `loadProfile` `[Tests]`
- **Where:** `internal/launcher/server.go:690-692`, `server_test.go`
- **What:** `ops.waitHealthy` failure → `startupTimeoutErr`, `started=false` is only tested in pieces (the error constructor, `WaitForHealth` directly), never as the composed orchestration outcome.
- **Why it matters:** A child that crashes mid-load *after* the 500 ms grace lands exactly here; nothing pins that `loadProfile` reports `started=false`, attempts no model load, and surfaces PID/log guidance.
- **Fix:** One seam subtest with `waitErr` set.

### Medium — Drift-notice content lost its assertion in the reconciliation `[Tests]`
- **Where:** `internal/launcher/server.go:651` (`printDriftNotice`), `server_test.go:573`
- **What:** Origin's deleted suite asserted the notice names the field, both values, the profile, and the `--restart` hint; the surviving seam subtest feeds a pre-built drift string through `fakeOps` and asserts only the no-op.
- **Why it matters:** ADR-0007 documents the notice as the user's cue to act; dropping the `--restart` hint or field naming would pass the suite.
- **Fix:** Assert the notice text via `captureStderr` around a fakeOps drift load, or test `printDriftNotice` directly.

### Medium — `status --json` extended keys are asserted nowhere `[Tests]`
- **Where:** `internal/launcher/cli_test.go:16` (`statusJSONEntry`)
- **What:** The MCP `server_status` tool promises `active_profile`, `active_model`, `pid`, `uptime_seconds`; no test checks those JSON keys exist (the test struct carries only backend/running/address). Pre-existing — origin never asserted them either.
- **Why it matters:** A silent key rename would break MCP clients without a test failing.
- **Fix:** Extend `TestCmdStatusJSON_ListsEveryInstanceOfABackend` with a raw-JSON key check on a running entry.

## Recommended Action Order

1. **Doc corrections** (dead ADR link, `status --json` mischaracterization, `-m` → `--model` × 3, ADR-0008 `--json` overclaim) — five-minute fixes, and the docs are the project's authority chain.
2. **MCP request-body cap + ReadTimeout** — the one finding with real security weight; small, well-understood change plus a test.
3. **Bidi-character sanitisation** — completes the control the sanitiser already claims to provide.
4. **`Ollama.TryStart` reap** — one line, removes a 21 s UX stall, aligns with the imported rationale.
5. **Test fixes** — port derivation and stdout drain first (flake/deadlock risks), then the four missing assertions/compositions.
6. No finding is a candidate for `/improve-codebase-architecture`; no design discussion needed.

## What Looked Good

The merge's riskiest hand-work held up under four independent audits: the reap goroutine grafted into `startManagedServer` composes correctly with the StartupProber refusal (buffered channel, no double-Wait, no leak, stable at `-count=5` under `-race`); `limitedWriter` honours the `io.Writer` contract and composes cleanly with the exit-code discriminator; the validation gate covers all five MCP tools with the allowlist grammar; every backend body read goes through the 512 KB bound with no remnant of the replaced tiered helper; and the re-expression of the dropped no-seam test scenarios onto the activationOps/registry seams is faithful. The CHANGELOG's published-history discipline (1.4.5 byte-preserved, deltas in Unreleased) held exactly.
