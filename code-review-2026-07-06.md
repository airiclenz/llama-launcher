# Code Review — Whole Repository — 2026-07-06

**Scope:** All source under the project root (`main.go`, `internal/launcher/`, `cmd/llama-launcher-mcp/`), excluding tests except where coverage was assessed.
**Mission:** A one-shot macOS Go CLI that manages local LLM Servers (llama.cpp, Ollama, LM Studio) through named YAML Profiles — start/stop servers, load/unload Models, then exit with zero resident memory; a process manager, not a request router. An optional separate `llama-launcher-mcp` binary exposes the lifecycle commands as MCP tools over HTTP, gated by a source-IP allowlist.
**Files reviewed:** 20 source files + the existing test suite.

## Executive Summary

The core architecture is faithful to its ADRs: the CLI opens no socket, persists no state, stops unconditionally, keys instances by `host:port`, and the MCP adapter's shell-out design is exactly as documented. The problems cluster in two seams. First, **the llamacpp parameter story is broken end-to-end**: five sampling parameters are configured (and shipped in the default config) but never passed to `llama-server`, and the ADR-0007 drift detector compares the over-promised config surface against an under-reporting `/props`, so **out of the box, the second `load` of any llamacpp profile prints a bogus, unresolvable "parameters have drifted" notice** — cross-validated by two reviewers. Second, the single most dangerous defect is a **Critical data-loss bug: automatic log cleanup runs with a nil config and therefore deletes the live logs of still-running servers**, defeating the "always skip running servers' logs" rule the code enforces everywhere else. On the network side, the MCP adapter forwards tool arguments as raw CLI positionals, so a prompt-injected agent can pass `tail_log{"target":"clean"}` to trigger log deletion **through a read-only tool**, or `target:"-f"` to hang the adapter. The remaining findings are localized multi-instance and TUI edge cases plus a real test gap: the entire lifecycle orchestration (`LoadProfile`, stop/start) and the CLI command surface have no tests, so the ADR-0001/0004/0007 behaviours are protected only by leaf-level helper unit tests.

## Intent & Architecture Findings

### High — Idempotent llamacpp load prints a false, unresolvable drift notice `[Intent + Correctness]`

- **Where:** `internal/launcher/server.go:399-408` (`liveParamDrift`) → `server.go:418-462` (`paramDrift`), driven by `backend_llamacpp.go:88-129` (`QueryLiveParams`).
- **What:** `paramDrift` reports drift whenever one side is nil (`if a == nil || b == nil || *a != *b`), but `/props` only ever populates 7 of the 17 compared fields — `gpu_layers`, `threads`, `threads_batch`, `batch_size`, `flash_attn`, `cont_batching`, `mlock`, `no_mmap`, `embedding`, `jinja` are never reported. `QueryLiveParams`'s own docstring claims "the rest remain nil so paramDrift will skip them"; that is false.
- **Why it matters:** The shipped default config sets all these fields in `defaults:`. Running `llama-launcher load <profile>` twice — the ADR-0007 no-op path — prints a ~10-line "parameters have drifted… run `--restart`" notice with zero real drift, and because the fields never appear in `/props`, `--restart` never clears it. Through the MCP adapter this stderr is appended to the `load_profile` tool result, so an agent told the call is an idempotent no-op instead sees a drift warning and may restart a multi-GB model for nothing.
- **Fix:** In the live-drift path, compare only fields the backend actually reports — treat a nil `live` value as "unknown, skip" rather than "unset, drifted" (mask `fresh` to the fields `/props` populates, or make `paramDrift` skip any field where either side is nil).

### High — llamacpp sampling parameters are configured but never applied `[Intent + Correctness]`

- **Where:** `internal/launcher/backend_llamacpp.go:164-228` (`BuildServerArgs`).
- **What:** `temperature`, `repeat_penalty`, `top_k`, `top_p`, `min_p` are absent from the flag mapping — no `--temp`, `--repeat-penalty`, `--top-k`, `--top-p`, `--min-p` anywhere in production code. No other backend consumes them at load either.
- **Why it matters:** `defaults/config.yaml:184-188` documents all five as llamacpp-applicable and sets them in `defaults:`; the TDD calls them "server-side defaults for API requests." The server silently runs with llama.cpp's own defaults (temp 0.8, repeat_penalty 1.0), so user-configured sampling is ignored. This is also the evil twin of the drift bug: any sampling value `/props` *does* report produces a drift notice that `--restart` can never reconcile.
- **Fix:** Emit the five flags in `BuildServerArgs` when the pointers are set, **or** remove them from the promise (config table, shipped defaults, and the drift comparison) in the same change. (Design decision — see the plan.)

### High — Documented LM Studio parameter mappings don't exist

- **Where:** `internal/launcher/backend_lmstudio.go:84-110` (`LoadModel`).
- **What:** The config table promises lmstudio support for `gpu_layers` "(mapped: 99→'max', 0→'off')", `batch_size` "(mapped to eval_batch_size)", and `flash_attn` (`defaults/config.yaml:171-177`, mirrored in `README.md`), and the shipped lmstudio example profiles set them. `LoadModel` sends only `model` and `context_length`. Worse, the TUI "Show model config" popup renders the 99→"max"/0→"off" mapping (`menu.go:444-457`), telling the user a setting is in effect that never left the launcher.
- **Why it matters:** Setting `gpu_layers: 0` on an lmstudio profile expecting CPU-only still loads with LM Studio's GPU default — a silent correctness gap the UI actively misreports.
- **Fix:** Implement the documented payload fields in `LMStudio.LoadModel`, or correct the table, README, and popup mapping. (Design decision — see the plan.)

### Medium — `status --json` collapses multiple instances per backend, contradicting ADR-0006 `[Intent + Correctness]`

- **Where:** `internal/launcher/cli.go:514-521` (`cmdStatusJSON`).
- **What:** The loop takes the first `inst.Backend == name` match and `break`s. `DiscoverRunningInstances` probes every profile-derived `host:port` and can return several instances per backend.
- **Why it matters:** With `auto_stop_server: false` (documented as "allow multiple servers to run simultaneously") and two llamacpp profiles on ports 8080/8081, human `status` lists both but `status --json` — and therefore the MCP `server_status` tool — silently drops one. Note the TDD §3.2 currently documents the one-per-backend schema, so that wording must change with the fix.
- **Fix:** Emit one JSON entry per discovered instance, plus a `running:false` entry only for enabled backends with no instances; update TDD §3.2.

### Medium — `Ollama.TryStop` is broken as written and overbroad if it worked `[Intent + Correctness]`

- **Where:** `internal/launcher/backend_ollama.go:130-158`.
- **What:** `exec.Command(binary, "stop").Run()` invokes `ollama stop` with no model argument; the ollama CLI requires exactly one model name, so `TryStop` returns early with an error on every call and the `pgrep -f "ollama serve"` SIGTERM fallback below it is dead in practice. `EnsureStopped` (`server.go:192`) discards the return, contradicting the documented "errors reported but non-blocking." If the command ever succeeded, the pgrep sweep would kill *every* ollama instance on the host regardless of the target `addr`, contradicting ADR-0006.
- **Fix:** Stop the specific loaded model (`ollama stop <model>` from `ListRunningModels`) or delete `TryStop`'s body and rely on the unconditional PID path; report `TryStop` errors in `EnsureStopped`. *(uncertain on the exact CLI arity — confirm with `ollama stop` on the target install.)*

### Medium — Stale documentation claims contradicting the live-derivation invariant

- **Where:** `CONTEXT.md:32`, `llama-launcher.TDD.md:833`.
- **What:** CONTEXT.md states "The persisted state-file schema retains `backend` as a JSON field name on `ServerState`" — no `ServerState` type exists in the codebase (zero grep hits) and state files were removed. TDD §14 instructs "Follow `skills/coding-standards/SKILL.md`", but the repo's `skills/` directory contains only `manage-llm-server`, so that path is broken for any contributor.
- **Fix:** Rewrite the CONTEXT.md sentence to reflect live derivation; point TDD §14 at an existing location (or vendor the standard).

### Medium — Write-only `ResolvedParams` field kept alive by a per-discovery HTTP call

- **Where:** `internal/launcher/discovery.go:133-140`; assigned also at `server.go:532`, `server.go:569`.
- **What:** `RunningInstance.ResolvedParams` is assigned in three places and read nowhere (production or tests). To populate it, `probeInstance` makes an extra `QueryLiveParams` (`GET /props`) request per llamacpp instance on *every* discovery pass — including each menu refresh tick — for a value nobody consumes.
- **Fix:** Delete the field, the probe call, and the dead assignments. (Drift detection in `LoadProfile` does its own `QueryLiveParams`.)

### Medium — Dead exported / production-dead functions

- **Where:** `internal/launcher/server.go:32` (`IsServerAlive`), `sysmem.go:260` (`FormatMemoryLine`), `sysmem.go:267` (`percentString`).
- **What:** `IsServerAlive` has zero callers anywhere; `FormatMemoryLine` and `percentString` are called only from their own test files while the TDD §5.2 table still advertises them. All pass the deletion test.
- **Fix:** Delete them and their TDD mentions; keep `percentValue`, which the template engine uses.

### Medium — Profile-row rendering exists in three diverged copies (one already buggy)

- **Where:** `internal/launcher/cli.go:441-483` (`cmdList`), `menu.go:524-565` (`buildSimpleProfileLines`), `menu.go:567-606` (`buildProfileItems`).
- **What:** Each independently computes server-tag columns, favourite-star alignment, and widths — and they have diverged: `cmdList` measures with byte `len()` while both menu variants use `visibleWidth`.
- **Why it matters:** Any profile description with a multi-byte character (the TDD's own example uses an em dash) misaligns the `★` column in `list` output but not in the menu.
- **Fix:** Switch `cmdList` to `visibleWidth` for the immediate bug. The three-way split is a candidate for `/improve-codebase-architecture`.

## Critical & High Findings

### Critical — Automatic log cleanup deletes the live logs of running servers `[Correctness]`

- **Where:** `internal/launcher/log_cleanup.go:121-124` (`autoCleanupLogs`) with `:107-119` (`activeLogFiles`) and `server.go` `createLogPath`.
- **What:** `autoCleanupLogs` calls `cleanupLogs(nil, …)`, and `activeLogFiles(nil)` returns an empty map, so the running-instance protection that `logs clean` gets is silently skipped on the automatic path.
- **Why it matters:** The shipped default config sets `log_retention: 7`. If a server (e.g. auto-started `ollama serve`) has run longer than the retention window and the user then loads any profile or starts another server, `createLogPath` → `autoCleanupLogs` unlinks the still-open log. The server keeps writing to the unlinked inode and `llama-launcher logs` reports "No launcher-managed log found" — silent log loss in normal use.
- **Fix:** Thread `*Config` through `createLogPath`/`autoCleanupLogs` into `cleanupLogs` (both call sites already hold `cfg`).

### High — MCP `tail_log` target is an unsanitized CLI positional (read-only bypass + DoS) `[Security]`

- **Where:** `cmd/llama-launcher-mcp/main.go:90-95` and `main.go:158-163` (`argsFor`) → `internal/launcher/cli.go:592-605` (`cmdLogs`).
- **What:** `tail_log` (a *read* tool, registered even under `--read-only`) forwards its attacker-controlled `target` straight through as a positional arg: `cfg.run(ctx, argsFor("logs", args.Target)...)`. `cmdLogs` starts with `if args[0] == "clean" { return cmdLogsClean(...) }`, and treats `-f`/`--follow` as flags.
- **Why it matters:** A prompt-injected agent calling `tail_log{"target":"clean"}` triggers `cmdLogsClean` at its default `--days 7`, **deleting every non-active `*.log` older than 7 days — a mutation reached through the read surface, defeating the `--read-only` guarantee**. `tail_log{"target":"-f"}` makes the launcher run `tail -f`, which never returns; the adapter buffers stdout into an in-memory `strings.Builder` and blocks on `cmd.Run()`, so the request hangs and the buffer grows unbounded. (The same unvalidated-positional pattern is shared by `stop_server`/`unload_model` via `argsFor`.)
- **Fix:** In the adapter, reject `target`/`profile` values that start with `-` or equal a subcommand keyword (`clean`), ideally validating against discovered `host:port`/backend names before forwarding; never let a read tool's argument select a different subcommand.

## Medium Findings

### Medium — Menu "primary instance" selection picks the wrong instance `[Correctness]`

- **Where:** `internal/launcher/menu.go:29-42`.
- **What:** The `if primary == nil { primary = inst }` fallback runs *inside* the loop, so the first discovered instance wins even when a later instance is the one with a model loaded.
- **Why it matters:** With two servers running (`auto_stop_server: false`), e.g. an idle LM Studio (sorts first) plus Ollama with a model, `anyModel` is true but `primary` is the idle LM Studio; `runLoadedMenu` then shows the wrong instance — "Show log" tails the wrong backend, "Show model config" describes the wrong server, and simple mode prints an empty `Model:` line.
- **Fix:** Track "first instance with a model" separately and only fall back to `instances[0]` after the loop.

### Medium — auto-stop / auto-unload skips the target by address without checking backend `[Correctness]`

- **Where:** `internal/launcher/server.go:349-352`, `server.go:360-363`.
- **What:** Both loops `continue` on any instance whose `Addr() == targetAddr`, assuming it is the target server — without checking its backend.
- **Why it matters:** If a *different* backend currently occupies the profile's `host:port` (e.g. an ollama profile configured on 8080 while `llama-server` still runs there after a config change), `auto_stop_server: true` stops everything *except* the actual blocker; the subsequent start then fails to bind — exactly the failure auto-stop exists to prevent.
- **Fix:** Only `continue` when `inst.Addr() == targetAddr && inst.Backend == profile.Backend`.

### Medium — `showPopup` leaves the terminal cursor hidden on exit `[Correctness]`

- **Where:** `internal/launcher/ui.go:338, 344-350`.
- **What:** `showPopup` writes `escCursorHide`, waits for a key, restores termios, but never re-shows the cursor.
- **Why it matters:** With the built-in `auto_close` default (true), menu → "Show model config" (or any popup) → keypress → the process exits leaving the cursor invisible until the user runs `reset`.
- **Fix:** Print `escCursorShow` after `readKey()` in `showPopup`, mirroring `selectMenu`'s deferred restore.

### Medium — MCP adapter has no HTTP timeouts and unbounded tool output `[Security]`

- **Where:** `cmd/llama-launcher-mcp/main.go:61`; `cmd/llama-launcher-mcp/config.go:130-134`.
- **What:** `srv := &http.Server{Addr: cfg.listen, Handler: mux}` sets no `ReadHeaderTimeout`/`WriteTimeout`, and `run` collects all subprocess stdout into an unbounded `strings.Builder`.
- **Why it matters:** Compounds the `tail_log{"target":"-f"}` hang (above) and leaves the listener open to slowloris-style resource exhaustion from any allowed client.
- **Fix:** Set read/write timeouts on the `http.Server` and cap tool output size.

### Medium — Unbounded reads of probed servers' HTTP responses `[Security]`

- **Where:** `internal/launcher/backend_llamacpp.go:32,73,118`; `backend_ollama.go:38`; `backend_lmstudio.go:98,188` (`io.ReadAll` / `json.NewDecoder(resp.Body)` with no size limit).
- **What:** Health checks, model listing, and `/props` reads consume the response body with only a 2 s client timeout bounding them.
- **Why it matters:** A malicious local process squatting on a configured port (an in-scope adversary) can stream gigabytes over loopback within 2 s on each probe. `DiscoverRunningInstances` probes several addresses in parallel, so one squatter can force large concurrent allocations and OOM the one-shot CLI or the long-lived MCP adapter.
- **Fix:** Wrap each body in `io.LimitReader` (health/model listings need only a few KB) before reading or decoding.

### Medium — API key may leak to the MCP client through `tail_log` output `[Security]` (uncertain)

- **Where:** `cmd/llama-launcher-mcp/main.go:90-95` → launcher-managed log file.
- **What:** llama-server's stdout/stderr (started with `--api-key <key>` in argv) is captured to the managed log; `tail_log` returns that raw log to the network client with no redaction (`redactAPIKeyArgs` only masks display surfaces, not the log-tail surface).
- **Why it matters:** If the configured llama-server build echoes its parsed command line into stdout/stderr at startup, the plaintext key is handed to the prompt-injectable cloud agent that is supposed to hold no credentials.
- **Fix:** Confirm by loading a profile with an `api_key` and running `llama-launcher logs` to check whether the key appears; if so, scrub known secret tokens from tool output or restrict `tail_log` to lines the launcher itself writes.

### Medium — Lifecycle orchestration and the CLI command surface are untested `[Tests]`

- **Where:** `internal/launcher/server.go:319` (`LoadProfile`), `server.go:145-245` (stop lifecycle), `cli.go` (no `cli_test.go`); server-start / health-timeout paths at `server.go:97-101, 296-307, 575-584`.
- **What:** `LoadProfile` — the single most important orchestration (idempotency no-op, drift notice, `auto_stop_server`/`auto_unload`) — has no test; it is covered only indirectly via unit tests of the private helpers `paramDrift` and `shouldCrossServerUnload`. The stop lifecycle (`StopInstance`/`EnsureStopped`), the exit-code contract (0/1/2/3, which the MCP result mapping depends on), and the crash-on-start / health-timeout error paths are all untested.
- **Why it matters:** An inverted `!restart && healthy` gate or a broken model-match would restart a live server on every `load` with the suite still green. The MCP adapter's result mapping pins `status --json exits 1 while printing JSON` against a *fake* CLI, so nothing verifies the real CLI honours it.
- **Fix:** Add httptest-backed `LoadProfile` tests (assert `started==false` and no stop/load traffic on the no-op path; assert the drift notice on real drift; assert auto-stop targets other addresses only); add `StopInstance`/`EnsureStopped` tests with an in-package fake backend; add CLI tests asserting exit codes and `status --json` output shape.

### Medium — Log-cleanup "skip running servers" and MCP allowlist adversarial cases are untested `[Tests]`

- **Where:** `internal/launcher/log_cleanup.go:36-39, 107-119`; `cmd/llama-launcher-mcp/allowlist.go:134-147`.
- **What:** Every `cleanupLogs` test passes `cfg = nil`, so `active[path]` is never true — the only guard protecting a live server's open log has no coverage (directly related to the Critical finding). The allowlist middleware tests cover an allowed IP, a denied IP, and garbage `RemoteAddr`, but no IPv6 bracket form (`[::1]:port`), no IPv4-mapped-IPv6 (`[::ffff:192.168.64.2]:port` — what a dual-stack listener reports), and no assertion that `X-Forwarded-For`/`X-Real-IP` are ignored.
- **Fix:** Add a `cleanupLogs` test with a healthy httptest instance whose log survives while a stale log is removed; extend the allowlist middleware table with IPv6/mapped forms and a forwarded-header spoof asserting 403.

## Recommended Action Order

1. **Critical log-loss fix** (thread `*Config` into automatic cleanup) — self-contained, prevents data loss, and unblocks its regression test.
2. **Drift nil-masking** — stops the out-of-the-box false drift notice; the safety net for the parameter work.
3. **Sampling params (design call)** and **LM Studio mappings (design call)** — decide implement-vs-walk-back; these two carry the only genuine design forks in the plan.
4. **MCP `tail_log` target sanitization** — closes the read-only bypass and the `-f` hang; then the adapter HTTP timeouts + output cap, then `io.LimitReader` on probe reads.
5. **Multi-instance correctness cluster** — `status --json` per-instance, `Ollama.TryStop`, menu primary-instance, auto-stop backend check.
6. **Small UX/cleanup** — cursor restore, `cmdList` `visibleWidth`, dead-code removal, doc fixes.
7. **Test hardening last** — `LoadProfile`/stop lifecycle, CLI exit codes + `status --json` shape, log-cleanup skip-running, allowlist adversarial cases (ordered after the fixes they assert).

The three-way profile-row builder split (behind the `cmdList` byte-length bug) is flagged as a `/improve-codebase-architecture` candidate rather than a refactor inside these fixes.

## What Looked Good

Health-check discrimination is genuinely well-built and well-tested across all three backends, including the LM Studio "200-to-everything" body-based cases. The config layer (parse/merge/enable-disable, bool-or-mapping server form, api_key plumbing on load *and* reload, `defaults.server` deprecation, `sort_alphabetically`/`profileOrder`) and the memory-template engine are clean and thoroughly covered. The MCP allowlist itself is sound — it keys on kernel-provided `RemoteAddr`, ignores `X-Forwarded-For`, defaults to loopback, and wraps the single `/mcp` route before the handler. The exec boundary is careful everywhere: fixed binaries, argv slices, no shell, no `$EDITOR`. The weaknesses are at the edges of otherwise-solid subsystems, not in their foundations.
