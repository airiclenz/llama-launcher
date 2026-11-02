# Plan: The CLI keeps waiting for a managed server after a startup stall

**Goal:** `llama-launcher load` (and every path that runs through it: `start --profile`, the non-terminal menu, MCP `load_profile`) no longer reports a failure for a managed server that stalls past `startup_stall_timeout` and then turns healthy. After a managed startup timeout the CLI keeps watching the server it left running, like the menu's still-loading popup, bounded by `startup_max_wait` from the start of the load.
**Date:** 2026-11-02
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** eb6fd5c

**Regression check (2026-11-02, eb6fd5c):**
- 1: guard folded (named CLI-watch builder with injected probes, `now` and `sleep`; test pins the 10 s cadence)
- 2: guard folded (widened docs grep incl. `cmd/llama-launcher-mcp/main.go`; waited success sets `ActiveProfile`; waiter skipped once `startup_max_wait` already elapsed)

**Sources:** capcom issue `cli-load-false-timeout`; `internal/launcher/menu.go` (`doLoadProfile`, `routeLoadError`, `keepWaitingForLoad`, `waitStillLoading`, `stillLoadingWatch`); `internal/launcher/cli.go` (`cmdLoad`); `internal/launcher/server.go` (`startupTimeout`, `startupTimeoutErr`, `waitForStartup`); ADR-0010 (`capcom know show adr-0010`); `llama-launcher.TDD.md` §3.2, §6.2 step 9, §error table; `docs/plans/archived/2026-09-24 - 04 - progress-aware-startup-wait-plan.md`

**Ratified design calls** (user, 2026-11-02):
- **Wait bound:** after a managed startup timeout the CLI keeps polling while the server is healthy-pending (Starting or its PID alive), until healthy (exit 0), gone (exit 3) or `startup_max_wait` measured from the start of the load (exit 3).
- **Layer:** CLI layer only. The library `LoadProfile` still returns `ErrStartupTimeout` at the stall; `waitForStartup` and `startup_stall_timeout` semantics are unchanged.
- **External servers:** the Ollama / LM Studio auto-start (15 s `externalStartWait`) is out of scope; its timeout still exits 3 without waiting.
- **Output** (writer, 2026-11-02): the still-loading notice uses the CLI progress style on stdout (`newCLIProgress`): one `    Still loading <profile>… m:ss` line when the extra wait begins and one every 10 s after; no raw mode, no cursor control. Ctrl+C is plain SIGINT: the CLI exits and the detached server keeps loading.
- **Cap message** (writer, 2026-11-02): a wait that reaches `startup_max_wait` returns the original timeout error plus the line `Still not healthy after <max> (startup_max_wait).`; a gone server keeps `stillLoadingGoneText`.

**Standing requirements:**
- skills: coding-standards. `make cross` stays green.
- Each item updates the TDD/README/comment lines its own change invalidates; its CHANGELOG entry travels in its sidecar.
- Follow-ups become capcom issues with spoken ids (`capcom create --id <slug>`), never a NOTES line alone.

**Out of scope:**
- External (Ollama/LM Studio) auto-start wait and `start <server>` (`EnsureServer`).
- The library facade (`launcher/`), `waitForStartup`, the config keys and their defaults.
- The terminal menu's popup behaviour (Esc, unbounded wait) beyond the shared refactor in item 1.
- ADR changes: ADR-0010's "a start timeout leaves the server running" still holds.

## 1. A terminal-free still-loading wait with a deadline — ✅ DONE (2026-11-02)

NOTES (2026-11-02): consequential edit — llama-launcher.TDD.md: made necessary by the `stillLoadingWatch` clock/deadline fields, `probeTimedOutServer` and `waitStillLoadingCLI`/`cliStillLoadingWatch` (the `menu.go` row's description of the watch, and the `menu_test.go` row's enumeration of the `TestWaitStillLoading_*` cases); the rows' "in terminal mode" wording stays for item 2.
NOTES (2026-11-02): `stillLoadingWatch` gains `now` and `maxWait` beside `deadline`: `now` is the clock the deadline is checked against (the CLI test drives it with a fake clock), `maxWait` names the cap in the `Still not healthy after <max> (startup_max_wait).` line (the `stillLoadingDeadlineText` constant). `scriptedWatch` sets `now: time.Now`. The builder takes `maxWait` as well as the guard's named parameters, because it sets the deadline to `loadStart + maxWait`.
NOTES (2026-11-02): `waitStillLoadingCLI` has no production caller yet. Item 2 wires it into `cmdLoad` and the non-terminal `doLoadProfile`.

**What:**
**Goal:** package `internal/launcher` has one still-loading poll loop shared by the TUI popup and a new CLI waiter. The CLI waiter, given a managed `startupTimeout`, its timeout error, the profile's backend, the load's start time and `cfg.StartupMaxWait()`, polls once per `stillLoadingPollInterval` and returns `(true, nil)` when the server turns healthy, `(false, <timeout text + stillLoadingGoneText>)` after `stillLoadingGoneStreak` gone polls, and `(false, <timeout text + "Still not healthy after <max> (startup_max_wait).">)` once load start + max has passed. It prints the still-loading line in CLI progress style when it begins and every 10 s, and never touches raw mode. The TUI popup's behaviour (no deadline, Esc/Ctrl+C/q return, raw mode) is unchanged.
**Regression guard.** The CLI watch comes from one named builder taking the probe trio (`healthy`, `starting`, `alive`), `displayName`, `loadStart`, `now func() time.Time` and `sleep func(time.Duration)`; production passes the shared probe helper's probes, `time.Now` and `time.Sleep`. Its `readKey` calls `sleep` and its 10 s draw throttle reads `now()`, so a test drives the cadence with a fake clock. The test pins exactly one line at start plus one per 10 s elapsed.
**Approach (assumed at the header base):** extends `menu.go`; no new file. `stillLoadingWatch` gains a `deadline time.Time` (zero = none) checked after the healthy check in `waitStillLoading`, returning the cap error. Extract the healthy/starting/alive probe construction in `keepWaitingForLoad` into one helper that both waiters call (one reason to change: how a timed-out server is observed). The CLI waiter builds its watch with `enterRaw` a no-op, `readKey` sleeping the interval and returning `keyNone`, and `draw` throttled to the 10 s cadence using `formatElapsed`. The extra-wait loop must not be a second copy of `waitStillLoading`.
**Files:** internal/launcher/menu.go, internal/launcher/menu_test.go
**Read first:** internal/launcher/menu.go — waitStillLoading, stillLoadingWatch, keepWaitingForLoad; internal/launcher/menu_test.go — scriptedWatch, TestWaitStillLoading_HealthyOnThirdPoll;
internal/launcher/progress.go — newCLIProgress, formatElapsed; internal/launcher/cli_test.go — captureStdout

**Tests:**
- `TestWaitStillLoading_DeadlineEndsWait`: a scripted watch that stays starting and a deadline already passed → `(false, err)` whose text holds the timeout text and `startup_max_wait`.
- `TestWaitStillLoading_HealthyBeatsDeadline`: healthy on the first poll with a passed deadline → `(true, nil)`.
- `TestWaitStillLoadingCLI_PrintsProgress` (stdout captured with `captureStdout`): a CLI watch from the builder with scripted probes and a fake clock that `sleep` advances, staying starting until the clock is past 10 s and then healthy → output holds exactly two `Still loading` lines (one at start, one at 10 s) and no escape sequences (`\033`).
- Existing `TestWaitStillLoading_*` and `TestDoLoadProfile_RoutesStartupTimeout` stay green unchanged.

**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test -race ./internal/launcher/ -run 'TestWaitStillLoading|TestDoLoadProfile' -count=1`

**Commit:** `feat(launcher): add a terminal-free still-loading wait with a deadline`

## 2. cmdLoad and the non-terminal menu keep waiting after a managed startup timeout — ✅ DONE (2026-11-02)

NOTES (2026-11-02): `cmdLoad`'s post-`LoadProfile` handling is `cmdLoadFinish(cfg, profile, inst, started, err, loadStart, wait cliLoadWaiter)`. `cliLoadWaiter` is `func(timeoutErr error, st startupTimeout) (bool, error)`, and production gets it from `newCLILoadWaiter`, which wraps `waitStillLoadingCLI`. Both `cmdLoadFinish` and the non-terminal `doLoadProfile` call one shared `keepWaitingCLI`. That function skips the wait once `startup_max_wait` has elapsed (guard c) and builds the waited `RunningInstance` via `timedOutInstance`, with `ActiveProfile` set (guard b). `keepWaitingForLoad` now uses `timedOutInstance` too, so the code that builds the instance exists only once.
NOTES (2026-11-02): `doLoadProfile` picks its waiter by `isTerminal()`: the popup's `keepWaitingForLoad`, or `keepWaitingCLI` followed by `printLoadSuccess`. `routeLoadError` no longer takes the `terminal` parameter. `TestDoLoadProfile_RoutesStartupTimeout` now has three cases: managed waits, external is unchanged, any other error is unchanged.
NOTES (2026-11-02): docs updated beyond the named rows: the TDD §3.1 menu paragraph (the non-terminal case), the TDD config-example comment for `startup_stall_timeout`/`startup_max_wait`, and the TDD `cli_test.go` row (the new `TestCmdLoadFinish_*` tests). The library facade lines (TDD §16.2, README library caveat) describe `LoadProfile`, whose behaviour is unchanged, so they were left as they are.

**What:**
**Goal:** fixes capcom bug `cli-load-false-timeout`. `llama-launcher load <profile>` whose `LoadProfile` returns a managed `startupTimeout` (address and PID set) runs item 1's CLI waiter: healthy → the normal success lines (`Server started (PID n)`, `Loaded <profile> on <host>:<port>`) and exit 0; otherwise `Error: <waiter error>` on stderr and exit 3. Every other error, the external auto-start timeout included, exits 3 at once as before. The menu's `doLoadProfile` without a terminal routes a managed timeout to the same CLI waiter and prints `printLoadSuccess` on success; with a terminal it still opens the popup. The TDD (§3.2 `load` row, §6.2 step 9, the "Server health timeout" error-table row) and README (the still-loading paragraph) describe the CLI behaviour.
**Regression guard.** (a) Docs: fix every line saying only the terminal/menu keeps waiting, or that `load` waits only while the server makes startup progress — incl. the TDD `menu.go` / `menu_test.go` rows (`routeLoadError` "in terminal mode", "terminal-mode managed timeout"), the MCP `WriteTimeout` paragraph and the `WriteTimeout` comment in `cmd/llama-launcher-mcp/main.go`. (b) The waited-success path builds its `RunningInstance` from the profile's Host/Port, `st.pid` and `ActiveProfile = profile.Name`, so `cmdLoad`'s `Loaded …` line prints. (c) When `time.Since(loadStart) >= cfg.StartupMaxWait()` at the timeout (`waitForStartup`'s own total cap fired), skip the waiter and return the error unchanged (exit 3).
Depends on item 1.
**Approach (assumed at the header base):** record `loadStart` before `LoadProfile` in `cmdLoad`. Move `cmdLoad`'s post-`LoadProfile` handling into a function that takes the waiter as a parameter, so a test drives it with a scripted waiter and no server. `routeLoadError` loses its `terminal` parameter: it decides only "managed timeout or not"; `doLoadProfile` picks the popup waiter or the CLI waiter by `isTerminal()`. Update `TestDoLoadProfile_RoutesStartupTimeout` to the new signature (managed → waits, external → unchanged, other errors → unchanged).
Docs rule: every TDD/README/MCP-comment line saying a CLI `load` health timeout exits 3, that only the terminal/menu keeps waiting, or that `load` waits only while the server makes startup progress; find them with `grep -n "Still loading\|health timeout\|left running\|exit 3\|routeLoadError\|terminal-mode\|startup progress" llama-launcher.TDD.md README.md cmd/llama-launcher-mcp/main.go`.
**Files:** internal/launcher/cli.go, internal/launcher/cli_test.go, internal/launcher/menu.go, internal/launcher/menu_test.go, llama-launcher.TDD.md, README.md, cmd/llama-launcher-mcp/main.go
**Read first:** internal/launcher/cli.go — cmdLoad; internal/launcher/menu.go — doLoadProfile, routeLoadError, keepWaitingForLoad;
internal/launcher/server.go — startupTimeoutErr, externalStartupTimeoutErr, waitForStartup; internal/launcher/menu_test.go — TestDoLoadProfile_RoutesStartupTimeout
**Closes:** cli-load-false-timeout

**Tests:**
- `TestCmdLoadFinish_ManagedTimeoutThenHealthy`: a `startupTimeoutErr` (nil instance, as `LoadProfile` returns) and a waiter returning `(true, nil)` → exit 0, stdout holds `Loaded <profile> on <host>:<port>`. Bite: fails on the pre-item tree, which exits 3.
- `TestCmdLoadFinish_MaxWaitAlreadyElapsedDoesNotWait`: a `startupTimeoutErr` with `loadStart` older than `StartupMaxWait()` → exit 3, stderr holds the original error without a `startup_max_wait` line, waiter never called.
- `TestCmdLoadFinish_ManagedTimeoutGone`: waiter returns the gone error → exit 3, stderr holds `no longer running`.
- `TestCmdLoadFinish_ExternalTimeoutDoesNotWait`: an `externalStartupTimeoutErr` → exit 3, waiter never called.
- `TestDoLoadProfile_RoutesStartupTimeout` updated as above.

**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test -race ./internal/launcher/ -run 'TestCmdLoad|TestDoLoadProfile|TestWaitStillLoading' -count=1`
- `grep -n "startup_max_wait" llama-launcher.TDD.md README.md`

**Commit:** `fix(cli): keep waiting for a managed server after a startup stall`
