# Open issues sweep — plan

**Goal:** Resolve the 13 open, unplanned capcom issues: four defects, three test gaps, two features, one performance task and four doc gaps.
**Date:** 2026-10-13
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** 89aedca
**Sources:** `llama-launcher.TDD.md`; `README.md`; `.capcom/issues/<id>.md` per item; Note `decisions` (ADR-0016 API keys, ADR-0017 cobra only in `cmd/release`); Note `glossary`.

**Ratified design calls** (user, 2026-10-13):
- **api_key_cmd on Reload:** Reload reuses an entry's resolved key when its `api_key_cmd` string is unchanged; a new or changed command runs once.
- **Switch-model diff:** a confirm popup after the pick lists only the changed params (current → target); enter loads, q cancels; simple mode prints the delta and asks y/N.
- **Completions:** `llama-launcher completions bash|zsh|fish` prints a script that calls a hidden `__complete` subcommand for live names; the Homebrew formula installs them.
- **README requirements:** new `## Requirements` section with a runtime host-binaries table per OS.
- **README api_key table:** one sentence under the table saying `api_key_cmd` has the same per-backend effect as `api_key` (no new row or column).

**Standing requirements:**
- skills: coding-standards
- Every behaviour change updates README and TDD in the same item (CHANGELOG entry travels in the sidecar).
- Hand-rolled CLI: no cobra or `flag` in `internal/launcher` (ADR-0017).

**Out of scope:**
- `changelog-unreleased-heading-missing` (looks stale; not in this plan).
- IPv6 wildcard (`::`) probing beyond what `probeAddr` already maps.
- Any version bump or release.

**Regression check (2026-10-13, 89aedca):**
- 1: guard folded
- 4: guard folded; yields to the byte-identical contract (`llama-launcher.TDD.md` §5.2 `configwrite.go` row; `configwrite.go` header comment)
- 5: guard folded (decision); supersedes `llama-launcher.TDD.md` §4.2 Reload sentence, the `keyCommandTimeout` comment and the `resolvedKey` comment in `config.go`
- 6: guard folded
- 8: guard folded
- 9: guard folded
- 10: recast (decision); guard folded
- 11: guard folded
- 12: guard folded
- 13: guard folded
- 10: guard folded (decision, re-check round): no key resolution in `__complete`; Approach failure wording; dispatch before `--config` filter; bash-3.2 builtins only
- 13: rejected — add `defaults/embed.go` to the §5.2 rows; §5.2 already has the `defaults/embed.go` row (llama-launcher.TDD.md:490)

## 1. Skip TestStopServerAt_StartingOccupant when lsof cannot see the listener — ✅ DONE (2026-10-13)

NOTES (2026-10-13): TestStopServerAt_AuthFailedListener carries the same hard-fail `t.Fatalf("nc (PID %d) never showed up listening…")` after its findListeningPID poll; left unchanged as outside this item's named test.

**What:**
**Goal:** `TestStopServerAt_StartingOccupant` skips (never fails) on a host where `lsof` is missing or cannot see the `nc` listener; it still runs and passes where it can.
**Approach (assumed at the header base):** in the "signalling the listener…" subtest of `TestStopServerAt_StartingOccupant` (`internal/launcher/server_test.go`), skip when `exec.LookPath("lsof")` fails, and turn the `t.Fatalf("nc (PID %d) never showed up listening…")` after the `findListeningPID` poll into `t.Skipf`, matching the precedent in `TestStartManagedServer_RefusesWhenPortOccupied`.
**Regression guard.** The second Acceptance run keeps `nc` and drops `lsof` — on macOS `PATH=/usr/bin:$(dirname $(command -v go))` (`nc` is `/usr/bin/nc`, `lsof` is `/usr/sbin/lsof`); a `/nonexistent:` PATH drops `nc` too, so the existing "nc not available" skip fires at base. That run must report SKIP with the new lsof skip text, not "nc not available".
**Files:** internal/launcher/server_test.go
**Read first:** internal/launcher/server_test.go — TestStopServerAt_StartingOccupant, TestStartManagedServer_RefusesWhenPortOccupied, toolOnlyPATH; internal/launcher/server.go — findListeningPID, findListeningPIDs; internal/launcher/helpers_test.go — deadAddr
**Closes:** starting-occupant-test-lsof
**Tests:** the subtest itself; run with a `PATH` that keeps `nc` and lacks `lsof` to see the new lsof skip text.
**Acceptance:**
- `go test ./internal/launcher/ -run TestStopServerAt_StartingOccupant -v`
- `PATH=/usr/bin:$(dirname $(command -v go)) go test ./internal/launcher/ -run TestStopServerAt_StartingOccupant -v` reports SKIP with the lsof skip text (not "nc not available"), not FAIL
**Commit:** `fix(test): skip starting-occupant stop test when lsof cannot see nc`

## 2. Test keystore entry names containing a single quote — ✅ DONE (2026-10-13)

**What:** test-only. Add cases with an entry name containing `'` (e.g. `bob's key`) to `TestReadCmdIsTheDocumentedSpelling` (expected spelling `'bob'\''s key'` for keychain and secret service) and to `TestReadCmdReadsBackWhatWriteStored` (round-trip through `readKeyThroughShell` via `useFakeTools`), in `internal/keystore/keystore_test.go`. If a case exposes a `shellWord` bug, fix it in `internal/keystore/keystore.go` and note it in the commit body.
**Files:** internal/keystore/keystore_test.go; internal/keystore/keystore.go
**Read first:** internal/keystore/keystore_test.go — TestReadCmdIsTheDocumentedSpelling, TestReadCmdReadsBackWhatWriteStored, useFakeTools, readKeyThroughShell; internal/keystore/keystore.go — Store.ReadCmd, shellWord, isPlainWord, Store.Write
**Closes:** keystore-quote-name-test
**Tests:** the two named tests.
**Acceptance:** `go test ./internal/keystore/ -run 'TestReadCmd' -v`
**Commit:** `test(keystore): cover entry names containing a single quote`

## 3. Test list --json — ✅ DONE (2026-10-13)

**What:** test-only. Add `TestCmdListJSON_*` tests in `internal/launcher/cli_test.go` mirroring the `status --json` pattern (`captureStdout` around `cmdListJSON(cfg)`, a test-side decode struct): every enabled profile appears with `name`, `backend`, `model`; `gpu_layers`/`context_size` present when set and omitted when unset; profiles of disabled servers are absent; output is valid JSON for an empty profile set. One end-to-end case via `writeRunConfig` + `runCLI(t, cfgPath, "list", "--json")`.
**Files:** internal/launcher/cli_test.go
**Read first:** internal/launcher/cli.go — cmdListJSON, cmdList; internal/launcher/config.go — Config.ProfileNames, IsServerEnabled; internal/launcher/cli_test.go — captureStdout, writeRunConfig, runCLI, TestRun_StatusJSONNothingRunning
**Closes:** list-json-test-gap
**Tests:** the new tests.
**Acceptance:** `go test ./internal/launcher/ -run 'TestCmdListJSON|TestRun_ListJSON' -v`
**Commit:** `test(cli): cover list --json output`

## 4. Preserve CRLF line endings on config rewrite — ✅ DONE (2026-10-13)

NOTES (2026-10-13): followed the Regression guard over the Approach — `splitConfigLines` now splits after "\n" so each line keeps its own ending, `joinConfigLines(lines, newline)` gives only ending-less (rewritten/inserted, or an unclosed last) lines the file's detected newline (`configNewline`); `scalarLineParts` reads the line through `configLineText`. The `configwrite.go` header comment and the TDD §5.2 row now state that line endings are kept.

**What:** fix — `saveServerKeySource` on a CRLF config writes LF-only lines (inserted `plaintext_key_ok` marker, possibly the rewritten `api_key_cmd` line), producing mixed endings.
**Goal:** after any `saveServerKeySource` edit, a CRLF config file uses CRLF on every line and an LF file uses LF on every line.
**Approach (assumed at the header base):** `splitConfigLines` detects `\r\n` (precedent: `rollUpChangelog` in `cmd/release/prepare.go`), strips `\r` from each line and returns the detected newline; `joinConfigLines` joins with it. Thread the newline through `spliceEntryKeyCommand` / `spliceEntryPlaintextKeyOK` callers, never per-line `\r` bookkeeping.
**Regression guard.** Keep each existing line's own ending — split after `"\n"` as `rollUpChangelog` does (`cmd/release/prepare.go`) — and give only the rewritten or inserted line the file's detected newline; this replaces the Approach's strip-and-rejoin, so untouched lines of a mixed-ending file stay byte-identical. Yields to the byte-identical contract (`llama-launcher.TDD.md` §5.2 `configwrite.go` row; `configwrite.go` header comment); that TDD row gains that the file's line endings are kept.
**Files:** internal/launcher/configwrite.go; internal/launcher/configwrite_test.go; llama-launcher.TDD.md
**Read first:** internal/launcher/configwrite.go — splitConfigLines, joinConfigLines, spliceEntryKeyCommand, spliceEntryPlaintextKeyOK; cmd/release/prepare.go — rollUpChangelog; internal/launcher/configwrite_test.go — writeConfigFixture, TestSaveServerKeyCommand_RewritesOnlyTheKeyLine, TestSaveServerPlaintextKeyOK_AppendsTheMarker
**Closes:** configwrite-crlf-mixed-endings
**Tests:** CRLF fixtures for both splices (rewrite to `api_key_cmd`, insert marker, rewrite existing marker): assert no bare `\n` remains (`strings.Count(out,"\n") == strings.Count(out,"\r\n")`); LF fixture unchanged; a mixed-ending fixture keeps every untouched line's own ending.
**Acceptance:** `go test ./internal/launcher/ -run 'SaveServer|CRLF' -v`
**Commit:** `fix(config): keep CRLF line endings when rewriting key source lines`

## 5. Resolve api_key_cmd once per entry across menu reloads — ✅ DONE (2026-10-13)

NOTES (2026-10-13): extracted the CLI warning printer into `printConfigWarning` so `Reload` can call the new unexported `loadConfig(path, notify, previous)` with the same sink `LoadConfig` binds; LoadConfig/LoadConfigNotify signatures and behaviour unchanged.

**What:** fix — `Config.Reload()` (menu loop and `liveStatusHeaderFn` probe tick) re-runs every `api_key_cmd`, contradicting "one subprocess per configured entry per run". Ratified: carry the key across Reload when the command is unchanged.
**Goal:** with the menu open, an unchanged `api_key_cmd` runs exactly once per launcher run; editing an entry's `api_key_cmd` makes it run once more; a failed resolve is retried on the next Reload.
**Approach (assumed at the header base):** in `(*Config).Reload`, before `*c = *newCfg`, pass the old config's per-server `(APIKeyCmd, resolvedKey)` into the load so `resolveKeyCommands` skips `runKeyCommand` for a server whose `APIKeyCmd` string is identical and whose previous resolve succeeded. Keep the cache on the `Config` value (no package-level state). `configTrusted` still gates every run.
**Regression guard.** the key cache rides a non-exported load variant used only by Reload; exported LoadConfig and LoadConfigNotify keep their current behaviour and signatures. `configTrusted` runs on exactly today's condition (any enabled entry with a non-blank `api_key_cmd`) before reuse vs run is decided, so a reused key never skips the trust gate. Docs: every prose site saying Reload / each load re-runs `api_key_cmd` is updated — `grep -rn -i 'reload\|once per load\|at load' internal/launcher/config.go llama-launcher.TDD.md README.md | grep -i 'key'` — superseding TDD §4.2 ("the interactive menu's `Reload` re-runs them"), the `keyCommandTimeout` comment, the `resolvedKey` field comment and the TDD §5.2 `config.go` row.
**Docs:** TDD §4.2 api_key_cmd paragraph and the comment above `keyCommandTimeout` drop "Reload re-runs it deliberately" and state the new rule; README `### API keys` wording "runs once at startup" stays true — add "and again only when the command changes".
**Files:** internal/launcher/config.go; internal/launcher/config_test.go; llama-launcher.TDD.md; README.md
**Read first:** internal/launcher/config.go — Config.Reload, LoadConfigNotify, resolveKeyCommands, runKeyCommand, ServerConfig.resolvedKey; internal/launcher/menu.go — liveStatusHeaderFn; internal/launcher/config_test.go — keySourceConfig, TestLoadConfig_KeyCommandTrustGate
**Closes:** api-key-cmd-reruns
**Tests:** a counting fake key command (script appending to a file): two Reloads → one run; change the command and Reload → two runs; failing command → reruns on next Reload; a Reload case with a cached command after `chmod 0620` on the config → receiver unchanged; exported `LoadConfig`/`LoadConfigNotify` still run the command on every call.
**Acceptance:** `go test ./internal/launcher/ -run 'KeyCommand|Reload|APIKey' -v`
**Commit:** `fix(config): reuse resolved api_key_cmd keys across reloads`

## 6. Build and parse host:port with net.JoinHostPort / net.SplitHostPort — ✅ DONE (2026-10-13)

NOTES (2026-10-13): applyBackendFallbacks parses with net.SplitHostPort directly rather than splitHostPort, so an unparseable port still keeps the host and falls back to the default port as before; splitHostPort would have dropped the host too.

NOTES (2026-10-13): re-derived from the assumption that the producers and splitHostPort were the only address parses: LMStudio.TryStart (backend_lmstudio.go) also cut the addr at the first colon to get --port; it now uses net.SplitHostPort. No unit test, since TryStart needs the lms binary.

NOTES (2026-10-13): consequential edit — internal/launcher/backend_http_test.go: made necessary by joinHostPort/splitHostPort; dropped the TestProbeAddr comment saying IPv6 host addresses are out of scope launcher-wide.

NOTES (2026-10-13): consequential edit — llama-launcher.TDD.md: made necessary by joinHostPort/splitHostPort; §6 instance-identity paragraph now says how addresses are built and parsed, including bracketed IPv6.

NOTES (2026-10-13): CLOSES names ipv6-host-address-format as the plan's Closes line does, but that line says "(with item 7)": the issue should close only once item 7 (the lsof listener filter) lands too.

**What:** fix — an IPv6 host yields `::1:8000`; `splitHostPort` cuts at the first colon.
**Goal:** every launcher-built address for an IPv6 host is bracketed (`[::1]:8000`), every address parse accepts bracketed IPv6, and IPv4/hostname addresses are byte-identical to before.
**Approach (assumed at the header base):** replace `fmt.Sprintf("%s:%d", host, port)` with `net.JoinHostPort(host, strconv.Itoa(port))` at every producer: `(*RunningInstance).Addr`, `(discoveryTarget).addr`, the `discoveryTargets` dedupe key (discovery.go); `(*Config).ConfiguredBackendAddr` (config.go); `startManagedServer`, `connectExternalServer`, `EnsureServer`, `loadProfile` (server.go); `cmdUnload` (cli.go). `splitHostPort` (discovery.go) wraps `net.SplitHostPort`. Grep `%s:%d` in non-test Go to confirm none remain.
**Regression guard.** Build every address through one helper that trims surrounding `[]` from the host before `net.JoinHostPort`, so a config already holding `host: "[::1]"` yields `[::1]:8000`, not `[[::1]]:8000`. The leftover check is scoped to `Sprintf("%s:%d"` in `internal/launcher` (display `Printf`s in menu.go/cli.go and `cmd/release` file:line errors stay). `applyBackendFallbacks` (config.go, `strings.Cut(addr, ":")`) also parses through the new `splitHostPort`.
**Consumers to test:** `splitHostPort` callers (`discoveryTargets`, `StopInstance`, `EnsureServer`, `loadProfile`, `UnloadInstanceModel`); `"http://"+addr` URL builders in backend_*.go (a bracketed addr must build a valid URL — test via `url.Parse`); status/list display.
**Files:** internal/launcher/discovery.go; internal/launcher/config.go; internal/launcher/server.go; internal/launcher/cli.go; internal/launcher/discovery_test.go; internal/launcher/server_test.go; internal/launcher/config_test.go
**Read first:** internal/launcher/discovery.go — splitHostPort, (*RunningInstance).Addr, discoveryTargets; internal/launcher/config.go — ConfiguredBackendAddr, applyBackendFallbacks; internal/launcher/server.go — startManagedServer, loadProfile; internal/launcher/backend_http.go — probeAddr
**Closes:** ipv6-host-address-format (with item 7)
**Tests:** table tests for producers with hosts `127.0.0.1`, `localhost`, `::1`, `::`, `[::1]` (→ `[::1]:8000`); `applyBackendFallbacks` over a bracketed IPv6 addr; `splitHostPort` round-trip; an httptest server on `[::1]` (skip if IPv6 loopback unavailable) reached through `HealthCheck`.
**Acceptance:** `go test ./internal/launcher/ -run 'Addr|HostPort|Discovery|IPv6' -v`; `GOOS=windows go vet ./internal/launcher/`
**Commit:** `fix(net): bracket IPv6 hosts when building and parsing addresses`

## 7. Bracket IPv6 in the lsof listener filter — ✅ DONE (2026-10-13)

NOTES (2026-10-13): consequential edit — llama-launcher.TDD.md: made necessary by the bracketed lsof filter (§ stop step 1 now notes the IPv6 spelling)

**What:** fix — `findListeningPIDs` builds `-iTCP@host:port`; lsof needs `-iTCP@[::1]:port`. Depends on item 6.
**Goal:** `findListeningPIDs` passes lsof a bracketed host for IPv6 and the unchanged spelling for IPv4/hostnames.
**Approach (assumed at the header base):** build the filter as `"-iTCP@" + net.JoinHostPort(host, port)` in `findListeningPIDs` (server.go); extract the argv building into a small pure func so it is unit-testable without lsof.
**Files:** internal/launcher/server.go; internal/launcher/server_test.go
**Read first:** internal/launcher/server.go — findListeningPIDs, findListeningPID, addrHasListener; internal/launcher/server_test.go — TestParseListeningPIDs
**Closes:** ipv6-host-address-format
**Tests:** argv table for `127.0.0.1:8000`, `localhost:8000`, `[::1]:8000`.
**Acceptance:** `go test ./internal/launcher/ -run 'ListeningPID|Lsof' -v`
**Commit:** `fix(server): bracket IPv6 hosts in the lsof listener filter`

## 8. Parallelise identifyBackend health checks — ✅ DONE (2026-10-13)

NOTES (2026-10-13): pass 1 and pass 2 share one generic helper, `probeConcurrently` (server.go), using `sync.WaitGroup.Go`; pass 2 still runs only after pass 1 finds no healthy backend, as in the serial code.
NOTES (2026-10-13): the blackholed-address case uses fakes whose probes sleep `healthCheckTimeout` (three backends; bound 3×timeout vs 12s serial) rather than a real blackholed IP, so it does not depend on the network; the subtest takes ~4s.

**What:** perf — `identifyBackend` health-checks backends serially (2s timeout each).
**Goal:** `identifyBackend` returns exactly the same result as before for every input (sorted-name priority across the three passes is preserved), and a dead address costs about one `healthCheckTimeout`, not one per backend.
**Approach (assumed at the header base):** in `identifyBackend` (server.go), run pass 1 (`HealthCheck`) for all sorted `llmServers` names concurrently into a results slice indexed by sorted position, then scan in sorted order for the first nil and the first `ErrAuthFailed` from `configuredBackendsAt(addr)` — same rules as the serial loop. Pass 2 (`startingUp`) may run the same way. No shared mutable state besides the indexed slice; wait with `sync.WaitGroup`.
**Regression guard.** Pass 2 (`startingUp`) runs concurrently too, not "may": its probes also use `healthCheckTimeout`, so a dead address costs about one `healthCheckTimeout` per pass (two total). `identifyBackend` waits for every goroutine before returning. The new timing/priority test is a non-parallel top-level test registering fakes with `t.Cleanup(func() { delete(llmServers, name) })`, as `TestStopServerAt_StartingOccupant` does — `TestIdentifyBackend`'s parallel subtests iterate `llmServers`.
**Files:** internal/launcher/server.go; internal/launcher/server_test.go; llama-launcher.TDD.md
**Read first:** internal/launcher/server.go — identifyBackend, startingUp, addrHasListener; internal/launcher/discovery.go — configuredBackendsAt; internal/launcher/server_test.go — TestIdentifyBackend, TestIdentifyBackend_AuthPassNeedsConfiguredAddress, hookStopServer; internal/launcher/backend.go — healthCheckTimeout
**Closes:** identify-backend-serial-checks
**Docs:** TDD §6.5 notes the probes run concurrently with sorted-name priority.
**Tests:** existing `TestIdentifyBackend*` unchanged and passing; new case where a lower-sorted backend's probe is slow and a higher one fast but both match → the lower-sorted name still wins; elapsed-time bound (about two `healthCheckTimeout`s, one per pass) on a blackholed address with fake backends — both in a non-parallel top-level test that deletes its fakes in `t.Cleanup`.
**Acceptance:** `go test ./internal/launcher/ -run 'IdentifyBackend|StopInstance' -race -v`
**Commit:** `perf(server): run identifyBackend health checks concurrently`

## 9. Show a parameter diff before switching models — ✅ DONE (2026-10-13)

NOTES (2026-10-13): simple-mode tests cover the cancel paths ("1\nn\n", "1\n\n", "1\n") through doSwitchSimple; the "y" answer of a single piped "1\ny\n" is pinned at readLine (TestSwitchSimple_ReadLineKeepsPipedAnswer), since a confirmed switch would run a real doLoadProfile.
NOTES (2026-10-13): drawPopupAndWait keeps its signature; its frame rendering moved into a shared drawPopup that drawConfirmPopup also uses. Description is left out of the delta (the plan lists Backend, Model, specs, Extra args only).

**What:**
**Goal:** picking a profile in Switch model shows only the parameters that change (current → target) and loads only on confirmation, in both TTY and simple modes.
**Approach (assumed at the header base):** feature (ratified). After a profile is picked in `doSwitchModel`, show a popup titled with the target profile listing only lines that differ between the current and target profile — `label  old → new` — using the same labels and `spec.Format` values as `formatProfileParams` (Backend, Model, each `ParamSpecs()` entry, Extra args redacted). Enter loads (`doLoadProfile`), q/esc returns to the menu without loading. No differing lines → one line "No parameter changes". Current instance with no resolvable `ActiveProfile` → show the target's full `formatProfileParams` lines. `doSwitchSimple` prints the same lines then asks `Switch? [y/N]`.
**Approach (assumed at the header base):** one pure func `profileParamsDelta(cur, target *ResolvedProfile) []string` beside `formatProfileParams` in menu.go, sharing its label/format code (extract a helper returning label/value pairs; both funcs use it). Confirm popup reuses `showPopup`/`drawPopupAndWait` in ui.go.
**Regression guard.** `readLine` (menu.go) reads os.Stdin unbuffered up to `'\n'` instead of a fresh `bufio.Scanner` per call, so the confirm sees the `y` of piped `"1\ny\n"`. The TTY confirm is a new popup variant (footer `enter switch · q cancel`, wait returns the key, raw-mode failure = cancel); `drawPopupAndWait` keeps its signature so `TestDrawPopupAndWaitRestoresCursor` stays green. `doSwitchModel` passes `current *RunningInstance` into `doSwitchSimple`; the delta runs over the union of both backends' `ParamSpecs()`, a side with no value shown as `—`.
**Files:** internal/launcher/menu.go; internal/launcher/menu_test.go; internal/launcher/ui.go; internal/launcher/ui_test.go; README.md; llama-launcher.TDD.md
**Read first:** internal/launcher/menu.go — doSwitchModel, doSwitchSimple, formatProfileParams, readLine; internal/launcher/ui.go — drawPopupAndWait, waitForAnyKey; internal/launcher/menu_test.go — withStdin; internal/launcher/ui_test.go — TestDrawPopupAndWaitRestoresCursor
**Closes:** profile-switch-config-diff
**Tests:** `profileParamsDelta` table: identical, backend change, gpu_layers/context_size change, extra args redacted, nil current; `formatProfileParams` tests still pass; simple-mode y/N with `"1\ny\n"` fed in one `withStdin` call; a param only one backend has shows `—`; confirm popup raw-mode failure → cancel; `TestDrawPopupAndWaitRestoresCursor` still passes. New tests are named to match the Acceptance regex.
**Acceptance:** `go test ./internal/launcher/ -run 'ProfileParams|SwitchModel|SwitchSimple' -v`
**Commit:** `feat(menu): show parameter changes before switching models`

## 10. Shell completions subcommand — ✅ DONE (2026-10-13)

NOTES (2026-10-13): `completions` is dispatched twice in Run: before the `--config` filter (as the plan says) and again right after it, beside `version`, so `--config x completions bash` also prints the script without loading or creating a config. `__complete` is dispatched only before the filter, because the scripts always call it as the first word.
NOTES (2026-10-13): the `__complete` protocol takes the words after the program name with the word under the cursor last. It offers flags only when that word starts with `-` and positional values otherwise, and it prints nothing when the word is the path after `--config`, so the shell completes file names there (bash through `-o default`, zsh through `_files`, fish through `-l config -r -F`).
NOTES (2026-10-13): the README tells bash users to `eval "$(llama-launcher completions bash)"`, because `source <(…)` silently loads nothing in macOS bash 3.2 (checked by hand). The fish `--no-execute` syntax check skips here because fish is not on PATH.

**What:** Recast at the regression check (2026-10-13).
**Goal:** `llama-launcher completions bash|zsh|fish` prints a working completion script, and the hidden `__complete` subcommand returns live subcommand, flag, profile and backend candidates.
**Approach (assumed at the header base):** new `internal/launcher/completions.go`:
- `completions bash|zsh|fish` prints a script; unknown/missing shell → usage error, exit 2.
- hidden `__complete <words…>` prints one candidate per line: subcommands (`load unload start stop status list logs config version completions`); profile names (`cfg.ProfileNames()`) after `load`, `unload`, `start --profile|-p`; enabled backend names after `stop`, `logs`; `config` → `validate init reset`; `logs` also `clean`; flags per command as in `printUsage`. Config missing or invalid → omit profile/backend candidates only; subcommand and flag candidates still print; exit 0, nothing on stderr.
- Wire both in `Run` (cli.go); `completions` handled before config load, `__complete` loads config honoring `--config`/`LLAMA_LAUNCHER_CONFIG`. `__complete` is absent from `printUsage`; `completions` is listed.
**Regression guard.** `completions` is dispatched in Run before any config load or config-file creation, and never reads, creates or writes a config file (the Homebrew install sandbox runs it with a throwaway HOME); `__complete` never creates a config file either — a missing config yields only subcommand/flag candidates.
`__complete` loads the config without resolving keys — parse and validate only, never resolveKeyCommands or any api_key_cmd subprocess (a tab press must not run key commands); add a test that a config with an api_key_cmd writing a marker file leaves no marker after `__complete`; the missing-config test sets t.Setenv("LLAMA_LAUNCHER_CONFIG", "") or passes an explicit nonexistent --config; item 13 owns the §5.2 row for completions.go.
`__complete` and `completions` are dispatched in Run before the `--config` filter (cli.go:20-31); `__complete` scans its own words for `--config <path>` and ignores a trailing bare `--config` (no stderr, exit 0). The bash script uses only bash-3.2 builtins (`COMPREPLY=( $(compgen -W …) )`, no `mapfile`/`readarray`) — macOS /bin/bash is 3.2.57.
**Files:** internal/launcher/completions.go; internal/launcher/completions_test.go; internal/launcher/cli.go; README.md; llama-launcher.TDD.md
**Read first:** internal/launcher/cli.go — Run, printUsage; internal/launcher/config.go — parseConfig, validate, resolveKeyCommands, ProfileNames, IsServerEnabled;
internal/launcher/cli_test.go — TestRun_ExitCodes
**Closes:** shell-completions (with item 11)
**Docs:** README usage section: install lines for each shell; TDD CLI section; delete the TDD §11 Future Considerations "Shell completions" bullet.
**Tests:** `__complete` table over a fixture config; each script contains `__complete` and parses — `bash -n`, `zsh -n`, `fish -n` when the shell is on PATH (skip otherwise); `completions` and `__complete` with a missing config under a temp HOME create no file, and `__complete` then prints only subcommand/flag candidates (test sets `t.Setenv("LLAMA_LAUNCHER_CONFIG", "")` or an explicit nonexistent `--config`); an `api_key_cmd` that writes a marker file leaves no marker after `__complete`; `__complete … --config` (trailing bare flag) exits 0 with empty stderr; the bash script contains no `mapfile`/`readarray`.
**Acceptance:** `go test ./internal/launcher/ -run 'Complet' -v`; `go build -o "$TMPDIR/ll" . && set -o pipefail && "$TMPDIR/ll" completions bash | bash -n`
**Commit:** `feat(cli): add shell completions for bash, zsh and fish`

## 11. Install completions from the Homebrew formula — ✅ DONE (2026-10-13)

**What:** Depends on item 10. Add `generate_completions_from_executable(bin/"llama-launcher", "completions")` to the `install` block of `cmd/release/templates/llama-launcher.rb.tmpl`; update the rendered fixture `cmd/release/testdata/llama-launcher-0.0.0.rb`.
**Goal:** the rendered formula installs bash, zsh and fish completions generated by `llama-launcher completions <shell>`.
**Approach (assumed at the header base):** template edit only; `render.go` unchanged.
**Regression guard.** README.md (Homebrew install paragraph) and `llama-launcher.TDD.md` §13 (formula paragraph) each gain one sentence that the formula also installs bash, zsh and fish completions.
**Files:** cmd/release/templates/llama-launcher.rb.tmpl; cmd/release/testdata/llama-launcher-0.0.0.rb; README.md; llama-launcher.TDD.md
**Read first:** cmd/release/templates/llama-launcher.rb.tmpl — install block; cmd/release/render_test.go — TestRenderFormulaMatchesTheGoldenFormula, goldenSums; cmd/release/render.go — renderFormula; README.md — Homebrew install paragraph; llama-launcher.TDD.md — §13 formula paragraph
**Closes:** shell-completions
**Tests:** existing render golden test against the updated fixture.
**Acceptance:** `go test ./cmd/release/ -v -run Render`
**Commit:** `build(release): install shell completions from the Homebrew formula`

## 12. README: Requirements section and api_key_cmd note — ✅ DONE (2026-10-13)

NOTES (2026-10-13): Requirements table also lists `ioreg` (macOS GPU fields of the memory readout) and the `llama-launcher` binary the MCP adapter runs — both found by the exec.Command/LookPath grep the regression guard prescribes.

**What:**
**Goal:** README has a `## Requirements` section listing every runtime host binary the code executes, and the `### API keys` section states that `api_key_cmd` has the same per-backend effect as `api_key`.
**Approach (assumed at the header base):** docs (ratified). Add `## Requirements` near the install section: Go 1.26+ for source builds; a table of runtime host binaries — `lsof` (stop, port-conflict check), `ps`, `tail` (logs), `sysctl`/`vm_stat` (macOS memory readout), `security` / `secret-tool` (keystore), `sh` (api_key_cmd), the backend binaries (`llama-server`, `ollama`, `lms`, `splash`) — each with OS and what fails without it; note unix process control is unavailable on Windows. Under the `### API keys` per-backend table add one sentence: `api_key_cmd` has the same effect as `api_key` (its output is the key). Verify each binary against the code (`exec.Command`/`exec.LookPath` grep) before listing it.
**Regression guard.** The table lists every program name run through `exec.Command`/`exec.LookPath`/`runMemCmd`/the keystore under `internal/` and `cmd/llama-launcher-mcp` (`grep -rn 'exec.Command\|exec.LookPath\|runMemCmd' internal cmd/llama-launcher-mcp`) — which adds `open` (macOS Edit config), `$VISUAL`/`$EDITOR`, and `cmd /C` (Windows `api_key_cmd`) to the Approach list. Acceptance greps inside the new text, since `lsof` and `api_key_cmd` already match README at base.
**Files:** README.md
**Read first:** README.md — Install / From source, API keys table; internal/launcher/server.go — lsof, ps, tail calls; internal/launcher/sysmem.go — runMemCmd; internal/launcher/config.go — keyCommandArgv; internal/keystore/keystore.go — keychainProgram, secretServiceProgram; internal/launcher/menu.go — editConfigCommand
**Closes:** readme-requirements-host-binaries; readme-api-key-cmd
**Tests:** none (docs).
**Acceptance:** `grep -n '^## Requirements' README.md`; `awk '/^## Requirements/,/^## [^R]/' README.md | grep -c 'lsof\|open\|secret-tool'` is at least 3; `grep -n 'api_key_cmd.*same effect as.*api_key' README.md`
**Commit:** `docs(readme): add Requirements section and api_key_cmd note`

## 13. TDD §5.2 Source Files: missing rows — ✅ DONE (2026-10-13)

NOTES (2026-10-13): `cli.go`, `cmd/llama-launcher-mcp/main.go` and `config.go` already matched the Acceptance check (by basename via the `main.go` row and `cmd/release/main.go`), so no separate `cli.go` row was added; the grouped MCP row still names all four adapter files as the plan asks.

**What:**
**Goal:** every non-test `.go` file in the repo appears in TDD `### 5.2 Source Files`.
**Approach (assumed at the header base):** add rows to `### 5.2 Source Files` in `llama-launcher.TDD.md` for every non-test Go file not listed, in the table's existing style: `cli.go`, `frame.go`, `completions.go` (item 10), `internal/keystore/keystore.go`, `internal/keystore/run.go`, `cmd/llama-launcher-mcp/main.go` / `config.go` / `validate.go` / `allowlist.go` (one grouped row pointing at §15). Rule: after the edit, every non-test `.go` file under the repo appears in the table — check with the Acceptance loop. Depends on item 10.
**Regression guard.** The Acceptance check reads only §5.2 and matches the full path or the backticked basename: a whole-TDD basename grep passes files absent from §5.2 vacuously (`cmd/llama-launcher-mcp/validate.go` is named only in §5.3).
**Files:** llama-launcher.TDD.md
**Read first:** llama-launcher.TDD.md — §5.2 Source Files table (main.go row, cmd/release rows), §15 MCP adapter; internal/keystore/keystore.go; internal/keystore/run.go; cmd/llama-launcher-mcp/allowlist.go, validate.go; internal/launcher/frame.go
**Closes:** tdd-file-map-gaps; tdd-keystore-rows-missing
**Tests:** none (docs).
**Acceptance:** `S=$(awk '/^### 5.2 Source Files/{p=1;next} p&&/^##/{exit} p' llama-launcher.TDD.md); for f in $(git ls-files '*.go' | grep -v _test.go); do b=$(basename $f); printf '%s' "$S" | grep -qF -e "\`$b\`" -e "/$b\`" -e "$f" || echo MISSING $f; done` prints nothing
**Commit:** `docs(tdd): list every source file in the 5.2 file map`
