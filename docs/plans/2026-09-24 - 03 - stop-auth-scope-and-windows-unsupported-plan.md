# Plan: Scope the 401/403 stop carve-out and keep ErrUnsupported on Windows stop

**Goal:** Stop/Unload act on a 401/403 answer only at a configured address, as ADR-0010's amendment scopes it. The Windows stop verbs surface `ErrUnsupported` instead of losing it.
**Date:** 2026-09-24
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** ea4bab0

**Sources:** beads `llama-launcher-unconfigured-auth-listener-signalled`, `llama-launcher-windows-err-unsupported-lost`; `docs/adr/0010-starting-instances-are-visible-and-stoppable.md`, `docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md`, `docs/adr/0011-public-library-facade.md`; `llama-launcher.TDD.md` §16.6; `docs/plans/archived/2026-09-24 - 02 - code-audit-fixes-plan.md` item 6

**Ratified design calls** (user, 2026-09-24):
- **Configured-address source:** a RWMutex-guarded process-global snapshot pushed by `LoadConfigNotify` from `discoveryTargets(cfg)`; no facade signature change. Before any LoadConfig the set is empty — facade Stop never signals a 401/403 listener.
- **Unconfigured 401/403 listener:** Stop/Unload return `ErrNotRunning` (ADR-0010 foreign-occupant refusal); no signal.
- **Windows stop message:** `server at <addr> is still reachable and could not be stopped: stopping a server process: operation not supported on this platform`, exit 3, via a new seam function `requireProcessStop()`.
- **Windows scope:** the `connectExternalServer` path already wraps the sentinel (8e5ae5e); item 2 only pins it with a test (writer, 2026-09-24).

**Standing requirements:**
- skills: coding-standards. `make cross` (darwin/linux/windows build+vet) stays green; platform files follow the `process_unix.go` / `process_windows.go` split.
- Each item updates the TDD/README/ADR lines its own change invalidates; its CHANGELOG entry travels in its sidecar.
- Follow-ups become beads with spoken ids (`bd create --id llama-launcher-<slug>`), never a NOTES line alone.

**Out of scope:**
- Any change to discovery's probe set or the CLI/MCP stop paths beyond what item 1's shared check reaches.
- Windows process control beyond the stop verbs' error.

**Regression check (2026-09-24, ea4bab0):**
- 1: guard folded (unpinned unload legs, docs rule, explicit empty pin)
- 2: guard folded (docs rule, LoadProfile stop-step clause, stop-hook exception, Acceptance greps); supersedes ADR-0012's windows bullet and TDD §16.6 "Does not hold" via the third amendment

## 1. Stop acts on a 401/403 answer only at a configured address

**What:**
**Goal:** `identifyBackend`'s third pass accepts `ErrAuthFailed` only from a backend the last `LoadConfig` points at `addr`, so `StopInstance`, core and facade `Stop(addr)`, `UnloadInstanceModel` and `Unload` return `ErrNotRunning` and signal nothing at an address no config names, while a 401/403 listener at a configured address is still stopped with `Backend ""`.
Fixes bead llama-launcher-unconfigured-auth-listener-signalled: the third pass takes a 401/403 from any registered backend at any address, so the facade `Stop(addr)` SIGTERMs whatever answers 401/403 on an LLM API path anywhere, beyond the ADR-0010 amendment's "at a configured address".
**Approach (assumed at the header base):** `Stop(addr)` carries no `Config` and its facade signature is SemVer surface (TDD §16.2), so the configured set is process state pushed the way API keys are (the "one Config per process" rule). discovery.go gains a `sync.RWMutex`-guarded package snapshot of address → backend names, written by `applyConfiguredTargets(cfg)` from `discoveryTargets(cfg)` (the one definition of a configured address) and read by `configuredBackendsAt(addr)`. `LoadConfigNotify` calls `applyConfiguredTargets` right after `applyAPIKeys`, so `Config.Reload` and every facade `LoadConfig` refresh it; before any `LoadConfig` the set is empty. In `identifyBackend` the first loop records an auth error only for a name in `configuredBackendsAt(addr)`. The health and Starting passes, the signature and the sort-first rule stay unchanged, and the name returned is now a configured backend's. Consumers of the changed answer: `StopInstance`, `UnloadInstanceModel` and `realOps.identify` → `unloadServerModel`. At an unconfigured address all of them now take their existing `ErrNotRunning` branch. `authRefusalAt`, discovery, CLI and menu stop are untouched: they already use the `cfg` they hold.
Binding: the new state is not a seam. Tests set it through a `pinConfiguredTargets(t, cfg)` helper (helpers_test.go) that restores the previous snapshot in `t.Cleanup`. Every caller of that helper, and every facade test that relies on `LoadConfig` having configured an address, is a non-parallel top-level test. Real listeners only: httptest for identify, and a detached re-exec child for anything that signals, never the test's own process. Docs: the facade `Stop` doc names the configured-address scope; doc.go's "One Config per process" section adds that `LoadConfig` also sets the addresses a 401/403 stop may act on; TDD §6.5 third-pass paragraph, §16.2 point 4, and the §16 file rows for launcher_test.go (no real processes) and the new launcher_unix_test.go.
**Regression guard.** The unpinned (unconfigured-address) outcome of `UnloadInstanceModel` and facade `Unload` is tested, not only `Stop`'s: `unloadServerModel`'s auth refusal (server.go:1349-1352) becomes an arm fall-through there, and both arms must end at `ErrNotRunning` with no signal sent.
Docs are a rule, not a list: every comment or doc line describing the 401/403 identify/stop/unload outcome gets the configured-address scope, and every claim that the facade tests start no real process is updated; find the sites with `grep -rn "401/403\|refuses the configured api_key\|ErrAuthFailed\|real process\|starts a real server" internal/launcher/server.go launcher/ llama-launcher.TDD.md` (includes the StopInstance, identifyBackend, UnloadInstanceModel and activationOps.identify docs, facade `Unload`/`LoadConfig` docs, launcher_test.go:1-5 and TDD §16.5).
Every "unpinned" leg calls `pinConfiguredTargets(t, &Config{})` explicitly: earlier sequential `LoadConfig` calls (config_test.go, `runCLI` → `Run()`) leave their targets in the process-global snapshot, so the empty set is set by the test, never assumed.
**Files:** internal/launcher/discovery.go, internal/launcher/config.go, internal/launcher/server.go, internal/launcher/helpers_test.go, internal/launcher/server_test.go, internal/launcher/cli_test.go, launcher/launcher.go, launcher/doc.go, launcher/launcher_test.go, launcher/launcher_unix_test.go, llama-launcher.TDD.md
**Read first:** internal/launcher/server.go — identifyBackend, StopInstance, UnloadInstanceModel, unloadServerModel, realOps.identify; internal/launcher/discovery.go — discoveryTargets, authRefusalAt; internal/launcher/config.go — LoadConfigNotify, Reload, ConfiguredBackendAddr;
internal/launcher/cli_test.go — TestCmdStop_StopsAuthFailedServer, startAuthRefusingChild, TestAuthRefusingHelperProcess, startingCfg; internal/launcher/server_test.go — TestIdentifyBackend, TestUnloadInstanceModel_AuthFailed; internal/launcher/discovery_test.go — authRefusingServer, sharedAddrCfg;
launcher/launcher_test.go — writeConfig, standInConfig, deadAddr; launcher/launcher.go — Stop, Unload, LoadConfig
**Closes:** llama-launcher-unconfigured-auth-listener-signalled

**Tests:**
- `TestIdentifyBackend_AuthPassNeedsConfiguredAddress` (server_test.go, pins; replaces TestIdentifyBackend's parallel 401 subtest) runs against `authRefusingServer` 401. Unpinned (`pinConfiguredTargets(t, &Config{})`), it expects `ErrNotRunning` and name `""` (bite: pre-item gives llamacpp and the auth error). Pinned for llamacpp, it expects `llamacpp` and the "check api_key" error wrapping `ErrAuthFailed`. Pinned for ollama only, it expects `ollama` (bite: pre-item gives llamacpp).
- `TestUnloadInstanceModel_AuthFailed` becomes non-parallel and pins its address. `TestCmdStop_StopsAuthFailedServer` pins `cfg` before `cmdStop`. Both keep their assertions.
- `TestUnloadInstanceModel_AuthFailed` gains an unpinned leg (`pinConfiguredTargets(t, &Config{})`): `ErrNotRunning` and a nil instance (bite: pre-item returns the auth error).
- `TestUnload_UnconfiguredAuthRefusingListenerIsErrNotRunning` (launcher/launcher_test.go, httptest 401 listener, not parallel): `launcher.Unload` for `llamacpp` and for `ollama` gives `errors.Is(err, launcher.ErrNotRunning)` (bite: pre-item returns the auth error); no signal fires either way.
- launcher/launcher_unix_test.go (`//go:build !windows`, package `launcher_test`) holds `TestFacadeAuthRefusingHelperProcess`, re-exec'd with `Setsid` and serving 401 until signalled, plus a start helper that polls until the child answers 401.
- `TestStop_UnconfiguredAuthRefusingListenerIsNotSignalled`: `launcher.Stop(addr)` gives `errors.Is(err, launcher.ErrNotRunning)`, a non-nil result, and the child still answering 401 (bite: pre-item SIGTERMs the child and returns nil).
- `TestStop_ConfiguredAuthRefusingListenerIsStopped` (not parallel): `launcher.LoadConfig(writeConfig(t, standInConfig, host, port), nil)`, then `launcher.Stop(addr)` gives nil error, `Instance.Backend == ""`, `Instance.PID` equal to the child's PID, and the child reaped.

**Acceptance:**
- `go build ./... && go vet ./internal/launcher/ ./launcher/ && GOOS=windows go vet ./launcher/`
- `go test -race ./internal/launcher/ -run 'TestIdentifyBackend|TestStopInstance|TestStopServerAt|TestUnloadInstanceModel|TestCmdStop|TestLoadConfig' -count=1`
- `go test -race ./launcher/ -run 'TestStop_|TestLoadConfig|TestUnload_' -count=1`

**Commit:** `fix(launcher): stop signals a 401/403 listener only at a configured address`

## 2. Stop verbs wrap ErrUnsupported where the platform cannot stop by PID

**What:**
**Goal:** A `Stop` — or an `Unload` that reduces to one on a managed backend — that leaves the server reachable on a build whose process seam refuses stopping returns an error wrapping `ErrUnsupported`; on unix every stop outcome and message is byte-identical, and a native stop hook that works (LM Studio's `lms server stop`) still succeeds. TDD §16.6, `launcher/doc.go`, README and ADR-0012 state that all four windows refusal paths preserve the sentinel.
Fixes bead llama-launcher-windows-err-unsupported-lost: the stop verbs never reach a seam function on windows (`IsProcessAlive` is false, lsof is absent), so they end at the generic "PID could not be determined". The bead's other path — `connectExternalServer` dropping Ollama's `TryStart` refusal — was closed by 8e5ae5e (it wraps `tryErr`); this item only pins it.
**Approach (assumed at the header base):** add a sixth seam function `requireProcessStop() error` to the per-OS process files: `process_unix.go` returns nil; `process_windows.go` returns `fmt.Errorf("stopping a server process: %w", ErrUnsupported)`. In `server.go`, beside `processTable`, declare `var processStopGuard = requireProcessStop` (a variable so tests substitute a refusing seam on a host whose own seam permits). In `stopServerAt`, after the successful-stop return and the `stopErr` (stop hook failed) return, before both `pid`-based returns, add: when `processStopGuard()` is non-nil, return `pid, fmt.Errorf("server at %s is still reachable and could not be stopped: %w", addr, guardErr)`. Nothing else in the stop path moves: no early refusal (LM Studio's hook must still run), no change to `IsProcessAlive`, `terminatePID` or `StopInstance`; `StopResult`, `cmdStop`/`cmdUnload` exit codes (3) and the MCP adapter pass the error through unchanged.
Binding: platform knowledge stays in the build-tagged files, never a `runtime.GOOS` branch in `server.go`; wrap with `%w`; tests that swap `processStopGuard` (or the `llmServers` registry) are not parallel and restore it with `t.Cleanup`.
Docs: TDD §16.6 — "Three do, one does not" becomes all four hold, the stop bullet describes the new wrap, the seam-file list and the `process_unix.go`/`process_windows.go` table rows gain `requireProcessStop` (five → six functions), the "two `require…` guards" sentence says the stop guard names the refusal after the mechanisms ran rather than before, and the closing "tracked in `TODO.md`" sentence is replaced by the closed state. `launcher/doc.go` "# Platforms": the stop verbs wrap the sentinel too. README Windows row: stopping llama-server/Ollama/Splash is refused wrapping `ErrUnsupported`. ADR-0012 gains a third amendment (2026-09-24) closing the known gap.
**Regression guard.** Docs are a rule, not a list: every comment/doc naming the process seam's function count, the stop verbs' windows outcome, or the stop tests is updated (includes process_unix.go:3-5, TDD rows at 483, 514 and 898); find them with `grep -rn 'five functions\|five signatures\|could not be determined\|Does not hold\|tracked in .TODO.md.' internal launcher llama-launcher.TDD.md README.md docs/adr`.
The Goal and the TDD §16.6 / doc.go text also say that `LoadProfile` errors from its stop step (auto-stop at server.go:817, managed restart at server.go:1065, both via `realOps.stop` → `StopInstance`) carry the sentinel too.
The wrap applies "unless the backend's native stop hook itself failed": the `stopErr` return (server.go:430-431, `%v`) stays first, so a failing `lms server stop` still returns "stop hook failed" without the sentinel; the Goal and the §16.6 stop bullet say so.
Supersedes ADR-0012's windows bullet (lines 34-40, "known gap … tracked in `TODO.md`") and TDD §16.6 "Does not hold — the stop verbs" (lines 1187-1189) via the ADR's third amendment.
**Files:** internal/launcher/process_unix.go, internal/launcher/process_windows.go, internal/launcher/server.go, internal/launcher/server_test.go, launcher/doc.go, llama-launcher.TDD.md, README.md, docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md
**Read first:** internal/launcher/server.go — stopServerAt, StopInstance, processTable, unloadServerModel, realOps.stop, StartServer, connectExternalServer; internal/launcher/process_unix.go — requireProcessControl; internal/launcher/process_windows.go — requireProcessControl;
internal/launcher/server_test.go — hookStopServer, TestStopServerAt_TryStopFlipsHealthCheck, TestStopServerAt_StartingOccupant, fakeExternalBackend, TestConnectExternal_TryStartErrorSurfaces; internal/launcher/helpers_test.go — deadAddr;
llama-launcher.TDD.md — §16.6 Platform contract; launcher/doc.go — Platforms
**Closes:** llama-launcher-windows-err-unsupported-lost

**Tests:** in `internal/launcher/server_test.go` (not parallel), with a registered always-healthy stub whose `TryStop` is a no-op, on a `deadAddr(t)`:
- `TestStop_RefusingStopSeamWrapsErrUnsupported` — `processStopGuard` swapped to return `fmt.Errorf("stopping a server process: %w", ErrUnsupported)`; `Stop(addr)` returns an error with `errors.Is(err, ErrUnsupported)`, text containing "still reachable and could not be stopped", not "PID could not be determined"; `StopResult` non-nil.
- `TestUnload_ManagedRefusingStopSeamWrapsErrUnsupported` — the same stub plus `ServerBinary`/`BuildServerArgs`/`BuildServerEnv` (managed); `Unload(name, addr)` wraps `ErrUnsupported`.
- `TestStop_PermittingStopSeamKeepsUnixMessage` — real seam (unswapped): `Stop(addr)` still returns "…PID could not be determined…" and `errors.Is(err, ErrUnsupported)` is false.
- `TestStopServerAt_HookStopSucceedsUnderRefusingSeam` — `hookStopServer` with the refusing seam: `stopServerAt` returns nil.
- `TestStartServer_TryStartUnsupportedWrapsErrUnsupported` — regression pin for path 1 (passes pre-item): a registered external fake whose `TryStart` returns the windows `requireProcessControl` error; `StartServer` (what `LoadProfile` delegates to) wraps `ErrUnsupported`.
Bite: the first four fail to compile against the pre-item tree (`processStopGuard` absent); the first two fail on the assertion once it exists without the `stopServerAt` wrap.

**Acceptance:**
- `go build ./... && go vet ./internal/launcher/ ./launcher/`
- `GOOS=windows go build ./... && GOOS=windows go vet ./...`
- `go test ./internal/launcher/ -count=1 -run 'TestStop_|TestUnload_Managed|TestStopServerAt_|TestStartServer_TryStart|TestConnectExternal_'`
- `go test ./launcher/ -count=1`
- `! grep -n 'Does not hold\|PID undetermined\|tracked in .TODO.md.' llama-launcher.TDD.md launcher/doc.go`
- `grep -q requireProcessStop llama-launcher.TDD.md && grep -q requireProcessStop docs/adr/0012-*.md`

**Commit:** `fix(launcher): wrap ErrUnsupported when a stop cannot signal on this platform`
