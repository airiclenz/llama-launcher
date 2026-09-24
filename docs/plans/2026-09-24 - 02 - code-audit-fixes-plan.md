# Plan: Fix the 2026-09-24 code-audit findings

**Goal:** Every finding of `docs/reviews/code-audit-2026-09-24.md` is fixed or settled as its ratified call says: shell and keystore seams, MCP adapter contract, unconditional stop, log and config-dir data loss, hostile-server bounds, facade contract, diverged config validators.
**Date:** 2026-09-24
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** b37981a

**Sources:** `docs/reviews/code-audit-2026-09-24.md` (each item cites its finding by title); `docs/adr/0001-stop-is-unconditional.md`, `0006-instances-are-keyed-by-address.md`, `0010-starting-instances-are-visible-and-stoppable.md`, `0011-public-library-facade.md`, `0012-the-library-compiles-everywhere-and-actuates-where-it-can.md`, `0015-a-loading-splash-is-found-by-its-process.md`; `llama-launcher.TDD.md`

**Ratified design calls** (user, 2026-09-24):
- **api_key_cmd:** keep `sh -c`; run it only when the config file is owned by the current uid and not group/world-writable, else refuse naming the file. New ADR records the config-is-trusted stance.
- **Edit config:** macOS keeps `open`; elsewhere `$VISUAL`, then `$EDITOR`, in the terminal; neither set → the verb is not offered.
- **Process table:** keep `ps` for pid/pgid; read the true argv of session-leader candidates (darwin `kern.procargs2`, linux `/proc/<pid>/cmdline`).
- **Auth-refused server:** visible (new `AuthFailed` flag, shown as auth failed) and stoppable; load/unload refuse with the `authFailedErr` message.
- **MCP stderr:** stdout verbatim as the first content item; non-empty stderr as a second text item; exit ≥ 2 unchanged.
- **Cancel with Stop:** the managed health wait returns a new exported `ErrLoadCanceled` promptly when the spawned server is stopped mid-load; docs keep "cancel with Stop".
- **Config alias:** doc carve-out only — the `Reload` exception stated in the headline invariant's sentence.
- **--config:** YAML parse errors report file and line and the type mismatch, never value fragments (one code path, so every config path).
- **Keystore / legacy cleanup / log names / MCP cap / hostile bounds:** as the item texts bind (writer, 2026-09-24).
- **Keychain charset:** refuse `"`, `\` and control characters; everything else is written quoted — base64 keys and spaced names keep migrating (user, 2026-09-24).
- **Auto-stop sweep:** skips auth-refusing occupants; only explicit stop signals them (user, 2026-09-24).
- **ADR-0010:** superseded for 401/403 responders at a configured address — explicit stop signals the listener (user, 2026-09-24).
- **Auth-failed label:** one row per address, no backend name, suppressed when another backend identifies the address (user, 2026-09-24).

**Standing requirements:**
- skills: coding-standards. `make cross` (darwin/linux/windows build+vet) stays green; platform files follow the `process_unix.go` / `process_windows.go` split.
- Each item updates the TDD/README lines its own change invalidates (grep for the mechanism it changes); its CHANGELOG entry travels in its sidecar.
- Follow-ups become beads with spoken ids (`bd create --id llama-launcher-<slug>`), never a NOTES line alone.

**Out of scope:** a no-shell argv resolver for `api_key_cmd` (rejected, see ratified calls); wrapping the facade `Config` instead of aliasing it; `llama-launcher-keystore-quote-name-test` (ReadCmd's `shellWord`, not the `security -i` write); `llama-launcher-identify-backend-serial-checks`, `llama-launcher-ipv6-host-address-format`.

**Regression check (2026-09-24, b37981a):**
- 1: guard folded (chmod-exact tests, `os.Stat` of `c.ConfigPath`, `config validate` gap stated)
- 2: recast (keychain charset decision); re-check: guard folded (new refusal keychain-only, linux round-trip test, `securityWord` comment superseded)
- 4: guard folded; yields to ADR-0010 ("An explicit stop stops it") — `stop_server` bypasses the cap
- 5: guard folded (`StartingUp` stays bool; Ollama joins)
- 6: recast (auth-failed decisions); supersedes ADR-0010's identification-over-signal line for 401/403; re-check: guard folded (unload refuses before the managed/external split, `Backend ""` stop wording, `primaryInstance`/cmdLogs skip, load hook bound to discovery, grep-rule doc sweep incl. TDD §6.5 and ADR-0010's neither-pass consequence)
- 7: guard folded (+ owns CLI unload refusal); re-check: recast (`status --json` emits AuthFailed rows, one shared listing helper, cmdLogs omits them)
- 8: guard folded (untagged parsers, true-argv merge test, doc lines)
- 9: guard folded (test names, `captureStderr`)
- 10: guard folded (ollama producer, `TestFindManagedLogFile`); revises TDD log-name format and the "revisit only if collisions become real" line
- 11: guard folded (pid ≥ 0, platform files, dir-taking helper, `state.json` checked)
- 12: guard folded (`processIdentity` name, untagged stat parser, spaced children, real-child mismatch test)
- 13: guard folded (key-migration prompt dropped, every call site, builders extracted)
- 14: guard folded (stub `ollama` on PATH)
- 15: guard folded (PID/log lines omitted when absent, external guidance); closes the gap ADR-0012 and TDD's "Does not hold — auto-starting an external server" record
- 16: guard folded (status-0 is canceled, no PID fallback, serialization carve-out); amends ADR-0011's serialization line
- 17: guard folded (check in `unloadServerModel`, positive mismatch only, facade leg via lmstudio)
- 18: guard folded (stamped timeout, ioreg zeros, package-var timeout)
- 19: recast (float half rejected; model id only); re-check: guard folded (bound only the stored copy; `liveLoadedModel` stays unbounded)
- 20: guard folded (`defaults:` port, ≤ 10-char bite value, whole-message redaction, configwrite paths)
- 21: guard folded (every doc sentence, keep the LoadConfig re-read line, stronger Acceptance)
- 22: guard folded (errors and warnings separate, per-surface wording); item 1's trust gate stays out of it

## 1. api_key_cmd runs only from a config the user owns — ✅ DONE (2026-09-24)

NOTES (2026-09-24): the gate sits in resolveKeyCommands before the first runKeyCommand, and only when an enabled entry carries api_key_cmd. It judges c.ConfigPath (the tilde-expanded path parseConfig read). A not-owned file's refusal also says "make the file yours", because `chmod 600` alone cannot fix ownership.
NOTES (2026-09-24): the not-owned branch has no test, because a test cannot chown a file to another uid without root. Only the mode branch and the symlink case are covered.

**What:** Fixes audit finding "`api_key_cmd` executed whole via `sh -c` from the config file" under the ratified trust gate.
**Regression guard.** `configTrusted` judges `os.Stat` of `c.ConfigPath` (the file `parseConfig` read; a symlinked config is judged by its target, never `os.Lstat`); the platform files carry `//go:build unix` / `//go:build windows` (`_unix` is no GOOS suffix). Tests `os.Chmod` each file to its exact mode after writing (`os.WriteFile` is umask-filtered: 0620 → 0600) and call `requirePOSIXShell`. `config validate` runs no command and does not report the gate (item 22); the TDD/README lines state that gap.
**Goal:** On unix, `LoadConfig` of a file not owned by the current uid, or whose mode has any of `0o022`, returns an error naming the path and the fix (`chmod 600 <path>`) whenever an enabled entry carries `api_key_cmd`, and runs no command. An owned `0600`/`0644` file behaves as before; Windows is unchanged.
**Approach (assumed at the header base):** gate inside `resolveKeyCommands` (config.go) before any `runKeyCommand`, through `configTrusted(path) error` in new `config_trust_unix.go` / `config_trust_windows.go` (windows returns nil). `keyCommandArgv`'s doc comment: the 0600 premise is now enforced. New ADR-0016 (context: argv splitting would not stop a hostile config).
**Files:** internal/launcher/config.go, internal/launcher/config_trust_unix.go, internal/launcher/config_trust_windows.go, internal/launcher/config_test.go, docs/adr/0016-api-key-cmd-runs-only-from-an-owned-config.md, llama-launcher.TDD.md, README.md
**Read first:** internal/launcher/config.go — resolveKeyCommands, LoadConfigNotify, parseConfig, keyCommandArgv; internal/launcher/config_test.go — keySourceConfig, requirePOSIXShell;
internal/launcher/cli.go — cmdConfigValidate; internal/launcher/process_unix.go — build-tag split
**Tests:** `TestLoadConfig_KeyCommandTrustGate` (table, `requirePOSIXShell`, modes set by `os.Chmod`): `0600` runs the command; `0620` and `0602` refuse without running it (the command touches a marker file that must not exist); a file without `api_key_cmd` loads at `0666`; a symlink to an owned `0600` config runs the command.
**Acceptance:**
- `go build ./... && GOOS=windows go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestLoadConfig|TestResolveKeyCommands|TestKeyCommand' -count=1`
**Commit:** `fix(config): run api_key_cmd only from a config the user owns`

## 2. Keychain write refuses values `security -i` could reparse — ✅ DONE (2026-09-24)

NOTES (2026-09-24): consequential edit — internal/keystore/run.go: made necessary by redactKey dropping its escaped-spelling pass. trimCappedKeyTail's comment said it checked both spellings "for the reason redactKey checks both". The comment is rewritten. The code still checks the quoted spelling, so the opening quote is trimmed together with a cut fragment.
NOTES (2026-09-24): redactKey now replaces only the bare key. securityWord no longer escapes and Write refuses `"` and `\` on the keychain, so the quoted spelling always contains the key literally. The second replacement could never match, so it was removed. Its doc comment is rewritten.
NOTES (2026-09-24): Write's error text is the message the user sees, because migrateKey returns Write's error unchanged. keymigrate.go needed no edit.

**What:** Recast at the regression check (2026-09-24). Fixes audit finding "Keychain write pipes a shell-parsed command line into `security -i`".
**Regression guard.** (user decision) A keychain key or entry containing `"`, `\` or any control character (newline included) is refused; every other value is written inside double quotes, so base64 keys (`=`, `+`, `/`) and spaced entries like "work laptop" keep migrating. The new refusal applies only when `s.kind == kindKeychain` — the existing `\r\n` refusal stays for both stores, and a secret-tool key holding `"`, `\` or a tab still migrates (secret on stdin; ReadCmd single-quotes via `shellWord`). `writeCommand` keeps its signature (`padToCutInsideTheKey` untouched). `securityWord`'s doc comment ("an ordinary word is left bare…") is superseded and rewritten.
**Goal:** The darwin keychain write refuses such a value before any `security` process starts; the migration reports it and keeps the plaintext key.
**Approach (assumed at the header base):** a `Store.Write` (keystore.go) case beside the existing refusals, before `writeCommand`; `securityWord` quotes every value. The migration already surfaces write errors — verify its message names the refusal.
**Files:** internal/keystore/keystore.go, internal/keystore/keystore_test.go, llama-launcher.TDD.md
**Read first:** internal/keystore/keystore.go — Store.Write, writeCommand, securityWord, redactKey; internal/keystore/keystore_test.go — TestWriteRefusesWhatItCannotStore,
TestWriteRedactsTheSecretFromWhatTheStoreSaid, padToCutInsideTheKey; internal/launcher/keymigrate.go — migrateKey
**Tests:** in `TestWriteRefusesWhatItCannotStore`: a key `x" ; touch /tmp/pwn ; "`, a key with `\` and an entry with a control character are refused with no runner invocation (fake runner records calls); a base64 key and the entry "work laptop" still write. The darwin quoted-key cases (`TestWriteRedactsTheSecretFromWhatTheStoreSaid`, the `padToCutInsideTheKey` table) become refusal cases, linux twins unchanged. `TestReadCmdReadsBackWhatWriteStored` gains a linux case (a key with `"` and `\` writes and reads back); its "work laptop" cases stay green.
**Acceptance:**
- `go build ./... && go vet ./internal/keystore/`
- `go test ./internal/keystore/ -count=1`
**Commit:** `fix(keystore): refuse keychain values security -i could reparse`

## 3. MCP tool results keep stdout as the sole first content item — ✅ DONE (2026-09-24)

**What:** Fixes audit finding "The MCP adapter fuses stderr into its JSON tool results".
**Goal:** For exit 0 or 1, `Content[0]` is the CLI's stdout verbatim (plus only the truncation notice when the cap hit) and non-empty stderr follows as `Content[1]`; empty stdout → the stderr item alone; both empty → the single item `(no output)`. `list_profiles` output with a plaintext-key warning parses as JSON from `Content[0]`. Exit ≥ 2 is unchanged.
**Approach (assumed at the header base):** in `run` (cmd/llama-launcher-mcp/config.go) replace `text += errOut` with a second `mcp.TextContent`.
**Files:** cmd/llama-launcher-mcp/config.go, cmd/llama-launcher-mcp/config_test.go, llama-launcher.TDD.md
**Read first:** cmd/llama-launcher-mcp/config.go — config.run, limitedWriter.text; cmd/llama-launcher-mcp/config_test.go — fakeCLI, TestRunExitOneWithStderrOnlyIsNotError, TestRunCapsOversizedOutput;
cmd/llama-launcher-mcp/integration_test.go — callText; internal/launcher/cli.go — plaintextKeyNotice warning on stderr; llama-launcher.TDD.md — §15 "Result mapping" paragraph
**Tests:** fake CLI printing JSON on stdout and `warning: …` on stderr, exit 0 → `Content[0]` unmarshals, `Content[1]` is the warning; exit 1 with stderr only → exactly one item, the stderr text; both empty → `(no output)`.
**Acceptance:**
- `go build ./... && go vet ./cmd/llama-launcher-mcp/`
- `go test ./cmd/llama-launcher-mcp/ -count=1`
**Commit:** `fix(mcp): keep stdout as the sole first tool-result item`

## 4. MCP adapter bounds in-flight subprocesses — ✅ DONE (2026-09-24)

NOTES (2026-09-24): the stop_server bypass is a sibling method `config.runUnbounded` (the exec body, which `run` wraps with the slot acquire/release) rather than a flag on `run`; the stop_server test drives the real handler through `startAdapter` while 4 `config.run` calls hold the slots.

**What:** Fixes audit finding "No bound on in-flight MCP tool subprocesses".
**Regression guard.** `stop_server` never waits on the cap — it runs outside the 4 slots, so held `load_profile` calls cannot delay the stop that cancels a load; the item yields to ADR-0010 ("An explicit stop stops it", docs/adr/0010-starting-instances-are-visible-and-stoppable.md). The semaphore is a field on `config` acquired in `config.run` (the one exec site; handlers have no runner seam); nil means unbounded, since tests build `&config{}` literals.
**Goal:** At most 4 `llama-launcher` subprocesses run at once across all tool calls; a call waits for a slot and returns an `IsError` result ("canceled while waiting for a free slot") if its request context ends first.
**Approach (assumed at the header base):** a buffered-channel semaphore (constant `maxInFlight = 4`, no flag) created in `newServer` (main.go), released when `run` returns.
**Files:** cmd/llama-launcher-mcp/main.go, cmd/llama-launcher-mcp/config.go, cmd/llama-launcher-mcp/config_test.go, llama-launcher.TDD.md
**Read first:** cmd/llama-launcher-mcp/main.go — newServer, stop_server/load_profile handlers; cmd/llama-launcher-mcp/config.go — config, config.run; cmd/llama-launcher-mcp/validate.go — toolError;
cmd/llama-launcher-mcp/integration_test.go — startAdapter, callText; cmd/llama-launcher-mcp/config_test.go — fakeCLI
**Tests:** through `config.run` with a blocking fake CLI: 6 concurrent runs → never more than 4 inside; an already-canceled context while 4 are held returns `IsError` without running; with 4 held, `stop_server` still runs; a `&config{}` with no semaphore runs unbounded.
**Acceptance:**
- `go build ./... && go vet ./cmd/llama-launcher-mcp/`
- `go test ./cmd/llama-launcher-mcp/ -race -count=1`
**Commit:** `fix(mcp): bound in-flight tool subprocesses`

## 5. llama.cpp health checks report auth failures like its siblings — ✅ DONE (2026-09-24)

NOTES (2026-09-24): consequential edit — llama-launcher.TDD.md: made necessary by the new `ErrAuthFailed` sentinel and the llamacpp/Ollama HealthCheck auth mapping (backend_http.go row and the llamacpp/ollama test rows)

**What:** Fixes the llamacpp message gap of audit finding "A server that refuses auth becomes invisible…".
**Regression guard.** `StartingUp` keeps its `bool` signature (`StartupProber`, backend.go) and stays false on 401/403, as today; only `HealthCheck` returns the error. `Ollama.HealthCheck` (today `unhealthy: status 401`) routes through `authFailedErr` too.
**Goal:** `LlamaCpp.HealthCheck` returns the `authFailedErr` text on 401/403, and every backend's 401/403 health error matches `errors.Is(err, ErrAuthFailed)`.
**Approach (assumed at the header base):** an exported sentinel `ErrAuthFailed` in backend_http.go that `authFailedErr` wraps (text kept); the llamacpp probes call it as the siblings do.
**Files:** internal/launcher/backend_http.go, internal/launcher/backend_llamacpp.go, internal/launcher/backend_ollama.go, internal/launcher/backend_llamacpp_test.go, internal/launcher/backend_ollama_test.go, internal/launcher/backend_http_test.go
**Read first:** internal/launcher/backend_llamacpp.go — LlamaCpp.HealthCheck, LlamaCpp.StartingUp; internal/launcher/backend_http.go — authFailedErr, expectOK; internal/launcher/backend_ollama.go — Ollama.HealthCheck;
internal/launcher/backend.go — StartupProber; internal/launcher/backend_http_test.go — TestAuthFailedErr; internal/launcher/backend_llamacpp_test.go — TestLlamaCppAuthFailure
**Tests:** httptest 401 and 403 against `LlamaCpp.HealthCheck` → `errors.Is(err, ErrAuthFailed)`, text contains `check api_key`, `StartingUp` false; a `TestOllama…` 401 case against `Ollama.HealthCheck` with the same assertion.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestLlamaCpp|TestAuthFailed|TestOllama|TestLMStudio' -count=1`
**Commit:** `fix(launcher): map llama.cpp 401/403 through authFailedErr`

## 6. An auth-refusing server is discovered and stoppable — ✅ DONE (2026-09-24)

NOTES (2026-09-24): consequential edit — README.md: made necessary by the auth-refusing server becoming stoppable and refused by load/unload (one user-facing paragraph beside the Starting-server one)
NOTES (2026-09-24): the load hook is a new activationOps method `authRefusal` (realOps → `authRefusalAt`, built on discovery's own target and probe code, so it applies discovery's every-backend rule) rather than an `ops.discover` lookup, so the refusal carries the real authFailedErr status text; `identify` (realOps → `identifyBackend`) is the identity method item 17 can share
NOTES (2026-09-24): `identifyBackend`'s third pass returns the probing backend's name together with an error wrapping ErrAuthFailed; `stopServerAt` gained a `nativeHook bool` parameter (false = listening PID only, no loading-process lookup, no TryStop), and the four existing test call sites pass true
NOTES (2026-09-24): the cmdStop leg uses a re-executed child of the test binary serving 401 (`TestAuthRefusingHelperProcess`) instead of an nc child: `identifyBackend` probes every registered backend over HTTP, which a one-shot nc listener cannot answer. The `stopServerAt` legs use the stub and nc child as the item describes
NOTES (2026-09-24): caveat: following the item's text, an AuthFailed row needs every backend probing the address to answer 401/403. On a shared port, a server that 401s one backend's probe path but answers another's with some other status is still dropped from discovery (idea: relax the rule to "any 401/403 and nothing healthy or Starting")
NOTES (2026-09-24): a bare `logs` with only an AuthFailed row running prints "No server running." (the row is omitted, per the item); the "  at <addr>" labels in resolveTargetInstance/cmdUnload listings and the stop picker rows are left to item 7

**What:** Recast at the regression check (2026-09-24). Fixes audit finding "A server that refuses auth becomes invisible, so the unconditional `stop` refuses" (ADR-0001). Depends on item 5.
**Regression guard.** (user decisions) An AuthFailed row has Backend "", no model or profile, at most one per address, dropped when any backend identifies the address as healthy or Starting; `instancesSignature` includes it; the facade `RunningInstance` doc (launcher/launcher.go) names it. `identifyBackend` accepts ErrAuthFailed only in a third pass after the health and StartingUp passes; `StopInstance` returns Backend "" for it (the name `stopServerAt`'s `GetLLMServer` needs stays internal) and cmdStop / the menu stop print `Stopped server at <addr> (PID n)` — item 6 owns that wording. Stop signals the listening PID only (no native hook); `stopServerAt` counts `errors.Is(healthErr, ErrAuthFailed)` as still reachable. `loadProfile` refuses with the authFailedErr message through a named hook (an `activationOps` method or an `ops.discover` lookup; fakeOps implements it) only when every probing backend answers 401/403 and none reports healthy or Starting — never on the profile backend's own health error (a healthy Splash answering 403 to Host 0.0.0.0 is still auto-stopped). Unload runs no discovery: `unloadServerModel` refuses before the managed/external split through an `activationOps` identity method shared with item 17, and `UnloadInstanceModel` errors on a third-pass identification (today a 401 yields "" from `liveLoadedModel` and success). The `auto_stop_server` sweep skips AuthFailed rows; they sort first, so `primaryInstance` (menu.go) skips them unless one is alone and cmdLogs' no-target pick omits them; CLI unload's refusal is item 7's. Tests: httptest only for the discovery/identify legs; unload's managed leg and `loadProfile` on fakeOps (a real stop SIGTERMs the test's process group — see `stopRecordingOps`); the stop leg via registry stub + nc child as `TestStopServerAt_StartingOccupant`. Amend every sentence stating two-pass identification or that an unattributed listener is never signalled (`grep -rn -i 'two passes\|neither pass\|cannot attribute\|silently omitted' llama-launcher.TDD.md docs/adr internal/launcher/*.go`) — TDD §6.5, the code comments, ADR-0001, and docs/adr/0010-starting-instances-are-visible-and-stoppable.md, whose "Identification over signal-whatever-listens" line and neither-pass `ErrNotRunning` consequence are superseded for a 401/403 on an LLM API path at a configured address.
**Goal:** Such an address is one `RunningInstance{AuthFailed: true}`; explicit `stop` signals its listener; `load` and facade `LoadProfile`/`Unload` against it return the `authFailedErr` message; the auto-stop sweep leaves it alone.
**Approach (assumed at the header base):** `probeInstance`/`DiscoverRunningInstances` (discovery.go) record `errors.Is(err, ErrAuthFailed)` after the `startingUp` fallback and collapse per address; `identifyBackend` (server.go) gains the third pass; hooks as bound.
**Files:** internal/launcher/discovery.go, internal/launcher/server.go, internal/launcher/menu.go, internal/launcher/cli.go, internal/launcher/discovery_test.go, internal/launcher/server_test.go, internal/launcher/menu_test.go, internal/launcher/cli_test.go, launcher/launcher.go, docs/adr/0001-stop-is-unconditional.md, docs/adr/0010-starting-instances-are-visible-and-stoppable.md, llama-launcher.TDD.md
**Read first:** internal/launcher/server.go — identifyBackend, StopInstance, stopServerAt, unloadServerModel, loadProfile; internal/launcher/discovery.go — DiscoverRunningInstances;
internal/launcher/server_test.go — stopRecordingOps; internal/launcher/menu.go — primaryInstance
**Tests:** httptest 401: discovery reports one `AuthFailed` row, Backend ""; with another backend healthy (or Starting) at the address, no row; `identifyBackend` accepts it only after both passes. Facade/internal unload → `ErrAuthFailed` error (managed leg on fakeOps; `UnloadInstanceModel` on a third-pass identification errors). `loadProfile` (fakeOps hook) refuses with the `authFailedErr` message; `TestLoadProfile_Orchestration_AutoStop` gains a case where a healthy foreign occupant at the target is still stopped; the sweep skips an AuthFailed row. Stop leg (stub HealthCheck → `ErrAuthFailed`, nc child): listener signalled, no native hook, a surviving listener not reported stopped, Backend "", cmdStop prints `Stopped server at <addr>`. `TestPrimaryInstance` and the cmdLogs no-target pick: Starting llamacpp + AuthFailed row → llamacpp. `TestInstancesSignature` gains an `AuthFailed` case.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/ ./launcher/`
- `go test ./internal/launcher/ -run 'TestDiscover|TestIdentifyBackend|TestStop|TestUnload|TestLoadProfile|TestInstancesSignature|TestPrimaryInstance|TestCmdStop|TestCmdLogs' -count=1`
**Commit:** `fix(launcher): discover and stop servers that refuse auth`

## 7. Status and menu show an auth-failing server — ✅ DONE (2026-09-24)

NOTES (2026-09-24): the auth text is kept out of the status state column (statusStateLabel and its 9-wide column are unchanged): an AuthFailed status row is `  ● <label>` on its own, with no backend, state or model columns
NOTES (2026-09-24): an AuthFailed row counts as discovered for the exit code of `status --json` (0), the same as the text path, which already returned 0 for it; its JSON object follows the per-backend entries so the existing entries keep their positions
NOTES (2026-09-24): the shared helpers are `authFailedLabel` (the label) and `instanceAtLabel` ("<backend> at <addr>" or the label) in menu.go. The unload refusal re-probes via `authRefusalAt`, which applies discovery's rule. If the server has stopped refusing since discovery, unload falls back to its "No model loaded…" answer
NOTES (2026-09-24): cmdLogs given an explicit host:port target that is an AuthFailed row prints "No launcher-managed log is known for that server: <label>." (exit 1), not the blank backend name; runIdleMenuSimple (an AuthFailed row alone) prints the label as its `Status:` line with no `Server:` line
NOTES (2026-09-24): skills/manage-llm-server/SKILL.md's status key list (line 24) already omitted `starting`, so it was left alone

**What:** Recast at the regression check (2026-09-24). Display half of the ratified auth-refused call. Depends on item 6.
**Regression guard.** (user decision) `status --json` (cmdStatusJSON) emits one object per AuthFailed address with `"backend": ""` and `"auth_failed": true` (additive field; existing objects unchanged) — it must not drop Backend-empty rows. Every instance listing that renders a backend name (grep `backendDisplayName(` in cli.go and menu.go — resolveTargetInstance, cmdLogs, cmdUnload, the status table, the menu header) renders an AuthFailed row through one shared helper as `auth failed at <addr> — check api_key in the servers section`, never as a bare "  at <addr>"; cmdLogs omits AuthFailed rows (no log file is known). The guard states this rule and the grep, not a closed list. Add a test per changed renderer (status text, status --json, unload listing). Also bound: the AuthFailed JSON object has `running: false`; `TestCmdStatusJSON_ListsEveryInstanceOfABackend`'s documented-key list and the MCP `server_status` Description gain `auth_failed`; `statusStateLabel` pads to `columnWidth` = 9 and an 11-wide label panics — widen it (and its comment) or keep the auth text out of the state column. CLI unload of an AuthFailed instance (cmdUnload's ActiveModel/Starting filter) refuses with the authFailedErr message, not "No model loaded".
**Goal:** `status`, `status --json`, the menu header and every instance listing render an `AuthFailed` instance as the guard binds, never as a model, as "not running" or as a bare address.
**Approach (assumed at the header base):** every renderer of `RunningInstance.Starting` (grep `\.Starting` in cli.go, menu.go, ui.go) and every `backendDisplayName(` listing gains the `AuthFailed` branch through the shared helper.
**Files:** internal/launcher/cli.go, internal/launcher/menu.go, internal/launcher/cli_test.go, internal/launcher/menu_test.go, cmd/llama-launcher-mcp/main.go, README.md, llama-launcher.TDD.md
**Read first:** internal/launcher/cli.go — cmdStatus, statusStateLabel, cmdStatusJSON, cmdUnload; internal/launcher/menu.go — serverStatusLines, stopTargetItems;
cmd/llama-launcher-mcp/main.go — status tool Description; internal/launcher/cli_test.go — TestCmdStatusJSON_ReportsStartingInstance
**Tests:** status text for an `AuthFailed` instance contains `auth failed at <addr> — check api_key in the servers section`, no backend name, no panic; `status --json` carries its object with `"backend": ""`, `auth_failed: true`, `running: false`, other objects unchanged; the cmdUnload listing and the menu header render the same label; CLI `unload` of it returns the `authFailedErr` message, not "No model loaded".
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/ ./cmd/llama-launcher-mcp/`
- `go test ./internal/launcher/ -run 'TestCmdStatus|TestServerStatusLines|TestStopTargetItems|TestCmdUnload|TestUnloadTargetLabel' -count=1`
**Commit:** `feat(launcher): show servers that refuse auth in status and menu`

## 8. A loading Splash is matched by its true argv — ✅ DONE (2026-09-24)

NOTES (2026-09-24): the ps-to-argv merge `withTrueArgv` runs inside `listProcesses` (process_unix.go); `processTable` stays `= listProcesses`, and `parseProcessTable` still returns the whitespace-split Args, which are now only the fallback. backend_splash_test.go was not changed: `TestWithTrueArgv` sits in proc_argv_test.go and reaches `splashLoadingPID` from there.
NOTES (2026-09-24): added `TestProcArgvReadsOwnArgv` (checks `procArgv(os.Getpid())` against `os.Args` on darwin/linux, skipped elsewhere), which is the only check of the real `kern.procargs2` layout. `proc_argv_other.go` is tagged `!darwin && !linux` so the untagged test compiles on windows. The linux reader was only compile-checked here (`GOOS=linux go test -c`) and runs on the ubuntu CI runner.
NOTES (2026-09-24): docs: TDD process_unix.go row, new proc_argv rows, the LoadingProcessFinder paragraph and the §16.6 platform-file list. ADR-0015's "Unix only" bullet, whitespace consequence (marked as amended) and discovery-cost consequence were also updated.

**What:** Fixes audit finding "Process-table parser splits on whitespace and hides a loading Splash".
**Regression guard.** The pure parsers (`parseProcArgs2`, `parseProcCmdline`) live in untagged proc_argv.go so both CI runners (macos-latest, ubuntu-latest) test them; only the syscall/`/proc` readers are build-tagged. The ps-to-argv merge is a pure helper (e.g. `withTrueArgv(entries, argvFn)`); its test starts from ps-split rows — a ready-made spaced argv already matches at base. Every comment or doc line calling the table / command line whitespace-split or naming `pid=,pgid=,command=` is updated (`grep -n 'whitespace' internal/launcher/process_unix.go internal/launcher/server.go; grep -n 'pid=,pgid=,command=' llama-launcher.TDD.md`).
**Goal:** `(*Splash).LoadingPID` (through `processTable`) finds a loading Splash whose `--binary`/`server.py` path, `--host` or `--port` contains spaces, on darwin and linux; windows is unchanged (no process table).
**Approach (assumed at the header base):** `parseProcessTable` reads only pid and pgid positionally; `processTable` replaces `Args` of session leaders (PID == PGID) with `procArgv(pid)` when it succeeds, else `strings.Fields`. New `proc_argv_darwin.go` (`unix.SysctlRaw("kern.procargs2", pid)`: int32 argc, exec path, NUL padding, argv), `proc_argv_linux.go` (`/proc/<pid>/cmdline` NUL-split), `proc_argv_other.go` stub. `golang.org/x/sys` becomes a direct require. Amend ADR-0015's whitespace consequence.
**Files:** internal/launcher/server.go, internal/launcher/proc_argv.go, internal/launcher/proc_argv_darwin.go, internal/launcher/proc_argv_linux.go, internal/launcher/proc_argv_other.go, internal/launcher/proc_argv_test.go, internal/launcher/backend_splash_test.go, internal/launcher/process_unix.go, go.mod, docs/adr/0015-a-loading-splash-is-found-by-its-process.md, llama-launcher.TDD.md
**Read first:** internal/launcher/server.go — processEntry, processTable, parseProcessTable; internal/launcher/process_unix.go — listProcesses;
internal/launcher/backend_splash.go — LoadingPID, splashLoadingPID, isSplashServeCommand; internal/launcher/backend_splash_test.go — TestSplashLoadingPID
**Tests:** `TestParseProcArgs2` / `TestParseProcCmdline` (untagged) with a spaced path; `TestWithTrueArgv`: ps-split leader rows for argv `[/opt/My Splash/.venv/bin/python, /opt/My Splash/server/server.py, --binary, /opt/My Splash/splash, --model, m, --port, 1234]` plus a fake `argvFn` returning that argv reach `splashLoadingPID` and match; without the merge they do not.
**Acceptance:**
- `go build ./... && GOOS=linux go vet ./internal/launcher/ && GOOS=windows go build ./...`
- `go test ./internal/launcher/ -run 'TestParseProc|TestWithTrueArgv|TestSplashLoadingPID|TestParseProcessTable' -count=1`
**Commit:** `fix(launcher): match a loading Splash by its true argv`

## 9. `logs clean --days 0` is refused — ✅ DONE (2026-09-24)

NOTES (2026-09-24): consequential edit — llama-launcher.TDD.md: made necessary by the new N ≥ 1 rule on `--days` (command table row and §log cleanup "Manual" line now state that `--days 0` is refused)
NOTES (2026-09-24): the n == 0 check also refuses an empty value (`--days ""`), which the digit loop previously parsed as 0

**What:** Fixes audit finding "`logs clean --days 0` deletes every non-active `.log` file".
**Regression guard.** New tests carry the Acceptance filter's prefixes (none exists at base): `TestLogsClean_…`, not the `TestCmd…` form the filter skips. Assert the message through `captureStderr` (cli_test.go) — `runCLI` discards stderr.
**Goal:** `logs clean --days 0` exits non-zero with "--days value must be a positive integer" and deletes nothing; `--all` stays the only delete-everything spelling.
**Approach (assumed at the header base):** the digit parse loop in cli.go rejects a parsed value of 0.
**Files:** internal/launcher/cli.go, internal/launcher/cli_test.go
**Read first:** internal/launcher/cli.go — cmdLogsClean; internal/launcher/log_cleanup.go — cleanupLogs; internal/launcher/cli_test.go — captureStderr, runCLI, writeRunConfig;
internal/launcher/log_cleanup_test.go — TestCleanupLogs_DeleteAll, TestAutoCleanupLogs_DisabledRetention
**Tests:** `TestLogsClean_RefusesZeroDays`: `--days 0` and `--days 00` refused with the message and a log file left in place; `TestLogsClean_AcceptsOneDay`: `--days 1` still parses.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestLogsClean|TestRunLogs' -count=1`
**Commit:** `fix(cli): refuse logs clean --days 0`

## 10. Every start gets its own log file — ✅ DONE (2026-09-24)

NOTES (2026-09-24): createLogPath keeps its name but now returns the open *os.File (its Name() is the path), so both producers drop their own os.Create; collision retry is bounded at 50 attempts, 1 ms apart.
NOTES (2026-09-24): findManagedLogFile keeps its plain lexicographic sort. The one ordering gap is a legacy log and a millisecond log from the same second (the legacy name sorts last), which can only happen when an old and a new binary start the same backend within one second.

**What:** Fixes audit finding "Same-second start reuses the log path and truncates a live server's log".
**Regression guard.** Both producers take the new `createLogPath` return — `startManagedServer` (server.go) and `Ollama.TryStart` (backend_ollama.go, which `os.Create`s the path today). The latest-log lookup is `findManagedLogFile` (discovery.go; its YYYYMMDD-HHMMSS comment updated), pinned by `TestFindManagedLogFile` with a new-form name. Revise the TDD's log-name format block and its "Per-instance log file naming … revisit only if collisions become real" line; names stay lexicographic = chronological.
**Goal:** Two starts of the same name never share a log file: names carry millisecond precision (`<name>-20060102-150405.000.log`), the file is created exclusively, a collision retries with a fresh stamp. `parseLogTimestamp`, log cleanup, latest-log lookup and `logs` accept both stamps.
**Approach (assumed at the header base):** `createLogPath` creates the file with `O_CREATE|O_EXCL` and returns it (or its path); `logTimestampFormat` gains the millisecond form, second precision as fallback. List every consumer of the log name at write time (grep `logTimestampFormat`, `-*.log`, `parseLogTimestamp`).
**Files:** internal/launcher/server.go, internal/launcher/backend_ollama.go, internal/launcher/log_cleanup.go, internal/launcher/discovery.go, internal/launcher/log_cleanup_test.go, internal/launcher/discovery_test.go, internal/launcher/server_test.go, llama-launcher.TDD.md
**Read first:** internal/launcher/server.go — createLogPath, startManagedServer; internal/launcher/backend_ollama.go — TryStart; internal/launcher/log_cleanup.go — logTimestampFormat, parseLogTimestamp, cleanupLogs;
internal/launcher/discovery.go — findManagedLogFile; internal/launcher/discovery_test.go — TestFindManagedLogFile
**Tests:** two `createLogPath` calls in the same second yield distinct existing files; `parseLogTimestamp` parses both forms; cleanup ages a new-form name correctly; `TestFindManagedLogFile` picks a new-form name as the latest.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestCreateLogPath|TestParseLogTimestamp|TestCleanupLogs|TestFindManagedLogFile' -count=1`
**Commit:** `fix(launcher): give every start its own log file`

## 11. Legacy state cleanup deletes only files the launcher wrote — ✅ DONE (2026-09-24)

NOTES (2026-09-24): `isLegacyStateFile` reads its candidate through `readLegacyStateCandidate`: it `Lstat`s the file (so a symlink is never followed), opens it, and uses `os.SameFile` to confirm the open file is the one it inspected. The read is capped at 64 KiB + 1. A `pid`/`port` given as a quoted string is refused, because the Goal says "number" and the launcher never wrote a string.
NOTES (2026-09-24): the tests go beyond the plan's list: a backend mismatch, a missing, negative or quoted pid, a zero port, a JSON array, an oversized file, a symlink and a directory. The unix owner-uid refusal has no test, because making a file owned by another uid needs root.
NOTES (2026-09-24): the TDD §12.2 test table gained a `TestCleanupLegacyStateFiles` / `TestIsLegacyStateFile` row, and the §2 file table gained rows for `legacy_state_unix.go` / `legacy_state_windows.go`. CONTEXT.md's line on "legacy state-*.json files written by pre-live-derivation versions" stays accurate and is unchanged.

**What:** Fixes audit finding "Glob-matched delete of user files in the config directory".
**Regression guard.** `pid` must be present as a number ≥ 0, not non-zero — external connects stored `"pid": 0` (git 17c5288 `connectExternalServer`, always for lmstudio). The owner-uid check lives in new `legacy_state_unix.go` / `legacy_state_windows.go` (`syscall.Stat_t` has no windows twin; windows skips it). Tests call a new unexported `cleanupLegacyStateFiles(dir string)` (no Once); the exported wrapper stays the Once + `DefaultConfigDir()` shim, already spent by `Run` in cli_test.go.
**Goal:** `CleanupLegacyStateFiles` removes a `state-*.json` file only when its name matches `^state-(llamacpp|ollama|lmstudio)(-.+)?\.json$`, it is a regular file owned by the current uid (unix), ≤ 64 KiB, and it decodes as a JSON object whose `backend` equals the name's backend, whose `pid` is present as a number ≥ 0 and whose `port` is > 0; `state.json` passes the same checks with `backend` any of the three. Anything else stays.
**Approach (assumed at the header base):** filter the existing glob through an `isLegacyStateFile(path)` predicate.
**Files:** internal/launcher/server.go, internal/launcher/legacy_state_unix.go, internal/launcher/legacy_state_windows.go, internal/launcher/server_test.go, llama-launcher.TDD.md
**Read first:** internal/launcher/server.go — CleanupLegacyStateFiles, legacyStateCleanupOnce; internal/launcher/cli.go — Run (CleanupLegacyStateFiles call); internal/launcher/config.go — DefaultConfigDir;
internal/launcher/cli_test.go — Run helper; llama-launcher.TDD.md — legacy state cleanup paragraph
**Tests:** `cleanupLegacyStateFiles(t.TempDir())`: `state-llamacpp.json` / `state-ollama-11434.json` with legacy content and a pid-0 `state-lmstudio-1234.json` removed; a legacy `state.json` removed and a non-state `state.json` kept; `state-notes.json`, `state-ollama-backup-2024.json` with non-state JSON, and a legacy name with invalid JSON kept.
**Acceptance:**
- `go build ./... && GOOS=windows go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestCleanupLegacyStateFiles|TestIsLegacyStateFile' -count=1`
**Commit:** `fix(launcher): delete only launcher-written legacy state files`

## 12. Stop never signals a reused PID — ✅ DONE (2026-09-24)

NOTES (2026-09-24): `stopServerAt` reads the identity right after resolving the PID (inside the existing `IsProcessAlive` guard, so TDD §16.6's windows "stop verbs" line stays true); an unreadable identity skips `terminatePID` entirely. `terminatePID(pid, identity, progress)` also uses `sameProcess` as its poll condition in both wait loops (identity match implies alive), not only before the two signals — so a PID reused mid-wait ends the wait too.
NOTES (2026-09-24): helper `sameProcess(pid, identity)` added to untagged proc_identity.go beside `parseProcStatStartTime`; the darwin identity is `P_starttime` in nanoseconds (a zero value is an error). Beyond the plan's tests, `TestProcessIdentity` also checks a reaped child has no identity. The linux reader and its test were compile-checked only (`GOOS=linux go test -c`); they run on the ubuntu CI runner.
NOTES (2026-09-24): docs: TDD §6.5 step 1 (identity check), the SIGKILL port-release reasoning (polls `sameProcess`, not `IsProcessAlive`), the server_test.go row, new proc_identity file-table rows and the §16.6 platform-file list.

**What:** Fixes audit finding "TOCTOU PID-reuse kill on the stop path". Depends on item 8 (golang.org/x/sys becomes a direct require there).
**Regression guard.** The helper is `processIdentity` — `processStartTime(pid) time.Time` already exists (discovery.go, uptime). The pure `/proc/<pid>/stat` field-22 parser lives in untagged proc_identity.go; only the readers are build-tagged. `TestTerminatePID`'s `terminatePID(pid, nil)` call is updated; the TDD §6.5 stop escalation gains the identity check.
**Goal:** The stop path records the target PID's start time when it resolves the PID and signals only while it still matches — checked before SIGTERM and again before SIGKILL. A mismatch or unreadable identity sends no further signal and reports the PID as gone.
**Approach (assumed at the header base):** `processIdentity(pid) (int64, error)` in `proc_identity_darwin.go` (`unix.SysctlKinfoProc("kern.proc.pid", pid)` → `Proc.P_starttime`), `proc_identity_linux.go` (`/proc/<pid>/stat` field 22, parsed after the last `)`), `proc_identity_windows.go` (matching whatever `terminatePID` does there). `stopServerAt` captures it; `terminatePID` takes it.
**Files:** internal/launcher/server.go, internal/launcher/proc_identity.go, internal/launcher/proc_identity_darwin.go, internal/launcher/proc_identity_linux.go, internal/launcher/proc_identity_windows.go, internal/launcher/proc_identity_test.go, internal/launcher/server_test.go, llama-launcher.TDD.md
**Read first:** internal/launcher/server.go — stopServerAt, terminatePID, findListeningPID; internal/launcher/discovery.go — processStartTime; internal/launcher/process_unix.go — signalPID, signalGroup;
internal/launcher/server_test.go — TestTerminatePID; llama-launcher.TDD.md — §6.5 stop escalation
**Tests:** `processIdentity(os.Getpid())` is stable across calls, and two children spawned ≥ 20 ms apart differ (linux starttime counts 10 ms ticks); `TestParseProcStat` (untagged) handles a `comm` containing `) (`; `terminatePID` with a mismatching identity leaves a real `sleep` child alive; `TestTerminatePID` passes with its updated call.
**Acceptance:**
- `go build ./... && GOOS=linux go vet ./internal/launcher/ && GOOS=windows go build ./...`
- `go test ./internal/launcher/ -run 'TestProcessIdentity|TestParseProcStat|TestTerminatePID|TestStopServerAt' -count=1`
**Commit:** `fix(launcher): verify PID identity before each stop signal`

## 13. "Edit config" works where it can and is hidden where it cannot — ✅ DONE (2026-09-24)

NOTES (2026-09-24): editConfigCommand takes the config path and returns the built `*exec.Cmd` plus `ok` rather than a builder func; `menuOffersEdit(cfg)` wraps it for the six menu sites, and `doEditConfig` returns a new `errNoEditor` if reached with no editor (defensive; the menus never offer it then).
NOTES (2026-09-24): the platform seam is a package var `editConfigGOOS = runtime.GOOS` in menu.go; TDD §16.6 says platform knowledge never sits in a shared-path `runtime.GOOS` branch, so §16.6 now names this read as a deliberate exception (no platform primitive, both branches compile everywhere).
NOTES (2026-09-24): the numbered fallbacks are tested end to end (stdin piped with `q`/`e`, stdout captured) rather than through an extracted prompt builder; the TUI lists come from the extracted `stoppedMenuItems` / `loadedMenuItems` / `idleMenuItems`.

**What:** Fixes audit finding "\"Edit config\" is macOS-only but offered in every menu variant" (ADR-0012).
**Regression guard.** New tests carry the filter's prefixes (only TestMenuRefreshInterval matches today). The key-migration prompt offers no Edit config (Move / Not now / Never) and is out of the Goal. Every `doEditConfig` site consults `ok`: the 3 TUI item appends and cases (`runStoppedMenu`, `runLoadedMenu`, `runIdleMenu`) and the 3 `*Simple` menus, including the `[1-%d, e, q]` / `[1-%d, s, e, q]` prompts, the numbered item and the `e` branch. The item lists come from extracted builders (or a `menuOffersEdit` helper) the test asserts; an empty `VISUAL`/`EDITOR` counts as unset.
**Goal:** On darwin the verb runs `open <config>`; elsewhere `$VISUAL`, else `$EDITOR` (split on whitespace, config path appended, terminal attached) and waits; with neither set the verb is absent from every menu variant.
**Approach (assumed at the header base):** `editConfigCommand() (*exec.Cmd builder, ok bool)` beside `doEditConfig` (menu.go); find the sites by grepping `doEditConfig` and the "Edit config" label.
**Files:** internal/launcher/menu.go, internal/launcher/menu_test.go, README.md, llama-launcher.TDD.md
**Read first:** internal/launcher/menu.go — doEditConfig, runStoppedMenu, runLoadedMenu, runIdleMenu, runStoppedMenuSimple, runLoadedMenuSimple, runIdleMenuSimple; internal/launcher/ui.go — selectMenu
**Tests:** `TestEditConfig…` with `runtime.GOOS` behind a seam: darwin → `open`; linux with `EDITOR="code -w"` → `code -w <path>`; linux with `VISUAL`/`EDITOR` cleared by `t.Setenv` (or empty) → `TestMenu…` asserts the extracted builders' item lists and the `*Simple` prompts omit the verb.
**Acceptance:**
- `go build ./... && GOOS=linux go vet ./internal/launcher/ && GOOS=windows go build ./...`
- `go test ./internal/launcher/ -run 'TestEditConfig|TestMenu' -count=1`
**Commit:** `fix(menu): offer Edit config only where an editor can open it`

## 14. Ollama's last-started fields are race-free

**What:** Fixes audit finding "Data race on Ollama's last-started PID and log fields".
**Regression guard.** No exec seam exists (`TryStart` calls `exec.LookPath("ollama")` and `exec.Command(binary, "serve")` directly) and none is added: the test puts a stub `ollama` script on `t.Setenv("PATH", …)` (not parallel, unix-only), as server_test.go's empty-PATH test does.
**Goal:** `TryStart`'s writes and the `LastStartedPID`/`LastStartedLogFile` reads are guarded by the RWMutex already guarding the API key (write under `Lock`, read under `RLock`); `go test -race` of concurrent `TryStart`/reads is clean.
**Approach (assumed at the header base):** take the existing lock at each write and read site; no production seam.
**Files:** internal/launcher/backend_ollama.go, internal/launcher/backend_ollama_test.go
**Read first:** internal/launcher/backend_ollama.go — Ollama.TryStart, LastStartedPID, LastStartedLogFile; internal/launcher/backend.go — apiKeyHolder, PIDTracker; internal/launcher/server.go — connectExternalServer;
internal/launcher/integration_ollama_test.go — TryStart/LastStartedPID caller; internal/launcher/backend_ollama_test.go — TestOllamaTryStop_IsNoOpAndNeverErrors
**Tests:** concurrent `LastStartedPID` reads against a stub-driven `TryStart`, under `-race`.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -race -run 'TestOllama' -count=1`
**Commit:** `fix(launcher): guard Ollama last-started fields with its mutex`

## 15. The external start arm reports timeouts like the managed arm

**What:** Fixes audit finding "`ErrStartupTimeout` is not wrapped on the external arm; retries spawn orphan daemons". Depends on item 14 (same file).
**Regression guard.** The external arm omits the PID line when PID is 0 and the Log line when LogFile is empty (LM Studio implements no `PIDTracker`), and its guidance names only commands that work on a not-yet-healthy external server (`logs`/`stop` answer "No server running." there); the managed `startupTimeoutErr` text stays byte-identical. It closes the gap docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md (Consequences) and the TDD ("Does not hold — auto-starting an external server") record; both are updated.
**Goal:** When an external backend's (Ollama, LM Studio) post-start wait times out, the error matches `errors.Is(err, ErrStartupTimeout)` and names the spawned PID and log path; a `TryStart` failure (e.g. binary not in PATH) is returned as its own error, not the generic not-reachable text. The wait duration is unchanged.
**Approach (assumed at the header base):** `connectExternalServer` (server.go) builds a `RunningInstance` from `LastStartedPID`/`LastStartedLogFile`, wraps the timeout with `startupTimeoutErr`, and returns `TryStart`'s error when non-nil.
**Files:** internal/launcher/server.go, internal/launcher/server_test.go, launcher/doc.go, docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md, llama-launcher.TDD.md
**Read first:** internal/launcher/server.go — connectExternalServer, startupTimeoutErr, ErrStartupTimeout, identifyBackend; internal/launcher/backend_lmstudio.go — LMStudio.TryStart;
internal/launcher/backend_ollama.go — LastStartedPID; launcher/doc.go — ErrStartupTimeout paragraph; internal/launcher/cli.go — cmdLogs
**Tests:** fake external backend whose health never passes → `errors.Is(ErrStartupTimeout)`, message contains the PID and log; an LM Studio-shaped backend (no `PIDTracker`) times out with neither a `PID 0` nor an empty `Log:` line; the managed-arm message is unchanged; `TryStart` returning "not found in PATH" surfaces that text.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/ ./launcher/`
- `go test ./internal/launcher/ ./launcher/ -run 'TestConnectExternal|TestStartupTimeout|TestLoadProfile' -count=1`
**Commit:** `fix(launcher): wrap ErrStartupTimeout on the external start arm`

## 16. Stop cancels an in-flight managed load promptly

**What:** Fixes audit finding "The documented \"cancel with Stop\" is exactly the forbidden concurrent call". Depends on item 15 (both edit startupTimeoutErr and launcher/doc.go's ErrStartupTimeout paragraph).
**Regression guard.** Only a non-zero exit code is a crash; status 0 (Splash traps SIGTERM and exits 0), signal death, 128+SIGTERM/SIGINT or an unreadable status is `ErrLoadCanceled`. Liveness comes only from the `Wait` result `startManagedServer` owns (carried on the instance or returned from start); with no probe attached the wait runs as today — no `IsProcessAlive` fallback (fakeOps.start's PID 4242 would flip `TestLoadProfile_StartupTimeoutIsErrStartupTimeout`). Every sentence requiring per-address serialization of lifecycle calls gains the Stop-cancels-a-load carve-out (`grep -rn 'serializ' launcher/ README.md llama-launcher.TDD.md docs/adr/`), amending docs/adr/0011-public-library-facade.md's serialization line. The `ErrLoadCanceled` identity row goes in launcher_internal_test.go's `TestNewSentinels_AliasTheCoreValues`.
**Goal:** When the server a managed `LoadProfile` spawned exits mid-health-wait, the load returns within two health-poll intervals: a non-crash exit matches `errors.Is(err, launcher.ErrLoadCanceled)`; a non-zero exit code returns a crash error carrying the redacted log tail. Neither matches `ErrStartupTimeout`. The facade docs keep "cancel with Stop" and name `ErrLoadCanceled`.
**Approach (assumed at the header base):** the managed arm's `WaitForHealth` call gets a liveness probe for the spawned PID; new sentinel in internal/launcher re-exported from `launcher/launcher.go`.
**Files:** internal/launcher/server.go, internal/launcher/server_test.go, launcher/launcher.go, launcher/doc.go, launcher/launcher_internal_test.go, README.md, llama-launcher.TDD.md, docs/adr/0011-public-library-facade.md
**Read first:** internal/launcher/server.go — loadProfileManaged, startManagedServer, WaitForHealth; internal/launcher/server_test.go — TestLoadProfile_StartupTimeoutIsErrStartupTimeout, fakeOps.start;
launcher/launcher_internal_test.go — TestNewSentinels_AliasTheCoreValues; launcher/doc.go — "Verbs block; cancellation is Stop"; ~/Repos/splash/server/server.py — _interrupt
**Tests:** fake managed start whose process exits by signal after one poll → `ErrLoadCanceled` well under the timeout; exit status 0 → `ErrLoadCanceled`; exit code 1 → crash error, not canceled; `TestLoadProfile_StartupTimeoutIsErrStartupTimeout` stays green; `TestNewSentinels_AliasTheCoreValues` gains the `ErrLoadCanceled` row.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/ ./launcher/`
- `go test ./internal/launcher/ ./launcher/ -run 'TestWaitForHealth|TestLoadProfile|TestLoadCanceled|TestNewSentinels' -count=1`
**Commit:** `fix(launcher): return ErrLoadCanceled when Stop ends a loading server`

## 17. Unload acts only on the backend it names

**What:** Fixes audit finding "`Unload(backend, addr)` acts on whatever the address actually hosts". Depends on item 6.
**Regression guard.** An AuthFailed occupant returns the ErrAuthFailed message before the mismatch check runs. The check sits in `unloadServerModel` behind the `activationOps` identity method item 6 adds (realOps → `identifyBackend`; fakeOps has it, steppedOps embeds fakeOps) — not in `StopInstance`/`UnloadInstanceModel`, which the auto-stop/auto-unload sweeps share; `TestUnload_Orchestration`'s `fakeOps{}` subtests get the matching occupant. Refuse only on a positive mismatch; on `ErrNotRunning` fall through to today's arms (`TestUnload_ServerStoppedFollowsBackendKind` pins `ServerStopped=true`). The managed-name leg stays internal with fakeOps.
**Goal:** `Unload(backend, addr)` (CLI and facade) returns an error matching `ErrNotRunning` with text `no <backend> server at <addr> (<occupant> is serving there)` when the resolved occupant's backend differs, compared before either arm, and signals or unloads nothing.
**Approach (assumed at the header base):** in `unloadServerModel` (server.go), resolve the occupant through the identity method and compare its backend to the named one.
**Files:** internal/launcher/server.go, internal/launcher/server_test.go, launcher/launcher_test.go
**Read first:** internal/launcher/server.go — unloadServerModel, activationOps, realOps, identifyBackend; internal/launcher/server_test.go — TestUnload_Orchestration, fakeOps;
launcher/launcher_test.go — TestUnload_ServerStoppedFollowsBackendKind, newOllamaStandIn
**Tests:** facade: `Unload("lmstudio", newOllamaStandIn(...).addr)` → `ErrNotRunning` and zero `unloadRequests()`; internal: named `llamacpp` with a fakeOps occupant of another backend → `ErrNotRunning`, no stop call; an AuthFailed occupant → the `ErrAuthFailed` message; the matching name still unloads; `TestUnload_ServerStoppedFollowsBackendKind` stays green.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/ ./launcher/`
- `go test ./internal/launcher/ ./launcher/ -run 'TestUnload' -count=1`
**Commit:** `fix(launcher): refuse Unload when another backend holds the address`

## 18. The memory readout never freezes the menu

**What:** Fixes audit finding "The menu's memory readout holds its cache mutex across unbounded subprocesses".
**Regression guard.** New tests carry the filter's prefixes (`TestReadMemStats…` / `TestSysmem…`). A timeout is published with `memCacheAt` stamped (the last good data, or the error) so the TTL still throttles retries — never an unstamped skip that re-runs a hung `vm_stat` on every keystroke. An `ioreg` timeout keeps the GPU fields at zero, as a `gerr` does today. The timeout is a package var the test shortens.
**Goal:** Each `ReadMemStats` subprocess runs under a 2 s context timeout; a timeout skips that tick (last cached value, or none); `memCacheMu` is held only to read and publish the cache, never across a subprocess.
**Approach (assumed at the header base):** `exec.CommandContext` with `context.WithTimeout` in sysmem.go; compute into locals, then lock to publish.
**Files:** internal/launcher/sysmem.go, internal/launcher/sysmem_test.go
**Read first:** internal/launcher/sysmem.go — ReadMemStats, readMemStatsLive, memStatsCacheTTL, memCacheMu; internal/launcher/menu.go — serverStatusLines; internal/launcher/ui.go — selectMenu headerFn render loop;
internal/launcher/sysmem_test.go — TestParseVMStat, TestParseIOAccelerator
**Tests:** `TestReadMemStats…` with a command-runner seam whose first call blocks and a shortened timeout: `ReadMemStats` returns within the timeout, a concurrent cache read is not blocked, a second call inside the TTL runs no subprocess; an `ioreg` timeout yields zero GPU fields; `-race` clean.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -race -run 'TestReadMemStats|TestSysmem' -count=1`
**Commit:** `fix(launcher): bound memory-readout subprocesses and drop the lock across them`

## 19. Server-reported model ids are bounded

**What:** Recast at the regression check (2026-09-24). Fixes audit finding "Unbounded server-reported strings reach the terminal and the MCP surface".
**Regression guard.** The float half of the finding is rejected: live params carry no server-reported floats (QueryLiveParams returns only ContextSize/Parallel; maskUnreported nils every fresh float); formatFloatPtr is untouched. Bound only the copy that is stored and displayed: in `probeInstance` after `matchProfileName` has run on the full sanitized id, and `loadProfile`'s returned `RunningInstance.ActiveModel`. `liveLoadedModel`'s return stays unbounded — it is handed back to the server (`UnloadModel` in `UnloadInstanceModel` and `loadProfileExternal`'s auto-unload, `modelNamesMatch` in `loadProfile`'s no-op check). The HTTP-driven test's id stays under `boundedBody`'s 512 KiB cap.
**Goal:** A server-reported model id is truncated to 512 bytes (on a rune boundary, `…` appended) after sanitization, where it is stored in `RunningInstance`.
**Approach (assumed at the header base):** a `boundModelID` helper applied at the two sites the guard names.
**Files:** internal/launcher/discovery.go, internal/launcher/server.go, internal/launcher/discovery_test.go, internal/launcher/server_test.go
**Read first:** internal/launcher/server.go — liveLoadedModel, loadProfile, UnloadInstanceModel; internal/launcher/discovery.go — probeInstance, matchProfileName;
internal/launcher/backend_http.go — sanitizeServerString, boundedBody; internal/launcher/discovery_test.go — TestDiscoverRunningInstances_SanitizesModelName
**Tests:** `TestDiscover…`: a 64 KiB model id served over httptest → `ActiveModel` ≤ 512 bytes + `…`, and `ActiveProfile` still matches its profile; `TestBoundModelID`: a multibyte rune straddling byte 512 is not split, `…` is appended, an id ≤ 512 bytes is unchanged; `TestUnloadInstanceModel…`: an over-512-byte id reaches `UnloadModel` whole; `TestLoadProfile…`: an over-512-byte live id still matches the profile (no restart).
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestBoundModelID|TestDiscover|TestUnloadInstanceModel|TestLoadProfile' -count=1`
**Commit:** `fix(launcher): bound server-reported model ids`

## 20. Config parse errors never echo file content

**What:** Fixes audit finding "`--config` parses any readable file; its content can surface in errors". Depends on item 1 (same file).
**Regression guard.** Every `yaml.Unmarshal` of config bytes goes through `redactYAMLError` (`grep -n 'yaml.Unmarshal' internal/launcher/*.go`), including configwrite.go's `serverEntryConfig` ("it does not parse") and `verifiedEntrySplice` ("the edited file would not parse"). Redaction runs over the whole message string — every backticked span and the quoted name in "unknown anchor 'x' referenced" — keeping every non-value word, since `ServerConfig.UnmarshalYAML` wraps the `*yaml.TypeError` with `%w` and a type switch misses it. Test bite values are ≤ 10 chars or assert the truncated spelling (`secretv`, `-----BE`) — yaml.v3 already cuts longer values; the mismatch sits under `defaults:` (Config has no top-level `port`).
**Goal:** A YAML syntax or type error from `LoadConfig` reports the path, the line and the kind of mismatch, and contains no value fragment of the file (yaml.v3's backticked values become `…`).
**Approach (assumed at the header base):** `parseConfig` wraps the `yaml.Unmarshal` error through `redactYAMLError`.
**Files:** internal/launcher/config.go, internal/launcher/configwrite.go, internal/launcher/config_test.go
**Read first:** internal/launcher/config.go — parseConfig, ServerConfig.UnmarshalYAML; internal/launcher/configwrite.go — serverEntryConfig, verifiedEntrySplice; internal/launcher/cli.go — cmdConfigValidate;
internal/launcher/config_test.go — TestParseConfig, TestServerConfigUnmarshal
**Tests:** `TestRedactYAMLError`/`TestParseConfig…`: `defaults:\n  port: hunter2` → error names the line and the mismatch, not `hunter2`; `servers:\n  llamacpp: hunter2` (the `%w`-wrapped path) keeps its "server entry must be a bool or a mapping" prefix and drops `hunter2`; a line of `-----BEGIN OPENSSH PRIVATE KEY-----` style content → no `-----BE`; an unknown-anchor error drops the anchor name.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestParseConfig|TestLoadConfig|TestRedactYAMLError' -count=1`
**Commit:** `fix(config): keep file content out of config parse errors`

## 21. The facade's no-stderr contract names the Reload exception

**What:** Fixes audit finding "The facade's \"never writes to stderr\" contract is reachably false through the `Config` alias" by the ratified doc carve-out.
**Regression guard.** Every doc sentence stating the library never writes to stderr names the `Reload` exception (`grep -rn 'never writes to' README.md llama-launcher.TDD.md launcher/` — README's library section, TDD §16 contract decision 2, doc.go). Drop only the "Do not use Config.Reload…" half of doc.go's later sentence; keep "Re-read a changed config file by calling LoadConfig again", the facade's only re-read instruction. Acceptance checks the headline sentence and `Config.Reload`'s comment, not any `Reload` mention.
**Goal:** The `launcher/doc.go` sentence stating the library never writes to its host's stderr names `Config.Reload` as the exception in that same sentence; `Config.Reload`'s doc comment (internal/launcher/config.go) says it writes warnings to stderr and is for the CLI only.
**Approach (assumed at the header base):** edit the package doc headline and the `Reload` comment; drop the redundant half-sentence as the guard binds.
**Files:** launcher/doc.go, internal/launcher/config.go, README.md, llama-launcher.TDD.md
**Read first:** launcher/doc.go — package comment "Notices are callbacks"; internal/launcher/config.go — Config.Reload, LoadConfig; README.md — library section;
llama-launcher.TDD.md — §16 contract decision 2; docs/adr/0011-public-library-facade.md
**Tests:** none (doc-only); `go doc ./launcher` shows the new headline.
**Acceptance:**
- `go build ./... && go vet ./launcher/`
- `go doc ./launcher | grep -A1 "never writes to its host's stderr" | grep -n 'Reload'`
- `go doc ./internal/launcher Config.Reload | grep -i stderr`
- `go doc ./launcher | grep -n 'calling LoadConfig again'`
- `grep -rn 'never writes to' README.md llama-launcher.TDD.md launcher/doc.go` (each hit names `Reload`)
**Commit:** `docs(launcher): name the Reload exception in the no-stderr contract`

## 22. One check list feeds both config validators

**What:** Fixes audit finding "Two diverged copies of the config validator". Depends on items 1, 20 and 21 (same file).
**Regression guard.** Item 1's trust gate is a precondition of running commands, not a validator check; it stays in resolveKeyCommands, outside this Goal. Checks return errors and warnings separately: `validate` fails on the first error and stores warnings in `c.Warnings`; `validateAll` returns errors + warnings + `ResolveProfile` errors, as today (a severity-less list would refuse a defaults.server-fallback config). Each surface keeps its own prefix (`config: `), indent (`\n  Move to` vs `\n     Move to`), wording ("unknown LLM server" vs "unknown server") and order.
**Goal:** `validate` and `validateAll` run the same check list: `validate` returns the first error, `validateAll` every error plus the per-profile `ResolveProfile` errors. No check exists in only one of them; every existing validation test passes unchanged.
**Approach (assumed at the header base):** extract `configChecks(c)` (or a slice of check funcs) from the two bodies in config.go; one deep function, two thin callers.
**Files:** internal/launcher/config.go, internal/launcher/config_test.go
**Read first:** internal/launcher/config.go — validate, validateAll, apiKeyWarnings, defaultsServerFallbackWarnings, memoryBarWarnings; internal/launcher/cli.go — cmdConfigValidate;
internal/launcher/config_test.go — TestValidateAll, TestValidate_DefaultsServerFallbackWarning
**Tests:** a table of single-offender invalid configs (both loops range maps) asserting the same check fires in both `validate` and `validateAll` (substring of the check's own wording), each keeping its surface's prefix, indent and wording; a defaults.server-fallback config still loads with the warning in `c.Warnings`.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestValidate|TestLoadConfig|TestConfigValidate' -count=1`
**Commit:** `refactor(config): derive both validators from one check list`
