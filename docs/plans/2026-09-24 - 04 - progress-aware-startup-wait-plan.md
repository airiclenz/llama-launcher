# Plan: Progress-aware startup wait and a menu that keeps waiting

**Goal:** A managed server's startup wait (`load` and `start`) runs as long as the spawned server keeps making progress. It gives up only after a configurable stall window or a configurable hard cap. The TUI shows elapsed time while it waits and, on a startup timeout, keeps polling in a "still loading" popup instead of showing an error.
**Date:** 2026-09-24
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** 0d68230

**Sources:** `internal/launcher/server.go` (`loadProfileManaged`, `EnsureServer`, `waitForHealth`, `startupTimeoutErr`); `docs/adr/0010-starting-instances-are-visible-and-stoppable.md`, `docs/adr/0015-a-loading-splash-is-found-by-its-process.md`; `llama-launcher.TDD.md` §6; `CONTEXT.md`

**Ratified design calls** (user, 2026-09-24):
- **Wait model:** keep waiting while the process is alive AND making progress; stall window default 30 s, hard cap default 10 min.
- **Progress signal:** the server's log file grew since the last poll, OR the backend's `StartupProber.StartingUp(addr)` answers true (llamacpp `/health` 503, Splash `/ready` 503). A Splash `LoadingPID` hit alone is not progress.
- **Scope:** managed servers on `load` (`loadProfileManaged`). A managed `start` is covered because `start --profile` goes through `load`; `EnsureServer` is untouched. The external auto-start (`connectExternalServer`, `externalStartWait` 15 s) is unchanged.
- **Configurable:** global keys `startup_stall_timeout` and `startup_max_wait`, integer seconds like `refresh_duration`.
- **Menu on `ErrStartupTimeout`:** a popup that keeps polling. Healthy → normal success output; server gone → error popup; Esc → back to the menu with the server left loading.
- **Elapsed time:** the TUI "Waiting for server" line ticks elapsed time every second.
- **Limits** (writer, 2026-09-24): max_wait = clamp(raw max or 600 s, 5 s, 3600 s), then stall = clamp(raw stall or 30 s, 5 s, max_wait). The MCP `WriteTimeout` becomes 65 min so no configured cap outlasts it.
- **Elapsed-time owner** (writer, 2026-09-24): the TUI `progressTracker` owns the ticker. `ProgressFunc`, the CLI progress output and the facade are unchanged.

**Standing requirements:**
- skills: coding-standards. `make cross` stays green.
- Each item updates the TDD/README/doc.go/comment lines its own change invalidates. Its CHANGELOG entry travels in its sidecar.
- Follow-ups become beads with spoken ids (`bd create --id llama-launcher-<slug>`), never a NOTES line alone.

**Out of scope:**
- External (Ollama/LM Studio) auto-start wait; per-profile timeout overrides.
- CLI `load`/`start` output and exit codes (still exit 3 on timeout); a cancel key during the in-`LoadProfile` wait.
- ADR changes: ADR-0010's "a start timeout leaves the server running" still holds.

**Regression check (2026-09-24, 0d68230):**
- 1: guard folded (explicit clamp order, defaults/config.yaml documents both keys, no separate TDD key reference)
- 2: guard folded (prober-silent log-growth test, waitForHealth docs rule, keyed external `startupTimeout` literal); yields to ADR-0011 decision 2
- 3: dropped — writer verified cli.go cmdStart routes `start --profile` to cmdLoad and a managed start without a profile fails fast before EnsureServer, so item 2 already covers `start`; EnsureServer's managed arm is unreachable from the CLI and stays untouched
- 4: guard folded (lands in the same run as item 2; Acceptance greps the old phrase and the new value)
- 5: guard folded (named seams, blank the previous rect when the popup narrows)
- 6: guard folded (managed-only popup entry per the writer's decision, raw mode with Ctrl+C/q as Esc, post-`LoadProfile` branching helper)
- 1 (second pass): header **Limits** line rewritten to the guard's clamp order; the guard's replaces-the-header sentence dropped
- 2 (second pass): guard extended — TDD:693 "up to 15 seconds on `start`" corrected, since a managed `start --profile` runs through `load`

## 1. Global config keys for the startup stall window and hard cap — ✅ DONE (2026-09-24)

NOTES (2026-09-24): the YAML fields are named `StartupStallSecs` / `StartupMaxWaitSecs` because Go forbids a field and a method sharing the name `StartupMaxWait`; clamping happens in whole seconds before the `time.Duration` conversion so an oversized value cannot overflow.
NOTES (2026-09-24): the YAML round-trip is a subtest of `TestStartupWaitAccessors` rather than a new `TestLoadConfig` case; the keys are parsed and documented here but read by nothing until item 2 wires them into the load wait.

**What:**
**Goal:** `Config` parses `startup_stall_timeout` and `startup_max_wait` (integer seconds). `Config.StartupStallTimeout()` returns 30 s by default and `Config.StartupMaxWait()` returns 10 min by default. max_wait = clamp(raw max or 600 s, 5 s, 3600 s); then stall = clamp(raw stall or 30 s, 5 s, max_wait). The keys are documented.
**Approach (assumed at the header base):** add two `*int` fields beside `RefreshDuration` in `Config` (config.go). Add two accessors modelled on `MenuRefreshInterval`. Document both keys in the TDD sample config block (near the `# refresh_duration: 10` line). Add them to README's "Other top-level options" sentence.
**Regression guard.** The clamp rule is exactly: max_wait = clamp(raw max or 600 s, 5 s, 3600 s), then stall = clamp(raw stall or 30 s, 5 s, max_wait).
`internal/launcher/defaults/config.yaml` (README.md:53 calls it the complete reference) gets a commented block for each key beside `# refresh_duration: 10`.
The TDD has no separate config-key reference: the §4.2 sample block is that reference.
**Files:** internal/launcher/config.go, internal/launcher/config_test.go, internal/launcher/defaults/config.yaml, llama-launcher.TDD.md, README.md
**Read first:** internal/launcher/config.go — Config, MenuRefreshInterval; internal/launcher/config_test.go — TestMenuRefreshInterval, TestLoadConfig; internal/launcher/defaults/config.yaml — refresh_duration block;
llama-launcher.TDD.md — §4.2 Schema sample block (`# refresh_duration: 10`); README.md — "Other top-level options" sentence

**Tests:**
- `TestStartupWaitAccessors` (config_test.go, table): unset → 30 s / 10 min; 60/1200 → 60 s / 20 min; stall 1 → 5 s; stall 900 with max 600 → 600 s; max 99999 → 3600 s; max 2 with stall unset → max 5 s, stall 5 s. Also one YAML round-trip through `LoadConfig` that sets both keys.

**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestStartupWaitAccessors|TestLoadConfig' -count=1`
- `grep -n "startup_stall_timeout" llama-launcher.TDD.md README.md internal/launcher/defaults/config.yaml`

**Commit:** `feat(config): add startup_stall_timeout and startup_max_wait`

## 2. The load path waits while the server makes progress

**What:**
**Goal:** `LoadProfile` on a managed backend waits until the server is healthy. It returns an error wrapping `ErrStartupTimeout` only when no progress was seen for `cfg.StartupStallTimeout()` or when `cfg.StartupMaxWait()` has passed. Progress is log-file growth or `StartingUp(addr)` true. An exit during the wait still ends it early with the `serverExitErr` classification. The timeout error still names the PID and log path, and it carries them (plus the address) so callers inside the package can read them with `errors.As`.
Depends on item 1.
**Approach (assumed at the header base):** add `waitForStartup(b, addr, logFile string, stall, max time.Duration, exited <-chan struct{}) error` in server.go beside `waitForHealth`, polling at `healthPollInterval`. Progress uses `os.Stat(logFile).Size()` compared with the previous poll (an empty or unreadable log counts as no growth) and `startingUp(b, addr)` restricted to the `StartupProber` answer. Stall message: `server at <addr> did not become healthy: no startup progress for <stall>`. Cap message: `server at <addr> did not become healthy within <max>`. `activationOps.waitHealthy` takes the log path and both durations instead of the single timeout. Update `realOps`, `fakeOps`, `realWaitOps` and `exitingStartOps` in server_test.go. `loadProfileManaged` passes `inst.LogFile`, `cfg.StartupStallTimeout()` and `cfg.StartupMaxWait()`. `startupTimeout` gains unexported `addr`, `pid` and `logFile` fields, set by `startupTimeoutErr` only. Its `Error()` text and `Unwrap` are unchanged.
Docs: every line stating the load window ("30 on `load`", "~30 s", "~30 seconds", "within 30s"). Find them with `grep -rn "30 s\|30s\|30 seconds\|30 on" llama-launcher.TDD.md README.md launcher/ internal/launcher/server.go`. ADR text stays.
**Regression guard.** `externalStartupTimeoutErr`'s positional literal (server.go:1229) becomes the keyed `startupTimeout{decorated: …}` and leaves the new fields zero.
Docs are also a rule: every doc or comment naming `waitForHealth`/`WaitForHealth` as the load's wait (TDD:484 server.go row, §16.2 at TDD:1147, server.go:55-57, server.go:694-696); find them with `grep -rn "waitForHealth\|WaitForHealth" llama-launcher.TDD.md internal/launcher/server.go launcher/`. `WaitForHealth` stays documented for its other callers.
The TDD §6.2 step 9 line "up to 15 seconds on `start`, 30 on `load`" (TDD:693) is corrected too: a managed `start --profile` runs through `load` and inherits the progress-aware wait, so the line states that wait for `load` and for `start --profile` alike.
Yields to ADR-0011 decision 2 (docs/adr/0011-public-library-facade.md:26-28, "up to ~30 s of health wait"): its rule (the facade documents the worst case; clients call from a goroutine) holds and its text stays unedited; the facade docs state the new worst case under it.
**Files:** internal/launcher/server.go, internal/launcher/server_test.go, llama-launcher.TDD.md, README.md, launcher/doc.go, launcher/launcher.go
**Read first:** internal/launcher/server.go — loadProfileManaged, waitForHealth, activationOps.waitHealthy, realOps.waitHealthy, startupTimeoutErr, startupTimeout, externalStartupTimeoutErr, startingUp; internal/launcher/backend_llamacpp.go — LlamaCpp.StartingUp;
internal/launcher/server_test.go — fakeOps.waitHealthy, realWaitOps, exitingStartOps, TestLoadProfile_StartupTimeoutIsErrStartupTimeout, TestLoadProfile_ServerExitMidWaitEndsTheLoad, TestStartupTimeoutErr_ManagedMessageUnchanged;
llama-launcher.TDD.md — §6.2 step 9, §16.2 verbs block, server.go module row, TestLoadProfile_StartupTimeoutIsErrStartupTimeout row; launcher/launcher.go — LoadProfile doc; launcher/doc.go — blocking-verbs paragraph

**Tests:**
- `TestWaitForStartup_LogGrowthKeepsWaiting`: a stall of 1 s and a max of 5 s, a log appended every 300 ms, and health going OK at about 2.5 s → nil (bite: the fixed-window wait fails at 1 s). The `StartupProber` stays silent: the backend is not a `StartupProber`, or the listener answers 500 or refuses until about 2.5 s, so only the log growth keeps the wait alive.
- `TestWaitForStartup_StallTimesOut`: a static log and no prober → a "no startup progress for" error within about stall + a poll interval.
- `TestWaitForStartup_StartingUpIsProgress`: an httptest 503 llamacpp listener with a static log reaches the cap, not the stall (message "within").
- `TestWaitForStartup_ExitEndsWait`: a closed `exited` gives `errServerExited`.
- `TestLoadProfile_StartupTimeoutIsErrStartupTimeout` and `TestLoadProfile_ServerExitMidWaitEndsTheLoad` keep their assertions on the new seam. `TestStartupTimeoutErr_ManagedMessageUnchanged` keeps its exact text. Add an `errors.As` check that reads PID, log and addr.

**Acceptance:**
- `go build ./... && go vet ./internal/launcher/ ./launcher/`
- `go test -race ./internal/launcher/ -run 'TestWaitFor|TestLoadProfile|TestStartupTimeout|TestLoadCanceled|TestConnectExternal' -count=1`
- `go test ./launcher/ -count=1`

**Commit:** `feat(launcher): wait for a managed server while it makes startup progress`

## 4. MCP write timeout outlasts the largest startup cap

**What:**
**Goal:** the MCP server's `http.Server.WriteTimeout` is 65 minutes. Its comment and the TDD's MCP section state that `load_profile` can wait up to the configured `startup_max_wait` (at most 60 min) and no longer say "up to 5 minutes".
Depends on item 1.
**Approach (assumed at the header base):** change `WriteTimeout` in `cmd/llama-launcher-mcp/main.go` and rewrite the comment above it. Update the TDD line that says "up to 5 minutes". Find the sites with `grep -rn "5 minutes\|WriteTimeout" cmd/llama-launcher-mcp llama-launcher.TDD.md`.
**Regression guard.** Item 4 must land in the same run as item 2 — after item 2 the default 10 min cap plus a restart's stop can exceed the 10 min MCP WriteTimeout until item 4 raises it; an executor must not stop the run between items 2 and 4.
Acceptance greps the old phrase only (`up to 5 minutes`, since "65 minutes" matches "5 minutes") and checks the new value and key name.
**Files:** cmd/llama-launcher-mcp/main.go, llama-launcher.TDD.md
**Read first:** cmd/llama-launcher-mcp/main.go — main (http.Server WriteTimeout and its comment), newServer (load_profile, start_server); cmd/llama-launcher-mcp/config.go — config.run, maxInFlight;
llama-launcher.TDD.md — §15 connection-timeouts paragraph

**Tests:**
- none new (a constant). The existing MCP tests stay green.

**Acceptance:**
- `go build ./... && go vet ./cmd/llama-launcher-mcp/ && go test ./cmd/llama-launcher-mcp/ -count=1`
- `! grep -rn "up to 5 minutes" cmd/llama-launcher-mcp llama-launcher.TDD.md`
- `grep -n "WriteTimeout: *65 \* time.Minute" cmd/llama-launcher-mcp/main.go`
- `grep -n startup_max_wait cmd/llama-launcher-mcp/main.go llama-launcher.TDD.md`

**Commit:** `fix(mcp): raise the write timeout above the startup wait cap`

## 5. TUI progress popup ticks elapsed time on the active step

**What:**
**Goal:** while a TUI progress popup is open, its active (last) step line shows the elapsed time for that step as `▸ Waiting for server... 1:07`, redrawn every second. The CLI progress output, `ProgressFunc` and the facade are unchanged. The ticker stops when the popup is closed, with no redraw after close.
**Approach (assumed at the header base):** `progressTracker` (progress.go) gains a mutex, the start time of the active step, a 1 s ticker goroutine started by `newTUIProgress`, and a `Close()` that stops it and waits for it to exit. `render` appends ` m:ss` to the active step once it has run for ≥ 1 s. `doLoadProfile` (menu.go) and every other `newTUIProgress` caller calls `Close()` before clearing the screen. Find the callers with `grep -rn "newTUIProgress" internal/launcher`. The TDD `progress.go` file row names the ticker.
**Regression guard.** The seams are named: `progressTracker` gains a `now func() time.Time` field (the injected clock). `render` writes straight to `os.Stdout`, so tests either use `captureStdout` (cli_test.go) and create and `Close()` the tracker inside its fn (not parallel), or the tracker gains an `io.Writer` field.
`render` blanks the previous rect when the popup narrows as well as when it loses rows (progress.go:106 blanks on a row shrink only): track the previous width/startCol, or reserve the suffix width from the first render, so no stale border columns stay.
**Files:** internal/launcher/progress.go, internal/launcher/menu.go, internal/launcher/progress_test.go, llama-launcher.TDD.md
**Read first:** internal/launcher/progress.go — progressTracker, newTUIProgress, render; internal/launcher/menu.go — doLoadProfile; internal/launcher/frame.go — Frame.Render; internal/launcher/cli_test.go — captureStdout;
internal/launcher/server.go — loadProfileManaged (reportStep calls); llama-launcher.TDD.md — progress.go file row

**Tests:**
- `TestProgressTracker_ElapsedOnActiveStep`: render output with an injected clock shows `1:07` on the last step only.
- `TestProgressTracker_CloseStopsTicker`: after `Close()`, no render happens (`-race` clean).
- `TestProgressTracker_NarrowerPopupBlanksOldCells`: a render whose active line carries ` 0:03`, then a shorter next step → the output blanks the previous rect's columns before drawing.

**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test -race ./internal/launcher/ -run 'TestProgressTracker|TestDoLoadProfile' -count=1`

**Commit:** `feat(menu): show elapsed time on the active progress step`

## 6. The menu keeps waiting after a startup timeout

**What:**
**Goal:** in terminal mode, when `LoadProfile` returns an error wrapping `ErrStartupTimeout`, `doLoadProfile` shows a popup `Still loading <profile>… m:ss` with the hint `Esc to return`. It polls every second:
- healthy → the same success lines as a normal load;
- the server is gone (not healthy, not `StartingUp`, no `LoadingPID`, PID not alive) on two polls in a row → an error popup with the original timeout text plus `The server is no longer running.`;
- Esc → the menu, with the server left running (it shows as `starting…`).

The non-terminal fallback still returns the error unchanged.
Depends on items 2 and 5.
**Approach (assumed at the header base):** `doLoadProfile` in menu.go reads addr, PID and log from the error with `errors.As` into `startupTimeout`. A new `waitStillLoading` in menu.go polls `b.HealthCheck`, `startingUp` and `loadingPID`, and uses the package's existing process-liveness helper for the PID. It reads keys through `readKeyTimeout` (ui.go) with a 1 s timeout and draws with the `Frame` popup style used by `progressTracker`. Success output is shared with the normal path: extract the success print into one helper that both call. TDD §3.1 (interactive mode) describes the popup.
**Regression guard.** The still-loading popup is entered only when errors.As yields a startupTimeout whose addr is non-empty and pid > 0 (a managed timeout); a startupTimeout with zero-valued fields (the external timeout from externalStartupTimeoutErr, which item 2 leaves unset) goes to the error popup exactly as before — add a test for that pass-through.
`waitStillLoading` enters `term.MakeRaw` and restores it on every return path (doLoadProfile runs in cooked mode; `readKeyTimeout` needs raw), and treats `keyCtrlC` and `keyQ` like Esc.
`LoadProfile` binds `realOps` (server.go:773-775), so the post-`LoadProfile` branching moves into a helper taking `(err, terminal bool, waiter)`; the pass-through tests drive that helper.
**Files:** internal/launcher/menu.go, internal/launcher/menu_test.go, llama-launcher.TDD.md
**Read first:** internal/launcher/menu.go — doLoadProfile, RunInteractiveMenu (showErrorPopup, ShouldAutoClose); internal/launcher/server.go — startupTimeout, startupTimeoutErr, externalStartupTimeoutErr, connectExternalServer, startingUp, loadingPID, IsProcessAlive;
internal/launcher/ui.go — readKeyTimeout, readKey, selectMenu, waitForAnyKey, showErrorPopup; internal/launcher/menu_test.go — TestDoLoadProfile_RefusesStartingOccupant; internal/launcher/cli_test.go — captureStdout

**Tests:**
- `waitStillLoading` takes its probes and key reader as injected funcs:
  - healthy on the 3rd poll → success;
  - gone twice → error containing the timeout text and `no longer running`;
  - Esc → returns nil with no stop issued; `keyCtrlC` and `keyQ` → the same.
- The post-`LoadProfile` helper, with a waiter that records calls:
  - non-terminal: a managed timeout error passes through unchanged, waiter not called;
  - terminal, a `startupTimeout` with zero addr/pid (the external timeout) → the error unchanged, waiter not called;
  - terminal, a managed `startupTimeout` (addr set, pid > 0) → the waiter is called with that addr and PID.

**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test -race ./internal/launcher/ -run 'TestWaitStillLoading|TestDoLoadProfile|TestMenu' -count=1`

**Commit:** `feat(menu): keep polling a still-loading server after a startup timeout`
