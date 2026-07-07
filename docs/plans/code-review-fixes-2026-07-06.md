# Code Review Fixes — 2026-07-06

Implementation plan for the defects found in the 2026-07-06 whole-repository code
review (`code-review-2026-07-06.md` in the repo root). Each numbered `## N.`
section below is one self-contained work item: one implementer, one verifier, one
commit. Work them in order — later items assume earlier ones are landed.

## How to work this plan

- **Coding standards are mandatory.** Read `skills/coding-standards/SKILL.md` (base
  `references/coding-standards.md` + the Go extension `references/coding-standards.go.md`,
  and `references/testing.md` + `references/testing.go.md` for any test work) and follow
  them for every item. A clear violation is a verifier FAIL. If that repo-relative path
  is missing, use `~/.claude/skills/coding-standards/SKILL.md` (item 15 fixes the broken
  path reference).
- **Authoritative source precedence.** Where an item's fix and a doc disagree, the
  **ADRs in `docs/adr/` and the code review report win**, in that order; the TDD and
  README are downstream docs to be *updated to match*, never treated as the spec when
  they conflict with an ADR. Each item names its authority.
- **Every behavioural fix ships a test.** Per coding standards, an item that changes
  behaviour must add or adjust a Go test that would fail before the fix and pass after,
  unless the item body says otherwise. Tests are `net/http/httptest`-based and must pass
  under `go test ./...` with no external dependencies.
- **Update docs in the same commit.** Per `llama-launcher.TDD.md` §14, when an item
  changes behaviour, config schema, subcommands, or error handling, update
  `llama-launcher.TDD.md`, `README.md`, `CHANGELOG.md`, and any touched ADR in the same
  change. Each item lists the docs it must touch.
- **Plan-wide verify command:** `go build ./... && go vet ./... && go test ./...` from
  the repo root must pass for every item before it is marked done. Items add item-specific
  assertions on top of that.
- **DESIGN-CALL items** (3 and 4) carry an unresolved product decision. The implementer
  must stop and get the decision before writing code; the recommended option is stated so
  the user can confirm quickly.

## Explicitly NOT in this plan

- Restructuring the three-way profile-row rendering split (`cmdList` /
  `buildSimpleProfileLines` / `buildProfileItems`) into one shared builder. Item 14 fixes
  only the concrete `cmdList` alignment bug; the consolidation is a separate
  `/improve-codebase-architecture` pass.
- Any change to the documented ADR-0008 trust model (source-IP allowlist, no token) or
  ADR-0002/0003 core decisions. The security items harden the *implementation* of the
  existing model, not the model itself.
- Cross-platform (non-macOS) support.

---

## 1. Automatic log cleanup must skip running servers' logs — ✅ DONE (2026-07-06)

NOTES (2026-07-06): Also touched `internal/launcher/backend_ollama.go` — it is the
second `createLogPath` call site the item's "Both call sites … have `cfg` in scope"
sentence refers to, though the Files list names only `server.go` for the call chain.
`createLogPath` now takes `(cfg *Config, name string)` (log dir and retention read
from cfg), and `autoCleanupLogs` takes `(cfg *Config)` with the retention nil-guard
moved inside.

**Severity:** Critical (silent data loss in normal use). **Authority:** the review's
"always skip running servers' logs" invariant, realised in `cleanupLogs`/`activeLogFiles`;
TDD §9.1 ("always skips log files belonging to running servers").

**What's wrong:** `autoCleanupLogs` (`internal/launcher/log_cleanup.go:121-124`) calls
`cleanupLogs(nil, …)`. `activeLogFiles(nil)` (`log_cleanup.go:107-119`) returns an empty
map, so the automatic retention path — triggered by `createLogPath` whenever
`log_retention` is set (the shipped default is `7`) — deletes the still-open log of a
server that has been running longer than the retention window. The server keeps writing to
the unlinked inode and `llama-launcher logs` then reports no managed log.

**Change:** Thread the live `*Config` from `createLogPath`'s caller through
`autoCleanupLogs` into `cleanupLogs` so `activeLogFiles(cfg)` populates the running-server
skip set on the automatic path exactly as `logs clean` already does. Both call sites of the
cleanup already have `cfg` in scope — do not introduce a package global.

**Files:** `internal/launcher/log_cleanup.go`, `internal/launcher/server.go` (the
`createLogPath` call chain), `internal/launcher/log_cleanup_test.go`, `CHANGELOG.md`.

**Out of scope:** Changing the filename-timestamp age logic or the `logs clean` CLI path
(they already pass `cfg`).

**Verify / Acceptance:**
- New test: a healthy httptest llama.cpp stand-in at the config address with a matching
  `llamacpp-<old-timestamp>.log` in `cfg.LogDir`; call the automatic cleanup with a zero
  retention window and assert that log **survives** while a stale `ollama-<old>.log` in the
  same dir is removed.
- `go build ./... && go vet ./... && go test ./...` passes.

---

## 2. Idempotent llamacpp load must not report false parameter drift — ✅ DONE (2026-07-06)

NOTES (2026-07-06): Also touched `llama-launcher.TDD.md` (not in this item's Files
list) — the §12 test-table row for `TestParamDrift` documented the old
"set-vs-unset is reported" semantics and was updated to match, per the plan-wide
"update docs in the same commit" rule. `backend_llamacpp.go` was not changed: the
`QueryLiveParams` docstring already describes the skip behaviour this fix makes true.

**Severity:** High (cross-validated). **Authority:** ADR-0007 (drift notice is for *real*
divergence between the running server and the resolved profile); `QueryLiveParams`'s
contract.

**What's wrong:** `paramDrift` (`internal/launcher/server.go:418-462`) flags drift whenever
one side is nil (`if a == nil || b == nil || *a != *b`), but `QueryLiveParams`
(`backend_llamacpp.go:88-129`) only ever populates 7 of the 17 compared fields from
`/props` (`gpu_layers`, `threads`, `threads_batch`, `batch_size`, `flash_attn`,
`cont_batching`, `mlock`, `no_mmap`, `embedding`, `jinja` are never reported). With the
shipped default config setting those fields in `defaults:`, the second `load` of any
llamacpp profile — the ADR-0007 no-op path (`server.go:329-345`) — prints a bogus
"parameters have drifted… run `--restart`" notice that `--restart` can never clear.

**Change:** In the **live-drift comparison path only** (`liveParamDrift`, `server.go:399-408`),
treat a field the backend does not report (nil `live` value) as "unknown — skip", not
"unset — drifted". Preferred implementation: make `paramDrift` skip any field where *either*
side is nil (both operands must be non-nil to be comparable), and update the `paramDrift`
docstring, which currently claims nil-vs-value drifts. Confirm this does not weaken the
genuine drift case the ADR-0007 test asserts (a value that changed on both sides). Do **not**
special-case field names in `liveParamDrift`.

**Files:** `internal/launcher/server.go`, `internal/launcher/server_test.go`,
`internal/launcher/backend_llamacpp.go` (only if the `QueryLiveParams` docstring at
:88-90 needs correcting), `CHANGELOG.md`.

**Depends on:** none. (Item 3 is related but independent — this fix must hold regardless of
whether sampling params are emitted.)

**Verify / Acceptance:**
- Extend `TestParamDrift` (`server_test.go`): a `live` set with only the 7 `/props` fields
  populated and a `fresh` set with all fields populated (matching values on the shared 7)
  yields **no drift**; a genuinely changed shared field (e.g. `context_size` 4096→8192)
  still yields drift naming that field.
- `go test ./internal/launcher/ -run TestParamDrift` passes; full `go test ./...` passes.

---

## 3. llamacpp sampling parameters: apply them or drop the promise — DESIGN-CALL — ✅ DONE (2026-07-06)

NOTES (2026-07-06): User chose Option A. Flag spellings verified against the
installed `/opt/homebrew/bin/llama-server --help`: `--temp` (alias
`--temperature`), `--repeat-penalty`, `--top-k`, `--top-p`, `--min-p` all exist
as documented. Also added the five flags to the TDD §8 flag-mapping table
(covered by the item's "TDD only if wording needs tightening" Files entry).

**Severity:** High (cross-validated). **Authority:** `defaults/config.yaml:184-188` +
README backend table + TDD §4.2 all promise these as llamacpp-applicable.

**Q (design decision needed before coding):** Five sampling params — `temperature`,
`repeat_penalty`, `top_k`, `top_p`, `min_p` — are configured, shipped in `defaults:`, and
documented as llamacpp "yes", but `BuildServerArgs` (`backend_llamacpp.go:164-228`) never
emits them, so llama-server runs with its own defaults. **Option A (recommended): apply
them** — emit `--temp`, `--repeat-penalty`, `--top-k`, `--top-p`, `--min-p` when the
pointers are set. **Option B: walk back the promise** — remove the five from the config
table/shipped defaults and from the `paramDrift` comparison. Choose A or B before
implementing.

**What's wrong:** User-configured sampling is silently ignored (Option A fixes) *or* the
docs over-promise (Option B fixes). Either way the config, code, and drift comparison must
end up consistent.

**Change (Option A):** In `BuildServerArgs`, append each flag when its pointer is non-nil,
placed before `extra_args` and the `--api-key` block so user overrides win (match the
existing ordering comment at :219-223). Verify the exact llama-server flag spellings against
the installed `llama-server --help` before committing.
**Change (Option B):** Remove the five keys from `defaults/config.yaml`, the README backend
table, TDD §4.2, and drop them from `paramDrift`'s comparison list (`server.go:457-461`).

**Files (A):** `internal/launcher/backend_llamacpp.go`, `internal/launcher/backend_llamacpp_test.go`,
`README.md`/`llama-launcher.TDD.md` only if wording needs tightening, `CHANGELOG.md`.
**Files (B):** `internal/launcher/defaults/config.yaml`, `internal/launcher/server.go`,
`README.md`, `llama-launcher.TDD.md`, `internal/launcher/server_test.go`, `CHANGELOG.md`.

**Depends on:** item 2 (both touch the parameter/drift surface; land the drift fix first).

**Verify / Acceptance:**
- Option A: extend `TestBuildServerArgs` (or the arg-assembly test in
  `backend_llamacpp_test.go`) to assert each sampling flag appears with its value when set
  and is absent when the pointer is nil.
- Option B: a test asserting the removed keys no longer appear in the example config and are
  not compared by `paramDrift`.
- Full `go test ./...` passes.

---

## 4. LM Studio parameter mappings: implement them or correct the docs — DESIGN-CALL — ✅ DONE (2026-07-06)

NOTES (2026-07-06): User chose Option A. Field names verified against LM Studio's
REST docs (lmstudio.ai/docs/developer/rest/load — `POST /api/v1/models/load`
accepts `model`, `context_length`, `eval_batch_size`, `flash_attention`,
`num_experts`, `offload_kv_cache_to_gpu`, `echo_load_config`). `batch_size` and
`flash_attn` are implemented as `eval_batch_size`/`flash_attention` per Option A.
The endpoint has **no** GPU-offload field — the documented 99→"max"/0→"off"
`gpu_layers` mapping is not implementable over this API (`offload_kv_cache_to_gpu`
is KV-cache placement, wrong semantics; the "max"/"off" GPU knob exists only in
the `lms` CLI/SDKs, not the REST API) — so `gpu_layers` alone got the Option-B
treatment: dropped from the config/README parameter table and the popup
(`menu.go`), with `TestFormatProfileParams_GPULayers_LMStudio` now asserting no
GPU line renders for lmstudio (llamacpp display coverage kept in a new
`TestFormatProfileParams_GPULayers_LlamaCpp`). Also touched `README.md` and
`llama-launcher.TDD.md` (parameter table / test-table rows) under the item's
"docs only if wording needs tightening" entry, and `menu.go`/`menu_test.go` from
the Files (B) list for the popup correction.

**Severity:** High. **Authority:** `defaults/config.yaml:171-177` + README backend table
promise `gpu_layers` (99→"max"/0→"off"), `batch_size` (→`eval_batch_size`), `flash_attn`
for lmstudio; the "Show model config" popup renders the mapping.

**Q (design decision needed before coding):** `LMStudio.LoadModel`
(`backend_lmstudio.go:84-110`) sends only `model` and `context_length`, yet the docs and the
TUI popup (`menu.go:444-457`) tell users the mapped `gpu_layers`/`batch_size`/`flash_attn`
are in effect. **Option A (recommended): implement the documented payload fields** in
`LoadModel` (map `gpu_layers` 99→"max"/0→"off"/other→number, `batch_size`→`eval_batch_size`,
`flash_attn`). **Option B: correct the docs** — drop the mappings from the config table,
README, and the popup. Choose before implementing. Confirm the LM Studio load-API field
names (`gpu`/`eval_batch_size`/etc.) against LM Studio's API before choosing A.

**What's wrong:** A user setting `gpu_layers: 0` on an lmstudio profile expecting CPU-only
still loads with LM Studio's GPU default, and the popup misreports the setting as active.

**Files (A):** `internal/launcher/backend_lmstudio.go`, `internal/launcher/backend_lmstudio_test.go`,
`CHANGELOG.md`; docs only if wording needs tightening.
**Files (B):** `internal/launcher/defaults/config.yaml`, `README.md`,
`internal/launcher/menu.go` (remove the popup mapping), `internal/launcher/menu_test.go`,
`llama-launcher.TDD.md`, `CHANGELOG.md`.

**Depends on:** none.

**Verify / Acceptance:**
- Option A: extend `TestLMStudioLoadModel` to assert the JSON payload carries the mapped
  fields (`gpu_layers: 99` → the "max" mapping; `0` → "off"; `batch_size` →
  `eval_batch_size`) via the httptest request body.
- Option B: `TestFormatProfileParams_GPULayers_LMStudio` updated/removed to match the popup
  no longer claiming an unsent mapping.
- Full `go test ./...` passes.

---

## 5. Sanitize MCP tool target/profile arguments (read-only bypass + `-f` hang) — ✅ DONE (2026-07-06)

**Severity:** High (security). **Authority:** ADR-0008 (`--read-only` "exposes only read
tools"); the review's `[Security] H-01`.

**What's wrong:** `tail_log` (a read tool, present even under `--read-only`) forwards its
`target` as a raw CLI positional via `argsFor("logs", args.Target)`
(`cmd/llama-launcher-mcp/main.go:90-95`, `main.go:158-163`). `cmdLogs`
(`internal/launcher/cli.go:592-605`) treats `args[0]=="clean"` as the destructive
`logs clean` subcommand and `-f`/`--follow` as a flag. A prompt-injected agent can therefore
call `tail_log{"target":"clean"}` to delete logs **through the read surface** or
`tail_log{"target":"-f"}` to make the adapter block forever. `stop_server`/`unload_model`
share the same unvalidated `argsFor` positional.

**Change:** In the adapter, validate every forwarded positional (`target`, `profile`) before
`argsFor`/`run`: reject values that start with `-` or exactly match a launcher subcommand
keyword (`clean`, and defensively the other subcommand names), returning an MCP tool error.
Centralize this in one helper used by `tail_log`, `stop_server`, and `unload_model` so no
tool can select a different subcommand. Do not change the CLI's own `logs`/`stop`/`unload`
parsing.

**Files:** `cmd/llama-launcher-mcp/main.go` (or a small new helper in the same package),
`cmd/llama-launcher-mcp/config.go` if the helper lives there, a new/extended test in
`cmd/llama-launcher-mcp/`, `docs/adr/0008-mcp-control-plane-adapter.md` (note the
argument-validation hardening), `CHANGELOG.md`.

**Verify / Acceptance:**
- New adapter test: the `tail_log` handler given `target:"clean"` and `target:"-f"` returns
  a tool error and does **not** invoke the CLI (assert via a stubbed/fake runner, mirroring
  the existing `cmd/llama-launcher-mcp/config_test.go` fake-CLI pattern); a legitimate
  `host:port` or backend name is still forwarded.
- Full `go test ./...` passes.

---

## 6. Add HTTP server timeouts and cap MCP tool output size — ✅ DONE (2026-07-06)

NOTES (2026-07-06): Also touched `llama-launcher.TDD.md` (not in this item's Files
list) — §15.1 now documents the listener timeouts and §15.2's result-mapping
paragraph the 1 MiB per-stream output cap, per the plan-wide "update docs in the
same commit" rule. Timeout values chosen: `ReadHeaderTimeout` 10 s, `IdleTimeout`
2 min, `WriteTimeout` 10 min (must outlast `load_profile`, whose model-load wait
is up to 5 min via `modelLoadTimeout` plus health/stop grace periods).

**Severity:** Medium (security). **Authority:** the review's `[Security]` timeout/output
finding; complements item 5.

**What's wrong:** `srv := &http.Server{Addr: cfg.listen, Handler: mux}`
(`cmd/llama-launcher-mcp/main.go:61`) sets no `ReadHeaderTimeout`/`WriteTimeout`, and
`run` (`cmd/llama-launcher-mcp/config.go:130-134`) accumulates subprocess stdout into an
unbounded `strings.Builder`.

**Change:** Set sensible `ReadHeaderTimeout` and `WriteTimeout` (and `IdleTimeout`) on the
`http.Server`. Bound captured stdout/stderr to a fixed maximum (e.g. wrap the capture in a
size-limited writer) and note truncation in the returned content when the cap is hit.

**Files:** `cmd/llama-launcher-mcp/main.go`, `cmd/llama-launcher-mcp/config.go`,
`cmd/llama-launcher-mcp/config_test.go`, `CHANGELOG.md`.

**Verify / Acceptance:**
- Test: `run` against a fake CLI that emits more than the cap returns truncated content
  (with a truncation marker) rather than the full unbounded output.
- Full `go test ./...` passes; `go vet ./...` clean.

---

## 7. Bound HTTP response reads from probed servers with `io.LimitReader` — ✅ DONE (2026-07-06)

NOTES (2026-07-06): Beyond the review's cited health/model-list//props sites,
the same files' remaining body reads were bounded too, per the Change's "wrap
each response body": LM Studio's load/unload error-body reads and Ollama's
load/unload response drains (`io.Copy(io.Discard, …)`), all at the 8 KiB
status cap. `/props` uses the 1 MiB cap (its response can carry a multi-KB
chat template), not the "few KB" class. Also touched `llama-launcher.TDD.md`
(not in this item's Files list) — module/test tables for `backend_http.go`
and the new oversized-body tests — per the plan-wide docs rule.

**Severity:** Medium (security). **Authority:** the review's `[Security]` unbounded-read
finding; threat model includes a process squatting on a configured port.

**What's wrong:** Backend health checks, model listing, and `/props` reads consume the
response body with no size limit (`io.ReadAll` / `json.NewDecoder(resp.Body)`), bounded only
by a 2 s client timeout: `backend_llamacpp.go:32,73,118`, `backend_ollama.go:38`,
`backend_lmstudio.go:98,188`. A squatter can stream gigabytes within the timeout, and
`DiscoverRunningInstances` probes several addresses in parallel.

**Change:** Wrap each response body in `io.LimitReader` with a small cap appropriate to the
payload (a few KB for health/status, larger but still bounded for model lists) before reading
or decoding. Prefer a shared helper (e.g. in `backend_http.go`) so all backends use the same
limit.

**Files:** `internal/launcher/backend_http.go`, `internal/launcher/backend_llamacpp.go`,
`internal/launcher/backend_ollama.go`, `internal/launcher/backend_lmstudio.go`, the
corresponding `*_test.go`, `CHANGELOG.md`.

**Verify / Acceptance:**
- Test: an httptest server returning a body larger than the cap is read/parsed without
  reading past the limit (assert the reader stops at the cap; the existing health/model
  tests still pass).
- Full `go test ./...` passes.

---

## 8. Confirm and (if needed) redact secrets in `tail_log` output — investigate first — ✅ DONE (2026-07-06)

NOTES (2026-07-06): Negative result — no leak, no code change. Confirmed empirically
against the installed llama-server (Homebrew build 9870): loaded a real llamacpp
profile via `llama-launcher --config <tmp> load keytest` with
`servers.llamacpp.api_key: "LLTESTKEY-8f3a9c"` on 127.0.0.1:18234, verified the key
was active (`/v1/chat/completions` → 401 without/with wrong key, 200 with correct
key), exercised authorized + unauthorized requests, then checked
`llama-launcher logs` and grepped the managed log directly: zero occurrences of the
key or any fragment. llama-server does not echo argv at default verbosity; auth
failures log only `unauthorized: Invalid API Key`; and `startManagedServer`
(server.go) writes nothing of its own to the log (child stdout/stderr only).
Residual, out of default path: if a user opts into verbose logging via
`extra_args: ["-lv", "N"]`, llama-server prints `api_keys: ****3a9c` — self-masked
by llama.cpp to the last 4 chars, not a plaintext leak.

**Severity:** Medium (security, uncertain). **Authority:** ADR-0008 (client "receives no
secret it could leak"); the review's `[Security]` key-leak finding.

**What's wrong (to confirm):** llama-server is started with `--api-key <key>` in argv and its
stdout/stderr is captured to the managed log; `tail_log` returns that raw log to the network
client with no redaction (`redactAPIKeyArgs` masks only display surfaces). If the installed
llama-server echoes its command line at startup, the plaintext key reaches the
prompt-injectable agent.

**Change:** First **confirm** by loading a profile with an `api_key` and running
`llama-launcher logs` to check whether the key appears in the captured log. If it does not,
close this item with a NOTES line recording the negative result and no code change. If it
does, scrub known secret tokens from `tail_log` output in the adapter (or restrict `tail_log`
to lines the launcher itself writes) and add a test.

**Files (if a fix is warranted):** `cmd/llama-launcher-mcp/` (redaction at the tool
boundary) or `internal/launcher/` (source-side masking), the corresponding test,
`CHANGELOG.md`.

**Verify / Acceptance:**
- If no leak: NOTES line documenting the check and its command/result; no code change.
- If leak: a test feeding a log line containing a configured key through the tool output
  path asserts the key is masked.
- Full `go test ./...` passes.

---

## 9. `status --json` must emit one entry per running instance — ✅ DONE (2026-07-07)

**Severity:** Medium (cross-validated). **Authority:** ADR-0006 (instances keyed by
`host:port`); §6.6 status prints one row per live instance. TDD §3.2 currently documents the
buggy one-per-backend schema and must be updated.

**What's wrong:** `cmdStatusJSON` (`internal/launcher/cli.go:514-521`) takes the first
`inst.Backend == name` match and `break`s, so with two instances of one backend on different
ports (supported when `auto_stop_server: false`) `status --json` — and the MCP `server_status`
tool — silently drops the second, while human `status` shows both.

**Change:** Emit one JSON entry per discovered running instance (keyed by address), plus a
`running:false` entry only for enabled backends that have no running instance. Keep the
documented field set (`backend`, `running`, `address`, `active_profile`, `active_model`,
`pid`, `uptime_seconds`) and the exit-code semantics (0 if any running, else 1). Update TDD
§3.2 to describe the per-instance schema.

**Files:** `internal/launcher/cli.go`, `internal/launcher/cli_test.go` (new — see item 17),
`llama-launcher.TDD.md`, `CHANGELOG.md`.

**Verify / Acceptance:**
- Test (may land with item 17's `cli_test.go`): with two fake instances of the same backend
  on different ports, `status --json` output parses to two array entries with distinct
  `address` values; with none running, one `running:false` entry per enabled backend and
  exit 1.
- Full `go test ./...` passes.

---

## 10. Fix `Ollama.TryStop` (broken call + host-wide sweep) — ✅ DONE (2026-07-07)

NOTES (2026-07-07): Chose the delete option (Option B) — `Ollama.TryStop` is now a
no-op, relying on the address-scoped `lsof`/PID path in `EnsureStopped`. `ollama`
is **not installed** on the target machine (`which ollama` → not found), so the
`ollama stop` arity could not be confirmed empirically; per the item's own "if that
is not reliable across versions" clause this favours Option B, and `ollama stop`'s
documented arity is `ollama stop <model>` (exactly one model arg, unloads a model
without stopping `ollama serve`, subcommand absent on older versions). Also touched
`llama-launcher.TDD.md` module-table row (§7.1, `backend_ollama.go`) and the
`TryStart`/`TryStop`-pair paragraph in addition to §6.5, since both named the removed
`ollama stop` mechanism — per the plan-wide "update docs in the same commit" rule.

**Severity:** Medium (cross-validated). **Authority:** ADR-0001 (stop is unconditional but
per-instance); ADR-0006 (address-keyed instances); TryStop is documented "best-effort,
errors reported but non-blocking".

**What's wrong:** `Ollama.TryStop` (`backend_ollama.go:130-158`) runs
`exec.Command(binary, "stop")` with no model argument; the ollama CLI requires a model name,
so it errors on every call and the `pgrep -f "ollama serve"` SIGTERM fallback below is dead
in practice. `EnsureStopped` (`server.go:192`) discards the error. If the sweep ever ran it
would kill every ollama instance on the host regardless of `addr`.

**Change:** Stop the specific loaded model — resolve it via `ListRunningModels(addr)` and run
`ollama stop <model>` — **or**, if that is not reliable across versions, delete `TryStop`'s
body and rely on the unconditional lsof/PID path (which already handles the listener).
Whichever is chosen, remove the unscoped host-wide `pgrep` sweep. Additionally, surface
`TryStop`'s error in `EnsureStopped` (report to stderr, still non-blocking) to match the
documented behaviour. Confirm the `ollama stop` arity on the target install and record it in a
NOTES line.

**Files:** `internal/launcher/backend_ollama.go`, `internal/launcher/server.go` (error
reporting in `EnsureStopped`), `internal/launcher/backend_ollama_test.go` /
`server_test.go`, `CHANGELOG.md`, and TDD §6.5 if the stop mechanism wording changes.

**Verify / Acceptance:**
- Test: `TryStop` with a fake model lister issues `ollama stop <model>` (or, for the delete
  option, `EnsureStopped` returns nil once the health check fails via the PID path); no
  host-wide `pgrep` sweep remains.
- Full `go test ./...` passes.

---

## 11. Menu "primary instance" must be the one with a model loaded — ✅ DONE (2026-07-07)

**Severity:** Medium. **Authority:** the review's `[Correctness]` finding; CONTEXT.md (no
global "primary" — but the menu's local selection should not surface an idle instance as the
loaded one).

**What's wrong:** In `menu.go:29-42` the `if primary == nil { primary = inst }` fallback runs
inside the loop, so the first discovered instance wins even when a *later* instance is the one
with a model. With an idle LM Studio (sorts first) plus Ollama-with-a-model, `runLoadedMenu`
shows the wrong instance's log/config and an empty `Model:` line in simple mode.

**Change:** Select the first instance that actually has a model loaded as the "loaded"
instance; only fall back to `instances[0]` after the loop when none has a model.

**Files:** `internal/launcher/menu.go`, `internal/launcher/menu_test.go`, `CHANGELOG.md`.

**Verify / Acceptance:**
- Test: given a slice of instances where a non-first instance is the only one with a
  non-empty `ActiveModel`, the selection helper returns that instance; with none loaded it
  returns the first. (Extract the selection into a pure, testable helper if it is currently
  inline in the render path.)
- Full `go test ./...` passes.

---

## 12. auto-stop / auto-unload must match the target by address AND backend — ✅ DONE (2026-07-07)

NOTES (2026-07-07): Extracted the skip predicate into a pure helper
`isTargetInstance(inst, targetAddr, targetBackend)` (`server.go`) and pointed
both loops at it, rather than inlining `inst.Addr() == targetAddr &&
inst.Backend == profile.Backend` in each. This makes the behaviour unit-testable
without item 17's fake-backend scaffolding — the inline path runs through
`StopInstance`, which locates the listening PID via `lsof` and would SIGTERM the
httptest server's owning process (the test process). `TestIsTargetInstance`
(`server_test.go`) covers the different-backend-same-address blocker case,
mirroring item 11's "extract into a pure, testable helper" precedent.

**Severity:** Medium. **Authority:** ADR-0004 (auto_unload rule), ADR-0006 (address keying);
the review's `[Correctness]` finding.

**What's wrong:** Both loops in `LoadProfile` (`server.go:349-352` and `:360-363`) `continue`
on any instance whose `Addr() == targetAddr`, without checking backend. If a different backend
occupies the profile's `host:port` (e.g. a leftover `llama-server` on 8080 while activating an
ollama profile bound there), `auto_stop_server: true` stops everything except the actual
blocker, and the subsequent start fails to bind.

**Change:** Change both skip conditions to `inst.Addr() == targetAddr && inst.Backend ==
profile.Backend`. An instance at the target address of a *different* backend is a blocker and
must be stopped/handled, not skipped.

**Files:** `internal/launcher/server.go`, `internal/launcher/server_test.go` (or the
`LoadProfile` tests from item 17), `CHANGELOG.md`.

**Depends on:** none, but coordinate with item 17 (which adds `LoadProfile` tests) — this
item's assertion may live in that test file.

**Verify / Acceptance:**
- Test: with `auto_stop_server: true`, a fake instance of a *different* backend at
  `targetAddr` receives the stop path (is not skipped), while a same-backend same-address
  instance is skipped.
- Full `go test ./...` passes.

---

## 13. Restore the terminal cursor when a popup closes — ✅ DONE (2026-07-07)

**Severity:** Medium. **Authority:** the review's `[Correctness]` finding.

**What's wrong:** `showPopup` (`internal/launcher/ui.go:338, 344-350`) writes `escCursorHide`,
waits for a key, restores termios, but never re-shows the cursor. With the default
`auto_close: true`, opening any popup and pressing a key exits the process leaving the cursor
invisible until `reset`.

**Change:** Emit `escCursorShow` after `readKey()` in `showPopup`, mirroring `selectMenu`'s
cursor restoration.

**Files:** `internal/launcher/ui.go`, `CHANGELOG.md`. (Raw-terminal rendering is not
unit-tested; a test is not required for this item — state that in NOTES. Verify by build +
manual check that `escCursorShow` is emitted on the popup-exit path.)

**Verify / Acceptance:**
- `go build ./...` passes; the popup-exit path provably writes `escCursorShow` (code
  inspection is acceptable here since the TUI loop is not unit-tested — note this in the
  commit).
- Full `go test ./...` passes.

---

## 14. `cmdList` must use display width for star-column alignment

**Severity:** Medium. **Authority:** the review's `[Intent/Structure]` finding.

**What's wrong:** `cmdList` (`internal/launcher/cli.go:441-483`) measures column widths with
byte `len()` while both menu builders use `visibleWidth`. Any profile description or title
with a multi-byte character (the TDD's own example uses an em dash) misaligns the `★` column
in `list` output.

**Change:** Replace the byte-`len()` width measurements in `cmdList` with `visibleWidth`
(the same helper the menu uses). Scope this item to the alignment bug only — do **not**
consolidate the three row builders (that is the out-of-scope `/improve-codebase-architecture`
follow-up).

**Files:** `internal/launcher/cli.go`, `internal/launcher/cli_test.go` (or `menu_test.go` if
the width helper is exercised there), `CHANGELOG.md`.

**Verify / Acceptance:**
- Test: a profile list containing a multi-byte character produces a `★` column aligned to the
  same visible position as the menu builder for the same input.
- Full `go test ./...` passes.

---

## 15. Remove dead code, the write-only field, and its per-discovery probe

**Severity:** Medium. **Authority:** the review's `[Intent/Structure]` dead-code and
write-only-field findings (deletion test).

**What's wrong:**
- `RunningInstance.ResolvedParams` is assigned at `discovery.go:135`, `server.go:532`,
  `server.go:569` and read nowhere; populating it makes `probeInstance` issue an extra
  `QueryLiveParams` (`GET /props`) per llamacpp instance on **every** discovery pass,
  including each menu refresh tick, for a value nobody consumes.
- `IsServerAlive` (`server.go:32`) has zero callers. `FormatMemoryLine` (`sysmem.go:260`)
  and `percentString` (`sysmem.go:267`) are called only from their own tests.

**Change:** Delete `RunningInstance.ResolvedParams` and its three assignments, and remove the
now-unnecessary `QueryLiveParams` call from the discovery/probe path (drift detection in
`LoadProfile` keeps its own `QueryLiveParams`). Delete `IsServerAlive`, `FormatMemoryLine`,
`percentString`, and their now-dead tests. Keep `percentValue` (used by the template engine).
Remove the TDD §5.2/§7.1 mentions of the deleted symbols and the `ResolvedParams` field.

**Files:** `internal/launcher/discovery.go`, `internal/launcher/server.go`,
`internal/launcher/sysmem.go`, `internal/launcher/sysmem_test.go`,
`internal/launcher/discovery_test.go` (if it references `ResolvedParams`),
`llama-launcher.TDD.md`, `CHANGELOG.md`.

**Depends on:** item 2 (both touch the drift/`QueryLiveParams` area; land the drift fix
first so this deletion doesn't disturb it).

**Verify / Acceptance:**
- `go build ./... && go vet ./...` clean with no unused-symbol or unused-import errors.
- `grep -rn "ResolvedParams\|IsServerAlive\|FormatMemoryLine\|percentString" --include=*.go`
  returns nothing outside comments/CHANGELOG.
- Discovery still returns instances with `ActiveModel` populated (existing discovery tests
  pass); full `go test ./...` passes.

---

## 16. Fix stale documentation that contradicts the live-derivation design

**Severity:** Medium. **Authority:** invariant "no persisted state / everything derived live";
the review's `[Intent/Structure]` stale-docs finding.

**What's wrong:**
- `CONTEXT.md:32` claims "The persisted state-file schema retains `backend` as a JSON field
  name on `ServerState`" — no `ServerState` type or state file exists in the codebase.
- `llama-launcher.TDD.md:833` (§14) says "Follow `skills/coding-standards/SKILL.md`", but the
  repo's `skills/` dir contains only `manage-llm-server`.

**Change:** Rewrite the CONTEXT.md "flagged ambiguities" sentence to reflect that there is no
persisted state and instance identity is derived live from `host:port` (align with ADR-0006 /
ADR-0010-none — no ADR needed, this is a doc correction). Point TDD §14 at an existing
location for the coding standard (either vendor a copy under the repo or reference the
`~/.claude/skills/...` path explicitly as an external personal skill), so a fresh contributor
is not sent to a non-existent path.

**Files:** `CONTEXT.md`, `llama-launcher.TDD.md`, `CHANGELOG.md`. Docs-only — no code change.

**Verify / Acceptance:**
- `grep -rn "ServerState" CONTEXT.md` returns nothing; the TDD §14 path resolves to a real
  location.
- `go build ./... && go test ./...` still passes (no code touched).

---

## 17. Add tests for the lifecycle orchestration and stop path

**Severity:** Medium (critical paths untested). **Authority:** ADR-0001/0004/0007; TDD §12.

**What's wrong:** `LoadProfile` (`server.go:319`) — idempotency no-op, drift notice,
`auto_stop_server`/`auto_unload` — has no test; the stop lifecycle
(`StopInstance`/`EnsureStopped`, `server.go:145-245`) and the crash-on-start / health-timeout
error paths (`server.go:97-101, 296-307, 575-584`) are untested. They are covered today only
via leaf helpers, so an inverted gate could restart a live server on every `load` with the
suite green.

**Change:** Add httptest-backed tests (register an in-package fake `ManagedLLMServer` /
`LLMServer` so no real process is signalled) asserting:
- **Idempotency no-op:** healthy target serving the profile's model on `/v1/models`, matching
  `/props` → `LoadProfile(cfg, profile, false, nil)` returns `started==false` and issues no
  stop/load traffic (record requests on the fake).
- **Drift:** a drifted shared `/props` field prints the drift notice naming that field
  (capture stderr) and still no-ops without `--restart`.
- **auto_stop_server:** a second fake instance at a different address receives the stop path
  while the target address is left alone.
- **Stop lifecycle:** `StopInstance` on an unreachable address returns `ErrNotRunning`; an
  invalid address returns an error; `EnsureStopped` returns nil when the fake's `TryStop`
  makes the health check fail.
- **Start failure:** a fake `llama-server` on `PATH` (via `t.Setenv`) that prints two lines
  and exits non-zero yields an error containing "exited immediately" plus the script output;
  `WaitForHealth` against a never-healthy fake returns a timeout error naming the address.

Keep `terminatePID`'s real SIGTERM→SIGKILL escalation out of scope for the happy-path tests
(it runs against real PIDs and the fixed 15 s timeout makes it slow) unless a timeout can be
injected cleanly.

**Files:** new `internal/launcher/server_load_test.go` (or extend `server_test.go`),
possibly a small test-only fake backend in the package.

**Depends on:** items 2, 12 (their behaviour is what these tests assert — land them first so
the assertions match the fixed behaviour).

**Verify / Acceptance:**
- The new tests fail against the pre-fix behaviour of items 2/12 and pass after; run
  `go test ./internal/launcher/ -run 'LoadProfile|Stop|EnsureStopped|WaitForHealth'`.
- Full `go test ./...` passes.

---

## 18. Add CLI exit-code and MCP allowlist adversarial tests

**Severity:** Medium (critical contract + sole auth layer untested). **Authority:** TDD §3.3
exit codes; ADR-0008 allowlist; the MCP result mapping depends on the real CLI's exit codes.

**What's wrong:** There is no `cli_test.go`: `Run`, `cmdLoad`, `cmdStop`, `cmdStatus`/JSON,
and the 0/1/2/3 exit-code contract are untested, yet the MCP adapter's result mapping assumes
`status --json` exits 1 while printing JSON (pinned only against a *fake* CLI). The allowlist
middleware tests (`cmd/llama-launcher-mcp/allowlist_test.go`) omit IPv6 forms and forwarded
headers.

**Change:**
- Add `internal/launcher/cli_test.go` driving `Run([]string{...})` with a temp config:
  `status --json` with nothing running → exit 1 and stdout parses as a JSON array (one
  `running:false` entry per enabled backend, per item 9); `load` with no profile → 2; unknown
  command → 2; unknown flag on `load` → 2; `stop`/`unload` with nothing running → 1.
- Extend `allowlist_test.go`'s middleware table with `"[::1]:port"`, an IPv4-mapped-IPv6
  `"[::ffff:192.168.64.2]:port"` against a `192.168.64.2` allow entry, and a denied
  `RemoteAddr` carrying `X-Forwarded-For: <allowed-ip>` asserting the header is ignored (403).

**Files:** new `internal/launcher/cli_test.go`, `cmd/llama-launcher-mcp/allowlist_test.go`.

**Depends on:** item 9 (the `status --json` shape assertion must match the per-instance output).

**Verify / Acceptance:**
- `go test ./internal/launcher/ -run TestRun` and
  `go test ./cmd/llama-launcher-mcp/ -run Allowlist` pass, exercising the new cases.
- Full `go test ./...` passes; `go vet ./...` clean.
