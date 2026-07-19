# Remediation Plan — Code Review + Architecture Pass (2026-07-19)

**Base commit:** `a5e6006` (branch `main`)
**Provenance:** consolidated from a full high-signal code review (intent & structure,
correctness, security, critical-path tests) and an architecture deepening pass over the
whole repo. Every finding below was re-read against the source before landing here.

## How to execute this plan

Run it with `/implement-plan` and forward `/coding-standards`:

```
/implement-plan docs/plans/2026-07-19-review-and-architecture-remediation.md with skills: coding-standards
```

Each work item is a numbered `## N.` heading. Items are ordered so earlier ones unblock
later ones; run them in order. An item is done when its heading carries a `✅ DONE`
marker. Items flagged **DESIGN-CALL** require a human (or the delegated reviewer) to
answer the `Design question` before implementation — the executor will stop and ask.
`DEPENDS: N` means do item N first.

## Authoritative sources & precedence

When an item's instructions appear to conflict with the code's intent, the **ground
truth wins, in this order**: the ADRs in `docs/adr/` → `CONTEXT.md` (domain language) →
`llama-launcher.TDD.md` → this plan. Do not treat this plan as overriding an ADR; if an
item seems to contradict one, stop and flag it. Each item names the invariant it
restores so acceptance can be checked independently of the fix.

## Repo conventions (apply to every item)

- Follow `skills/coding-standards/SKILL.md` (forwarded). Read the Go extension before editing.
- Any item that changes **behaviour, config schema, subcommands, or error handling** must
  update `CHANGELOG.md`, and update `llama-launcher.TDD.md` / `README.md` where the change
  touches something they document. Items call this out explicitly where a specific doc line changes.
- Tests are `httptest`-based, no external binaries, run under `go test ./...`. New
  regression tests must fail before the fix and pass after.
- Whole-plan sanity checks: `go build ./...`, `go vet ./...`, `go test ./...`.

## Explicitly NOT in this plan

- No new ADRs are being overturned. The shared-port, stateless, restart-per-profile,
  and MCP-adapter designs all stand; these items make the code honour them.
- No cross-platform (Linux/Windows) work; macOS remains the only target.
- The "further architecture candidates" at the end are recorded, not scheduled — do not
  implement them without a separate design pass.

---

# Phase 1 — Correctness & security fixes

## 1. Fix cross-backend switching on a shared address — ✅ DONE (2026-07-19)

NOTES (2026-07-19): Beyond the two condition edits, added an unexported package
var `stopInstanceFn = StopInstance` (server.go) used by the auto-stop loop and
`loadProfileManaged`; the Verify tests drive `LoadProfile` with an in-process
httptest fake at the target address, and the real `StopInstance` would lsof the
listening PID — the test process itself — and SIGTERM it. The seam records the
stop invocation instead. Tests also blank `PATH` so a regression fails at the
`llama-server` binary lookup rather than forking a real binary.

- **Severity:** Critical. Restores the shared-port design (all backends may share one
  `host:port` so a client needs only one address) and ADR-0004's cross-server unload.
- **Authoritative source:** README "process manager… multiple instances… distinct
  `host:port`" + shared-port design; ADR-0004; ADR-0006.
- **Where:** `internal/launcher/server.go:347-372` (the `ShouldAutoStopServer` and
  `ShouldAutoUnload` loops in `LoadProfile`).
- **Problem:** both loops skip the target address unconditionally
  (`if inst.Addr() == targetAddr { continue }`). When a *different* backend already
  occupies the shared address, it is the one instance that must be stopped, yet it is
  skipped. `healthy` (line 327) is computed with the *target* backend's
  body-discriminating `HealthCheck`, which by design returns false against a foreign
  occupant, so `loadProfileManaged`'s `if healthy` stop (server.go:~512) does not fire
  either. Trigger: `llamacpp` and `ollama` both configured at `127.0.0.1:8080`, ollama
  serving, then `llama-launcher load <llamacpp-profile>` → nothing stops ollama →
  `llama-server` cannot bind → "server exited immediately after start". The reverse
  (external backend onto a port an llamacpp instance holds) times out after 15s.
- **Change:** treat "same address, *different* backend" as a foreign occupant that must be
  cleared. Simplest correct fix: change both skips to
  `if inst.Addr() == targetAddr && inst.Backend == profile.Backend { continue }` so a
  foreign occupant at the target address is stopped by the auto-stop loop. Confirm the
  auto-unload branch still does the right thing for a *same-backend* instance at the
  target address (it should remain skipped there — that instance is the one being
  (re)activated). Do not change same-backend idempotency behaviour (ADR-0007).
- **Verify:** add `server_test.go` cases driving `LoadProfile` against httptest fakes: a
  fake Ollama healthy at the target address while activating an llamacpp profile at the
  same address → the ollama instance is stopped (its stop path is invoked) before the
  managed start is attempted; and the same-address, same-backend, same-model case remains
  a no-op. `go test ./internal/launcher/ -run TestLoadProfile` passes; `go build ./...`.
- **Docs:** note the fix in `CHANGELOG.md` (Fixed).

## 2. Stop auto log-cleanup from deleting active or all logs — DESIGN-CALL — ✅ DONE (2026-07-19)

NOTES (2026-07-19): Design question answered by the user: `log_retention: 0`
means cleanup DISABLED (nothing is deleted), same as unset; only positive N
enables age-based deletion. Implemented by guarding `autoCleanupLogs` (returns
on nil/`<= 0`). Threading `*Config` was done by narrowing `createLogPath` to
`(cfg *Config, name string)` — it read only `cfg.LogDir`/`cfg.LogRetention`
anyway — so `autoCleanupLogs(cfg)` calls `cleanupLogs(cfg, …)` and the
active-file protection now applies on the automatic path.

- **Severity:** High (data loss). Restores the "never delete a running server's log"
  invariant to the automatic path that the manual `logs clean` path already honours.
- **Authoritative source:** TDD §9.1 ("always skips log files belonging to running
  servers"); the invariant is called out in caps in the TDD.
- **Where:** `internal/launcher/log_cleanup.go:107-124` (`activeLogFiles`,
  `autoCleanupLogs`) and `internal/launcher/server.go:655-658` (`createLogPath`).
- **Problem:** `autoCleanupLogs` calls `cleanupLogs(nil, …)`; `activeLogFiles(nil)`
  returns an empty map, so the active-file skip never protects anything during automatic
  on-start cleanup. Two triggers: (a) `log_retention: 0` is accepted by validation
  (`config.go:372` rejects only `< 0`) → `maxAge = 0` → `now.Sub(ts) < 0` is never true →
  every timestamped log is deleted on each server start, including the live log of another
  running server; (b) with the example `log_retention: 7`, a server up longer than 7 days
  has its open log unlinked the next time any profile is loaded.
- **Change:** thread `*Config` from `createLogPath` into `autoCleanupLogs` so it calls
  `cleanupLogs(cfg, …)` and the active-file protection applies. Both callers already have
  `cfg`: `server.go:63` (`startManagedServer`) and `backend_ollama.go:103`
  (`Ollama.TryStart`). Update the outdated comment at `log_cleanup.go:105-106`.
- **Design question:** should `log_retention: 0` mean "delete everything on every start"
  (current, dangerous) or "cleanup disabled"? Recommendation: treat `0` as **disabled**
  (only positive N enables age-based deletion), matching the "Unset = no cleanup" spirit
  in the TDD. Confirm this reading before landing, since it changes documented-ish behaviour.
- **Verify:** add a `log_cleanup_test.go` case with a non-nil `cfg` whose config resolves
  an httptest fake llamacpp instance owning `llamacpp-<ts>.log`; create a second stale log;
  `cleanupLogs(cfg, dir, 0, true)` (the `--all` worst case) removes the stale file and
  **preserves** the active one. Add a case asserting the chosen `retention: 0` semantics.
  `go test ./internal/launcher/ -run TestCleanupLogs` passes.
- **Docs:** `CHANGELOG.md` (Fixed); `README.md` / TDD `log_retention` comments if the
  `0` semantics change.

## 3. Stop the spurious drift notice on every idempotent llamacpp load — ✅ DONE (2026-07-19)

NOTES (2026-07-19): Verified `/props` against the source of the installed
llama-server build (b10068, commit 571d0d540, `tools/server/server-context.cpp`):
sampling is nested under `default_generation_settings.params` and `n_ctx` is
per-slot (`meta->slot_n_ctx`). Took the item's "leave sampling out of the live
diff" branch instead of decoding the nested params: `BuildServerArgs` never
passes sampling flags to llama-server, so comparing live sampling (server
defaults 0.8/1.0) against the shipped config (0.7/1.1) would keep the notice
spurious and un-fixable by `--restart`. `QueryLiveParams` now returns only
`context_size` (normalised to `n_ctx * total_slots`) and `parallel`; the
live-nil skip is a `maskUnreported()` helper feeding the unchanged `paramDrift`.
`TestLlamaCppQueryLiveParams` (discovery_test.go) was updated to the new
contract — `paramDrift`'s own tests are untouched as required.

- **Severity:** High. Restores ADR-0007's promise: a no-op `load` of an unchanged profile
  is silent; a drift notice means real drift.
- **Authoritative source:** ADR-0007; the `QueryLiveParams` doc comment
  (`backend_llamacpp.go:88`) which states the unreported fields "remain nil so paramDrift
  will skip them" — the code does not skip them.
- **Where:** `internal/launcher/server.go:399-408` (`liveParamDrift`) and `418-463`
  (`paramDrift`); `internal/launcher/backend_llamacpp.go:91-131` (`QueryLiveParams`).
- **Problem:** `QueryLiveParams` populates only `ContextSize` (`n_ctx`), the sampling
  params, and `Parallel` (`total_slots`). Every other field (`gpu_layers`, `threads`,
  `threads_batch`, `batch_size`, `flash_attn`, `cont_batching`, `mlock`, `no_mmap`,
  `embedding`, `jinja`) stays nil on the live side. `paramDrift` treats nil-vs-set as
  drift (`if a == nil || b == nil || *a != *b`), and the shipped example config sets all
  those via `defaults`. So the second `load <llamacpp-profile>` with the default config
  prints a ~14-line "parameters have drifted" notice and tells the user (or an MCP agent,
  which will act on it) to `--restart` when nothing drifted. `n_ctx` is also per-slot, so
  `parallel > 1` yields a false `context_size` drift.
- **Change:** in `liveParamDrift` (the live-probe caller), compare **only** fields the
  live side actually reports — skip any field whose live value is nil — so unreported
  fields never manufacture drift. Keep the generic `paramDrift` and its existing
  set-vs-unset unit test unchanged (it is correct for its stated contract; the live caller
  is what needs the skip). Handle the per-slot `n_ctx` so `parallel > 1` does not report
  false `context_size` drift (e.g. compare `n_ctx * total_slots` or the per-slot value
  consistently). If current llama-server builds nest sampling params under
  `default_generation_settings.params`, decode from there too — verify against a real
  `/props` sample or leave sampling out of the live diff rather than guessing.
- **Verify:** add a `server_test.go` case: a fake llamacpp `/props` reporting only
  `n_ctx` + `total_slots` (+ sampling), and a resolved profile with the default block's
  gpu/threads/flash/etc set → `liveParamDrift` returns empty (no drift). A case where
  `n_ctx` genuinely differs → drift reported. `go test ./internal/launcher/ -run
  'TestLiveParamDrift|TestParamDrift'` passes.
- **Docs:** `CHANGELOG.md` (Fixed).

## 4. Harden the MCP `tail_log` tool against the read-only bypass and arg injection — ✅ DONE (2026-07-19)

- **Severity:** High (security). `tail_log` is a *read* tool exposed even under
  `--read-only`; it must not be able to mutate or hang the host.
- **Authoritative source:** ADR-0008 (`--read-only` "exposes only the read tools");
  the read/mutate split is the adapter's security boundary.
- **Where:** `cmd/llama-launcher-mcp/main.go:90-95` (`tail_log`) with
  `internal/launcher/cli.go` `cmdLogs` (the `logs` verb).
- **Problem:** `tail_log` forwards its free-form `target` verbatim as the positional
  argument to `llama-launcher logs <target>` (`argsFor("logs", args.Target)`), and is
  registered before the `if cfg.readOnly { return s }` early-return. The `logs` verb
  treats `args[0] == "clean"` as the destructive `logs clean` subcommand (deletes logs
  older than 7 days) and `-f`/`--follow` as a never-returning tail. So the untrusted
  container client can call `tail_log{target:"clean"}` to delete logs **in read-only
  mode**, or `tail_log{target:"-f"}` to pin a host goroutine + child `tail` process per
  call (resource exhaustion).
- **Change:** validate `target` in the adapter before forwarding: reject any value that
  begins with `-` and reject the literal `clean` (allow only a backend name or a
  `host:port`); return a tool error for rejected input. Prefer a positive allowlist
  (matches `^[A-Za-z0-9._:\[\]-]+$` **and** is a known backend name or parses as
  host:port) over a denylist. This lives in the adapter so the CLI's own grammar is not
  relied on as a security boundary.
- **Verify:** add an adversarial test in `cmd/llama-launcher-mcp/` using an
  argv-recording fake CLI and a `--read-only` adapter: `tail_log` with `"clean"`, `"-f"`,
  `"--days"`, `"; rm"` are all rejected and never reach the fake CLI as a mutating/blocking
  invocation; a valid `"ollama"` / `"127.0.0.1:8080"` passes through. `go test
  ./cmd/llama-launcher-mcp/` passes (the new test fails before the fix).
- **Docs:** `CHANGELOG.md` (Fixed/Security); note the `tail_log` input constraints in
  TDD §15.2.

## 5. Make `status --json` enumerate every running instance — ✅ DONE (2026-07-19)

NOTES (2026-07-19): Beyond the listed docs, updated the MCP `server_status`
tool description (`cmd/llama-launcher-mcp/main.go`) from "each enabled
backend's status" to the per-instance wording — the old text misdescribed
the new output to remote agents, and this item's rationale names that tool
as the JSON path's consumer. No adapter behaviour changed.

- **Severity:** High. Restores ADR-0006 (instances keyed by `host:port`; multiple
  concurrent instances of one backend allowed). The human `status` path already lists them
  all — the JSON path (which the MCP `server_status` tool serves to remote agents) does not.
- **Authoritative source:** ADR-0006; TDD §3.2 `status --json` ("one entry per enabled
  configured backend" is the current wording and is itself wrong for multi-instance).
- **Where:** `internal/launcher/cli.go:491-550` (`cmdStatusJSON`), lines 512-521 in
  particular.
- **Problem:** the loop iterates enabled **backend names** and keeps the first discovered
  instance per backend (`break`), silently dropping additional instances of the same
  backend. Two llamacpp instances on `:8080`/`:8081` (legal under `auto_stop_server:
  false`) → `status --json` reports only one; a remote agent concludes the other is stopped.
- **Change:** emit one JSON entry per **running instance** (keyed by address), plus one
  `running:false` entry per enabled backend that has no running instance. Preserve exit
  codes (0 if any running, 1 if all stopped) and field names. Update the human path only
  if needed for parity (it already lists all instances).
- **Verify:** add `cli` tests (new `cli_test.go`): two httptest fakes of the same backend
  on different addresses → `cmdStatusJSON` emits two `running:true` entries with distinct
  addresses and returns 0; all-dead config → one `running:false` entry per enabled backend
  and returns 1. `go test ./internal/launcher/ -run TestCmdStatusJSON` passes.
- **Docs:** update TDD §3.2 `status --json` description to "one entry per running instance
  plus one per idle enabled backend"; `CHANGELOG.md` (Fixed).

## 6. Reconcile LM Studio parameter support: docs, display, and payload — DESIGN-CALL — ✅ DONE (2026-07-19)

NOTES (2026-07-19): Design question resolved by verification, per the user's
rule "verify the load API first". Both the official REST docs
(lmstudio.ai/docs/developer/rest/load) and the installed LM Studio 0.4.15's
own request schema (zod schema in the app bundle) agree: `POST
/api/v1/models/load` accepts `model`, `context_length`, `eval_batch_size`,
`physical_batch_size`, `parallel`, `flash_attention`, `speculative_decoding`,
`num_experts`, `offload_kv_cache_to_gpu`, `ttl_seconds`, `echo_load_config` —
and no GPU-offload field of any kind. Outcome: `batch_size`→`eval_batch_size`
and `flash_attn`→`flash_attention` are now sent (docs/display kept);
`gpu_layers` is stripped from the lmstudio docs column and the pop-up.
Extension under the same rule ("fields it actually accepts get sent"):
`parallel` is accepted by the API, so it is also sent, displayed, and marked
supported for lmstudio — one field beyond the three the item names.

- **Severity:** High. The shipped config and the profile popup tell users that
  `gpu_layers` / `batch_size` / `flash_attn` apply to LM Studio profiles; the load request
  never carries them, so the UI actively misreports what the model is doing.
- **Authoritative source:** none yet — this is the decision to make. `CONTEXT.md` (Profile
  = LLM Server + Model + parameter overrides) implies displayed params must be real.
- **Where:** `internal/launcher/backend_lmstudio.go:84-110` (`LoadModel` sends only
  `model` + `context_length`); `internal/launcher/defaults/config.yaml:169-188` (the
  parameter table promising the mappings); `internal/launcher/menu.go:441-487`
  (`formatProfileParams` renders "GPU offload: max/off", "Batch size", "Flash attention"
  for lmstudio profiles).
- **Design question:** does LM Studio's `/api/v1/models/load` REST API accept GPU-offload /
  batch / flash-attention fields? **If yes** → send them (with the documented mapping
  99→"max", 0→"off", `batch_size`→`eval_batch_size`) so the display becomes truthful.
  **If no** → correct `defaults/config.yaml`'s table to mark those `-` for lmstudio and
  drop the lmstudio branches from `formatProfileParams` so nothing unsent is displayed.
  Do **not** silently pick one — this is a feature-scope call for the maintainer.
- **Change:** implement the chosen direction end to end (payload + config table + display +
  README backend/param docs).
- **Verify:** if sending params, extend `backend_lmstudio_test.go` to assert the load
  payload includes the mapped fields for a profile that sets them; if correcting docs,
  assert `formatProfileParams` omits the unsent labels for an lmstudio profile. `go test
  ./internal/launcher/ -run 'TestLMStudio|TestFormatProfileParams'` passes.
- **Docs:** `README.md` backend/parameter tables, `defaults/config.yaml`, `CHANGELOG.md`.

## 7. Fix interactive-menu primary-instance selection — ✅ DONE (2026-07-19)

- **Severity:** Medium. The loaded-state menu renders against the wrong instance when
  several are running.
- **Authoritative source:** ADR-0006 (no canonical instance, but the loaded menu must
  pick an instance that actually has a model to show its model/log/config).
- **Where:** `internal/launcher/menu.go:29-42` (the instance-selection loop in
  `RunInteractiveMenu`).
- **Problem:** the unconditional `if primary == nil { primary = inst }` runs on the first
  iteration, so `primary` is fixed to the sort-first instance regardless of whether it has
  a model; the "first instance with a model" branch above it can never override it on later
  iterations. Trigger: idle LM Studio at `:1234` + Ollama with a model at `:11434` (sort
  order `lmstudio` < `ollama`) → the loaded menu shows the idle instance: empty model
  label, wrong "Show log", "Show model config" shows blank with "No matching profile".
- **Change:** track `firstWithModel` and `firstAny` separately; after the loop set
  `primary = firstWithModel` when any instance has a model, else `firstAny`. Keep
  `anyModel` / `anyServer` semantics unchanged (they already scan all instances).
- **Verify:** extract the selection into a small pure helper taking `[]*RunningInstance`
  and returning the chosen instance, and unit-test it in `menu_test.go`: idle-first +
  loaded-second → returns the loaded one; all idle → returns the sort-first; empty → nil.
  `go test ./internal/launcher/ -run TestPrimaryInstance` passes.
- **Docs:** `CHANGELOG.md` (Fixed).

## 8. Flag failed MCP mutating calls as tool errors — ✅ DONE (2026-07-19)

- **Severity:** Medium (security-adjacent). A failed `load`/`stop`/`unload` currently
  returns a success-shaped result to the untrusted agent.
- **Authoritative source:** TDD §15.2 "Result mapping" (a real failure should surface as a
  tool error; only informational negatives should pass through as content) and §3.3 exit
  codes (1 = informational negative; 2/3 = real error).
- **Where:** `cmd/llama-launcher-mcp/config.go:139-148` (the `run` result mapping).
- **Problem:** the heuristic "non-zero exit **with** stdout ⇒ informational negative,
  `IsError:false`" is defeated because every mutating subcommand prints progress to stdout
  before it can fail (`internal/launcher/progress.go` `newCLIProgress` emits "  Loading
  X\n" immediately). So a `load` that fails with exit 3 (error on stderr) still has
  non-empty stdout and is returned as success, burying "Error: …" in the text.
- **Change:** key `IsError` off the exit code, not stdout emptiness: exit 0 → success;
  exit 1 → informational negative (content, not error, per §3.3); exit ≥ 2 → tool error
  carrying stderr (and stdout for context). Keep the "status --json exits 1 but emits the
  array" case working (exit 1 stays non-error).
- **Verify:** extend the adapter tests with a fake CLI that prints progress to stdout then
  exits 3 → the tool result has `IsError:true` and contains the stderr message; a fake
  exiting 1 with a JSON array → `IsError:false` with the array intact. `go test
  ./cmd/llama-launcher-mcp/` passes.
- **Docs:** `CHANGELOG.md` (Fixed); TDD §15.2 result-mapping wording.

## 9. Repair and unify the LLM Server stop path — ✅ DONE (2026-07-19)

NOTES (2026-07-19): `ollama stop` arity confirmed against the installed
Ollama 0.32.1: with no argument it fails with "Error: accepts 1 arg(s),
received 0" (exit 1); usage is `ollama stop MODEL` — a model unload, not a
server stop. The arg-less call was removed; the pgrep/SIGTERM sweep is now
Ollama's TryStop mechanism. `EnsureStopped` (single caller: `StopInstance`)
was folded into an unexported `stopServerAt`; TryStop's error — previously
discarded — now surfaces, but only when the address is still serving
afterwards, so a hook that errors after the PID signal already worked (e.g.
`lms server stop` with the server gone) stays silent. ADR-0001's parenthetical
still names "`ollama stop`" as Ollama's stop mechanism — left untouched: the
item's docs scope is CHANGELOG + TDD §6.5 and the ADR's decision itself
(stop is unconditional) is unaffected.

- **Severity:** Medium. The graceful-stop mechanism for Ollama is dead, and `StopInstance`
  contradicts its own docstring while duplicating `EnsureStopped`.
- **Authoritative source:** ADR-0001 (stop is unconditional; both mechanisms attempted);
  TDD §6.5 (order: PID signal, then backend `TryStop`).
- **Where:** `internal/launcher/server.go:140-158` (`StopInstance`) vs `185-207`
  (`EnsureStopped`); `internal/launcher/backend_ollama.go:130-158` (`Ollama.TryStop`).
- **Problem:** (a) `Ollama.TryStop` runs `ollama stop` with **no** model argument;
  verify against the installed Ollama, but the CLI's `stop` requires a model name, so
  `cmd.Run()` returns an error at line 137 and the pgrep/SIGTERM fallback (140-157) is
  unreachable — the graceful path never runs and the returned error is misleading. (b)
  `StopInstance`'s docstring says backend `TryStop` is tried first, but the code signals
  the listening PID (`terminatePID`) before `EnsureStopped` runs `TryStop`, and
  `EnsureStopped` then re-derives the same PID/terminate logic.
- **Change:** (a) fix `Ollama.TryStop` — there is no "stop the Ollama server" CLI command
  (`ollama serve` is killed by signal; `ollama stop <model>` only unloads a model), so drop
  the arg-less `ollama stop` call and rely on the pgrep/SIGTERM path (or, if a live model
  name is available and the intent is unload, pass it explicitly). (b) make `StopInstance`
  delegate to a single stop routine so the PID-signal and backend-`TryStop` steps happen
  once in the documented order; remove the duplication. Preserve ADR-0001 (unconditional)
  and the SIGTERM→SIGKILL→port-release escalation.
- **NOTE (executor):** confirm `ollama stop` arity against the installed Ollama before
  landing; record the observed behaviour as a dated NOTES line under this item.
- **Verify:** unit-test the decision layer without real signals: `identifyBackend` against
  a fake llamacpp httptest server → "llamacpp"; against a dead port → `ErrNotRunning`;
  `StopInstance("garbage")` → invalid-address error; `StopInstance` on a dead addr →
  `ErrNotRunning`. `terminatePID` against a spawned `sleep 60` child → process gone after.
  `go test ./internal/launcher/ -run 'TestStopInstance|TestIdentifyBackend|TestTerminatePID'`
  passes.
- **Docs:** `CHANGELOG.md` (Fixed); reconcile TDD §6.5 with the final order.

## 10. Do not orphan a server when the health wait times out — DESIGN-CALL — ✅ DONE (2026-07-19)

NOTES (2026-07-19): Option B implemented as decided. The "refuse to
double-spawn" guard lives in `startManagedServer` — the single choke point
for managed forks — so it covers both listed sites (`loadProfileManaged`
and `EnsureServer`/`cmdStart`), via a new `StartupProber` capability
interface (`LlamaCpp.StartingUp`: /health answering 503). The refusal
applies to `load --restart` too — the design decision names no restart
exception, and the still-loading server cannot be stopped via the normal
stop path anyway (it fails every backend's health check, so
`identifyBackend` yields ErrNotRunning); the refusal/timeout errors
therefore point at `kill <PID>`. Docs beyond the item's list: TDD §5.3
(new interface) and §6.2 (managed start steps), alongside §10 and the
CHANGELOG.

- **Severity:** Medium. A slow-loading large model leaves a running `llama-server` behind
  and a confusing follow-up error.
- **Authoritative source:** TDD §6.2 (health wait up to 15s start / §6.1 load path) and
  §10 error handling.
- **Where:** `internal/launcher/server.go:296-305` and `520-528` (`WaitForHealth` failure
  after a managed start).
- **Problem:** `llama-server` answers `/health` with 503 while loading; a 30–70 GB GGUF on
  cold disk can exceed the 15s/30s windows. On timeout the spawned process is left running
  and the error does not say so; an immediate retry sees the address unhealthy, spawns a
  second `llama-server` on the same port, which dies within the 500ms grace with "address
  already in use" → misleading "server exited immediately".
- **Design question:** on health-wait timeout after a managed start, should the launcher
  (A) kill the just-spawned process, or (B) leave it (it may still be legitimately loading
  a huge model) and report its PID + log path, treating a 503 "still loading" distinctly
  from "unreachable" so a retry does not spawn a duplicate? Recommendation: **(B)** —
  killing a legitimately slow load is worse than reporting it; detect the "already loading
  at this address" case on retry and refuse to double-spawn. Decide before implementing.
- **Change:** implement the chosen behaviour; make the timeout error name the PID and log
  path so the user can inspect or stop it.
- **Verify:** `server_test.go` with a fake `/health` that stays 503 and a short timeout →
  the timeout error mentions the PID/log (option B) or the process is gone (option A); a
  fake that flips healthy mid-wait → returns promptly with success. `go test
  ./internal/launcher/ -run TestWaitForHealth` passes.
- **Docs:** `CHANGELOG.md`; TDD §10 row for the health timeout.

## 11. Restore the terminal cursor on popup exit — ✅ DONE (2026-07-19)

- **Severity:** Medium. With `auto_close` (default true) a popup action exits the process
  with the cursor left hidden.
- **Where:** `internal/launcher/ui.go:337-351` (`showPopup`).
- **Problem:** `showPopup` writes `escCursorHide` but never `escCursorShow`, and on the
  `term.MakeRaw` error path returns without waiting for a key or restoring the cursor.
  Trigger: menu → "Show model config" → press a key → the program exits leaving an
  invisible cursor (and the popup over a stale menu) until `tput cnorm`/`reset`.
- **Change:** emit `escCursorShow` (and clear the popup) on all return paths of
  `showPopup`, including the `MakeRaw` error path. Check for the same missing-restore
  pattern in sibling popup helpers (`showErrorPopup`, progress popups) and fix any that
  share it.
- **Verify:** this is terminal-side; assert via a small refactor that the escape-sequence
  writer receives `escCursorShow` after the read on every path (inject the writer), or
  document manual verification (`llama-launcher` → Show model config → key → cursor
  visible) as a NOTES line. `go build ./...`, `go vet ./...`.
- **Docs:** `CHANGELOG.md` (Fixed).

## 12. Fail fast on `start`/`start_server` for a managed backend without a profile — ✅ DONE (2026-07-19)

- **Severity:** Medium. Bare `start` cannot work for llamacpp (the default shipped
  backend) yet fails with an opaque process-exit error, and the MCP tool description
  claims it works.
- **Authoritative source:** ADR-0003 (llamacpp is restart-per-profile; the model is in the
  start args, so there is nothing to start without a profile).
- **Where:** `internal/launcher/cli.go:229-272` (`cmdStart`) and
  `cmd/llama-launcher-mcp/main.go:119-128` (`start_server` description).
- **Problem:** with only llamacpp enabled, `cmdStart` with no profile forks `llama-server`
  with no `-m`, which exits immediately ("--model is required") → user gets "server exited
  immediately after start" + log tail. The MCP `start_server` description says "Without a
  profile it starts the default backend with no model loaded" — false for a
  `ManagedLLMServer`.
- **Change:** when the resolved default backend is a `ManagedLLMServer` and no profile is
  given, fail fast with a clear message ("llamacpp requires a profile: llama-launcher start
  --profile <name>") and the config-error exit code, before forking. Fix the MCP
  `start_server` description to match.
- **Verify:** `cli_test.go`: `cmdStart` with a managed default backend and no profile →
  returns the config-error exit code and the actionable message, and does not fork. `go
  test ./internal/launcher/ -run TestCmdStart` passes.
- **Docs:** `CHANGELOG.md`; TDD §3.2 `start` row; README usage.

## 13. Harden discovery against hostile local servers (sanitise + bound reads) — ✅ DONE (2026-07-19)

NOTES (2026-07-19): Sanitisation strips a superset of the item's literal
byte list: C0 (`< 0x20`) and DEL (`0x7f`) as specified, plus the C1 range
(U+0080–U+009F), which some terminals also interpret as controls (e.g.
U+009B as CSI). Applied at both `RunningInstance` entry points
(`probeInstance` and `liveLoadedModel` — the latter also feeds progress
lines) and to LM Studio's server-supplied error message
(`extractLMStudioError`), the one other server-sourced string that reaches
the terminal. The 512 KB cap (`boundedBody`) covers every `io.ReadAll` and
JSON decode in the three backend files, including LM Studio's load/unload
reply reads; Ollama's load/unload `io.Copy(io.Discard, …)` drains stay
uncapped — they never buffer the body, and the item scopes the cap to the
`ReadAll`s and decoders. Oversized bodies are truncated at the cap, so the
"error rather than unbounded allocation" surfaces as the subsequent JSON
parse / discrimination failure (asserted by the new regression tests,
which fail without the fix).

- **Severity:** Medium (security). A local process squatting a configured port can feed
  malicious responses that the launcher parses and displays.
- **Authoritative source:** the threat model — the launcher parses HTTP from whatever
  answers on configured local ports; that data is untrusted.
- **Where:** display of `inst.ActiveModel` (`internal/launcher/discovery.go` population →
  `menu.go` / `cli.go` display sites); response reads in
  `backend_llamacpp.go`, `backend_ollama.go`, `backend_lmstudio.go` (health checks'
  `io.ReadAll` and the JSON decoders).
- **Problem:** (a) server-reported model names are printed raw to the terminal (status
  output, auto-refreshing menu header, "Show model config" popup); `visibleWidth`
  (`frame.go`) skips ESC sequences, so injected ANSI/OSC escapes pass through (screen/title
  spoofing, OSC 52 clipboard writes). (b) health checks `io.ReadAll` the body and listers
  `json.NewDecoder(...).Decode` with no size cap; the 2s timeout bounds duration, not
  size, so over loopback a squatter can deliver GBs and OOM the launcher — amplified by the
  menu re-probing every tick.
- **Change:** (a) strip/escape control bytes (`< 0x20` and `0x7f`) from server-reported
  strings (`ActiveModel`, and any other server-sourced string that reaches the terminal)
  at the point they enter `RunningInstance`, so all display sites are covered once. (b)
  wrap response bodies in `io.LimitReader` (a few hundred KB is ample for `/health`,
  `/v1/models`, `/api/ps`, `/props`) before `ReadAll`/`Decode`.
- **Verify:** `discovery`/backend tests: a fake server returning a model name containing
  `\x1b]0;pwn\x07` → the stored/displayed `ActiveModel` has the escape stripped; a fake
  returning a body larger than the cap → the read is bounded and returns an error rather
  than allocating unbounded. `go test ./internal/launcher/ -run
  'TestDiscover|TestHealthCheck'` passes.
- **Docs:** `CHANGELOG.md` (Security).

## 14. Fix the `endpoints`→`servers` migration message — ✅ DONE (2026-07-19)

- **Severity:** Medium. The error tells users to do something that cannot be expressed,
  and the mapping form silently drops a custom address, making the instance invisible.
- **Where:** `internal/launcher/config.go` (the `endpoints` deprecation error, ~353-354 /
  ~444-449, and `ServerConfig` decoding, ~56-87; `backendAddr` ~503-509).
- **Problem:** the message says "'endpoints' has been merged into 'servers' — move entries
  to the servers section", but `ServerConfig` accepts only `enabled` / `api_key`; the
  address an `endpoints` entry held cannot go there. A scalar `ollama: "localhost:11500"`
  fails the bool decode; the mapping form silently ignores an `addr:` key (yaml.v3 drops
  unknown fields), after which discovery probes the default address and the custom-port
  instance is never found.
- **Change (smallest correct):** reword the migration error to name the real target — set
  `host`/`port` in `defaults` or per profile (which is how a non-default address is
  actually configured). Do **not** add an `addr` field to `ServerConfig` in this item
  (that is a schema change; see the further-candidates note) — just stop instructing the
  impossible.
- **Verify:** `config_test.go`: a config using the old `endpoints:` key → the returned
  error text names the `host`/`port` migration path (assert the new wording). `go test
  ./internal/launcher/ -run TestValidate` passes.
- **Docs:** `CHANGELOG.md`.

---

# Phase 2 — Dead-code / decay cleanup

## 15. Remove state-file-era dead code — DESIGN-CALL

- **Severity:** Medium. Left-over from the v1.3.1 state-file removal; one piece costs a
  wasted HTTP round-trip on every discovery pass.
- **Authoritative source:** the stateless design (state files removed in 1.3.1); ADR-0007
  (drift is derived live in `liveParamDrift`, independently of discovery).
- **Where:** `internal/launcher/discovery.go:26,133-140` (`RunningInstance.ResolvedParams`
  and the `probeInstance` block that populates it, including the no-op
  `ResolvedParams.ContextSize = params.ContextSize` right after `ResolvedParams = *params`);
  `internal/launcher/server.go:29-41` (`IsServerAlive`, zero callers incl. tests);
  `internal/launcher/sysmem.go:260-269` (`FormatMemoryLine`/`percentString`, test-only callers).
- **Problem:** `RunningInstance.ResolvedParams` is written (discovery.go:135,
  server.go:~532/569) but read only by `discovery_test.go` — and populating it issues an
  extra `/props` request per llamacpp instance on every CLI command and every menu tick,
  for data nobody consumes. `IsServerAlive` has no callers. The two sysmem helpers have
  only test callers.
- **Design question:** is `ResolvedParams` intended to feed a future `status` display? If
  **yes**, wire it into `status`/`status --json` instead of deleting it (and keep the
  probe). If **no**, delete the field and the `QueryLiveParams` call inside `probeInstance`
  (keep `QueryLiveParams` itself — it is still used by `liveParamDrift`), delete
  `IsServerAlive`, and unexport-or-delete the two sysmem helpers with their tests. Confirm
  before removing, per the "don't delete intentional features" rule.
- **Change:** implement the chosen direction. The deletion path must not touch
  `QueryLiveParams` (live drift depends on it) — only its discovery-time invocation.
- **Verify:** `go build ./...` and `go vet ./...` clean; `go test ./...` passes with the
  now-removed symbols' tests deleted/updated; grep confirms no remaining references.
- **Docs:** `CHANGELOG.md` (Changed/Removed); TDD §7.1 `RunningInstance` struct if the
  field is removed.

---

# Phase 3 — Architecture deepening

These make the orchestration testable and remove duplication. They are larger and some
require agreeing an interface first — those are DESIGN-CALL. Run them after Phase 1/2 so
they build on the corrected behaviour.

## 16. Sink the duplicated adapter HTTP logic into `backend_http.go`

- **Rating:** Strong (low risk). No ADR touched — this is HTTP plumbing, not behaviour.
- **Where:** `internal/launcher/backend_llamacpp.go:56-84` and
  `backend_lmstudio.go:171-199` (`ListRunningModels` — byte-identical but for the
  receiver); the shared `HealthCheck`/load/unload skeleton across all three adapters;
  `internal/launcher/backend_http.go` (the existing correct seam).
- **Problem:** `ListRunningModels` is duplicated verbatim between llamacpp and LM Studio
  (both GET `/v1/models`, decode `data[].id`, skip empties). The GET/POST-decode-and-check
  skeleton repeats across adapters. A fix to one copy can miss the other.
- **Change:** add an OpenAI-style `/v1/models` lister helper (e.g.
  `openAIModelList(addr, apiKey)`) to `backend_http.go` and have llamacpp and LM Studio
  call it; factor the shared "GET/POST JSON → map status to error → decode" shape into a
  helper where it does not obscure each adapter's genuinely different parts (endpoint path,
  payload keys, error extraction). Do not change any observable behaviour or health-check
  discrimination.
- **Verify:** the existing backend tests
  (`backend_llamacpp_test.go`, `backend_lmstudio_test.go`,
  `backend_ollama_test.go`, `backend_http_test.go`) still pass unchanged: `go test
  ./internal/launcher/ -run 'Backend|LlamaCpp|LMStudio|Ollama|ListRunningModels'`.
- **Docs:** TDD §5.2 `backend_http.go` responsibility line; `CHANGELOG.md` (Changed) if any
  behaviour is observable (it should not be).

## 17. Extract a testable activation seam behind `LoadProfile` — DESIGN-CALL

- **Rating:** Strong (the highest-leverage structural change), but requires interface design.
- **Where:** `internal/launcher/server.go` — `LoadProfile` and its fan-out
  (`EnsureServer`/`StartServer`/`loadProfileManaged`/`loadProfileExternal`/
  `connectExternalServer`/`startManagedServer`/`StopInstance`/`EnsureStopped` and the
  auto-stop/auto-unload loops).
- **Problem:** the whole activation orchestration (health → stop → start → wait → load,
  and the managed/external fork) has **zero** tests — only its pure leaf helpers are
  covered — because every path calls real `exec.Command`, `lsof`, `syscall.Kill`, and live
  HTTP. The ADR-0004/0007 decision logic (which Phase-1 items 1 and 3 touch) is exactly
  what is untested. The target address is also re-derived inline ~7× instead of carried.
- **Design question:** what is the seam? Options: (a) an interface capturing the
  process/health/probe operations (`start`, `stop`, `healthy`, `loadedModel`, `liveParams`)
  that `LoadProfile` drives, with a real adapter over `exec`/HTTP and a fake for tests; (b)
  inject the already-existing `LLMServer` plus a small `processController` for the
  fork/kill parts. Agree the interface (and that `LoadProfile` carries one `targetAddr`
  value) before implementing. This is a design conversation — treat it like the grilling
  step of `/improve-codebase-architecture`.
- **Change:** introduce the agreed seam; make `LoadProfile` orchestrate against it; add
  the first orchestration tests (idempotent no-op, `--restart`, auto_stop vs auto_unload
  matrix) using the fake. Preserve every ADR behaviour.
- **Verify:** new `server_test.go` orchestration tests pass against the fake with no real
  processes; `go test ./internal/launcher/` green; `go vet ./...`.
- **Docs:** TDD §5.2/§6.1; a short ADR if the seam is a durable decision; `CHANGELOG.md`.

## 18. Unify the Unload/Stop orchestration; CLI and menu become formatters — DESIGN-CALL, DEPENDS: 17

- **Rating:** Worth exploring.
- **Where:** `internal/launcher/cli.go` (`cmdUnload`, `cmdStop`, `resolveTargetInstance`)
  vs `internal/launcher/menu.go` (`doUnloadModel`, `doStopServer`).
- **Problem:** both sides independently discover instances, filter to loaded models,
  disambiguate multiple targets, and branch on `b.(ManagedLLMServer)` to choose
  `StopInstance` vs `UnloadInstanceModel`. The "unload on a managed backend means stop the
  server" rule (ADR-0003/0004) is encoded twice (`cli.go` and `menu.go`); deleting either
  handler resurrects the logic in the other.
- **Design question:** what does the single entry point return so both a CLI printer and a
  TUI progress sink can render it (a result struct? a callback)? Agree the shape.
- **Change:** one `Unload`/`Stop` entry point in `server.go` (built on item 17's seam)
  returning the agreed result; `cli.go` and `menu.go` reduce to formatting + target
  selection. Behaviour unchanged.
- **Verify:** existing CLI/menu behaviour preserved; add tests for the unified entry point
  (managed → stop, external → unload) via the item-17 fake. `go test ./internal/launcher/`.
- **Docs:** TDD §5.2; `CHANGELOG.md`.

## 19. Move backend parameter display behind the `LLMServer` seam — DESIGN-CALL, DEPENDS: 6

- **Rating:** Worth exploring. Resolves the last backend-name string-switch outside the
  backend files, and depends on the item-6 decision about which LM Studio params are real.
- **Where:** `internal/launcher/menu.go:441-487` (`formatProfileParams`, the
  `profile.Backend == "llamacpp"` / `== "lmstudio"` ladder).
- **Problem:** `formatProfileParams` is the only place outside `backend_*.go` that hard-codes
  backend names, and it encodes each backend's parameter vocabulary and semantics (the
  99→"max"/0→"off" mapping). A new backend needs an edit here that the type system will not
  flag; and today it displays params LM Studio never receives (item 6).
- **Design question:** what does the backend expose — a method returning display labels for
  the params it honours, or a `paramSpec` the backend owns that the menu renders? Decide
  after item 6 settles which LM Studio params exist.
- **Change:** add the agreed method to the `LLMServer` (or a capability interface); have
  `formatProfileParams` render from it instead of switching on backend name. Only display
  params the backend actually applies.
- **Verify:** `menu_test.go` `TestFormatProfileParams*` updated to drive the backend-owned
  spec; a new backend would not require editing `menu.go`. `go test ./internal/launcher/
  -run TestFormatProfileParams` passes.
- **Docs:** TDD §5.3 (new interface method); `CHANGELOG.md`.

---

## Further architecture candidates (recorded, NOT scheduled)

Do not implement these without a dedicated design pass — they are here so the next
architecture review does not re-discover them:

- **Table-driven `ProfileParams` field registry.** The ~19 params are hand-enumerated in
  `mergeParams`, `paramDrift`, `BuildServerArgs`, `formatProfileParams`, and `cmdListJSON`;
  a missed site is a silent behavioural gap (this class of bug underlies items 3 and 6).
  A field registry (name, getter, arg-flag, formatter) that all sites iterate would
  collapse them — but Go makes reflection-free enumeration awkward; friction is real, the
  fix is speculative.
- **Split menu "compute action list for state X" (pure, testable) from "render via TUI or
  simple".** `menu.go` (~893 lines) keeps hand-maintained TUI/non-terminal twins with
  parallel index bookkeeping; the menu *flow* is untestable through a real terminal today.
- **MCP subcommand/flag contract.** The adapter hard-codes CLI subcommand names and flags
  with no test that cross-checks them against `cli.go`'s real parser; a rename in `cli.go`
  keeps every adapter test green while runtime breaks. A shared constant surface or a
  contract test driving the real CLI binary would close it (ADR-0008 keeps the shim,
  so this is about the untested contract, not the shell-out design).

## What looked good (protect — do not refactor destructively)

- `memformat.go` — a genuinely deep module: a tiny `CompileMemoryTemplate` + `Render`
  interface hiding tokenising, style tags, eighth-block bar rendering, and memoisation;
  well tested including a benchmark.
- `backend_http.go` + the `LLMServer` registry and capability interfaces
  (`ManagedLLMServer`, `ModelLister`, `LiveParamsQuerier`, `PIDTracker`,
  `apiKeyConfigurable`) — the right extension seam; items 16/19 push *more* behind it.
- `cmd/llama-launcher-mcp/allowlist.go` — small interface over IP/CIDR/hostname/interface
  matching with a safe loopback default, thoroughly tested and cleanly stubbable. The core
  MCP trust boundaries (peer-IP match, loopback default, non-`0.0.0.0` bind, genuine
  read-only tool unregistration) are all correct; item 4 is the one gap and it is in a
  *read* tool's argument handling, not the allowlist.
