# Plan: Splash is visible when bound to a wildcard host

**Goal:** A Splash server configured on the IPv4 wildcard `0.0.0.0` or an empty host is probed, discovered, health-waited and stopped like any other: its HTTP probes dial loopback, while the instance stays keyed by its configured address (ADR-0006). A Splash bound to a non-loopback host also accepts requests that name the machine by its hostname.
**Date:** 2026-09-24
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** 3b60af1
**Closes:** llama-launcher-splash-wildcard-host-probe

**Regression check (2026-09-24, 01d657b):**
- 1: guard folded (writer decision: IPv6 wildcard out of scope; header and item Goal scoped to `0.0.0.0` and an empty host; the decision overrides the report's discovery-test guard).
- 2: guard folded (hostname pin scope and the existing `0.0.0.0` case; `splashLoadingPID` test argv; `BuildServerArgs` doc comment).
- 4: recast (writer decision: owns the docs for items 1–3, including item 3's `TestSplashWildcardHost`).

**Regression check (2026-09-24, 3b60af1):**
- 1: guard folded (writer decision: restores the discovery-level `TestDiscoverRunningInstances_WildcardSplash` in `discovery_test.go`, alongside the IPv6 scoping; supersedes the prior round's "overrides the report's discovery-test guard").
- 4: guard folded (sidecar CHANGELOG entry amending the Unreleased Splash bullet; widened site-finding grep; §12.5 integration-table row and §12.1 `TestSplashHealthCheck`/`TestSplashStartingUp` rows; Acceptance greps for loopback, DNS-name and hostname wording).

**Sources:**
- bead `llama-launcher-splash-wildcard-host-probe` (`bd show`)
- Splash source `~/Repos/splash` @ d2f902e: `server/server.py` (`FrontendServer.allowed_hosts` drops `0.0.0.0`/`::`; `--allowed-host` is `action="append"`), `server/http_security.py` (`validate_headers` lowercases the Host and strips a trailing dot)
- `docs/adr/0006-instances-are-keyed-by-address.md`, `docs/adr/0015-a-loading-splash-is-found-by-its-process.md`
- `llama-launcher.TDD.md` §5.3, §8 (Splash command line)

**Ratified design calls** (user, 2026-09-24):
- **Probe scope:** Splash only. One shared helper lives in `backend_http.go`, and only Splash's `HealthCheck`, `StartingUp` and `ListRunningModels` call it. The llamacpp, Ollama and LM Studio probes stay the same.
- **LAN hostname:** the launcher adds `--allowed-host` itself (not docs-only). It adds `os.Hostname()` and that name's first label when the name has a dot (e.g. `Apollo-II.local` and `Apollo-II`). It does this only when the configured host is not loopback. `extra_args` `--allowed-host` still adds more names.
- **Working tree:** the splash-loading-invisible work (ADR-0015, `LoadingPID`) landed on its own in b2df60a (bead closed by 3b60af1), before this plan runs. Items assume it is present.
- **IPv6 wildcard:** out of scope; producers format host:port without brackets — bead llama-launcher-ipv6-host-address-format (writer, 2026-09-24).

**Standing requirements:**
- skills: coding-standards
- Unit tests use `httptest` servers. Only item 3 needs a real Splash.
- Follow-ups become beads with spoken ids (`bd create --id llama-launcher-<slug>`), never a NOTES line alone.

**Out of scope:**
- Wildcard-to-loopback dialing for llamacpp, Ollama and LM Studio.
- `llama-launcher-splash-loading-invisible` (a separate bead; its work lands before this plan).
- Any change to how instances are keyed or displayed. The configured address stays the identity.
- IPv6 host addresses (bead llama-launcher-ipv6-host-address-format)

## 1. Splash probes dial loopback for a wildcard host — ✅ DONE (2026-09-24)

NOTES (2026-09-24): no CHANGELOG entry in this sidecar — item 4 owns the docs for items 1–3, including the CHANGELOG entry amending the Unreleased Splash bullet.
NOTES (2026-09-24): the Splash wildcard unit test is named `TestSplashWildcardProbe` (matches the Acceptance `TestSplash` pattern; not `TestSplashWildcardHost`, which item 3 reserves for its integration test); it also asserts that the Host-checking fake (`splashHostCheckingServer`, shared by the server and discovery tests) 403s a wildcard Host, so the fake cannot pass vacuously. Bite check: `TestSplashWildcardProbe`, the new `TestIdentifyBackend` subtest and `TestDiscoverRunningInstances_WildcardSplash` all fail with the base `backend_splash.go`.

**What:** Fixes the defect in bead `llama-launcher-splash-wildcard-host-probe`. Splash 403s every probe that sends `Host: 0.0.0.0:<port>`, so with the stock `host: "0.0.0.0"` the launcher's status, load health-wait, stop and `auto_stop_server` never see a running Splash.
**Regression guard.** The IPv6 wildcard is out of scope. Every address producer builds host:port with fmt.Sprintf("%s:%d"), so host "::" yields an unparseable ":::port" launcher-wide, and that is tracked as bead llama-launcher-ipv6-host-address-format. Scope the header Goal and item 1's Goal to the IPv4 wildcard 0.0.0.0 and an empty host. probeAddr still maps a well-formed "[::]:port" to "[::1]:port" (keep that TestProbeAddr row, labelled helper-only). Add "IPv6 host addresses (bead llama-launcher-ipv6-host-address-format)" to the header's Out of scope list, and record under Ratified design calls: "**IPv6 wildcard:** out of scope; producers format host:port without brackets — bead llama-launcher-ipv6-host-address-format (writer, 2026-09-24)." In addition to the IPv6 scoping, restore the discovery-level guard: item 1's Tests add a discovery-level unit test (`TestDiscoverRunningInstances_WildcardSplash`, in `internal/launcher/discovery_test.go`) that drives `probeInstance` / discovery for backend `splash` at host `0.0.0.0` against the Host-checking httptest server. It asserts a non-Starting instance keyed `0.0.0.0:<port>` with the served model.
**Goal:** `(&Splash{}).HealthCheck`, `StartingUp` and `ListRunningModels`, when called with the IPv4 wildcard `0.0.0.0:<port>` or an empty host `:<port>`, reach a server that rejects any non-loopback `Host` and get its real answer. Discovery reports a Ready Splash at the configured `0.0.0.0:<port>` address. `identifyBackend("0.0.0.0:<port>")` returns `splash`.
**Approach (assumed at the header base):**
- Add a pure helper to `internal/launcher/backend_http.go`: `probeAddr(addr string) string`. It maps a wildcard host to its loopback: an empty host or an IPv4 unspecified IP gives `127.0.0.1`, and an IPv6 unspecified IP gives `::1`. It rebuilds the address with `net.JoinHostPort`. Every other address is returned unchanged, and so is one `net.SplitHostPort` cannot parse.
- In `backend_splash.go`, `HealthCheck` and `StartingUp` build their `/ready` URL from `probeAddr(addr)`, and `ListRunningModels` passes `probeAddr(addr)` to `openAIModelList`. `LoadingPID` keeps the raw `addr`, because the process table holds the configured `--host`.
- Callers (discovery `probeInstance`, the start-path health wait, `identifyBackend`, stop) keep passing the configured address. Nothing outside Splash changes.
**Files:** internal/launcher/backend_http.go, internal/launcher/backend_http_test.go, internal/launcher/backend_splash.go, internal/launcher/backend_splash_test.go, internal/launcher/server_test.go, internal/launcher/discovery_test.go
**Read first:** internal/launcher/backend_splash.go — HealthCheck, StartingUp, ListRunningModels; internal/launcher/backend_http.go — openAIModelList; internal/launcher/server.go — identifyBackend;
internal/launcher/discovery.go — probeInstance; internal/launcher/backend_splash_test.go — splashTestServer; internal/launcher/server_test.go — TestIdentifyBackend
**Tests:**
- `TestProbeAddr` (table): `0.0.0.0:1111`→`127.0.0.1:1111`, `[::]:1111`→`[::1]:1111` (helper-only: IPv6 host addresses are out of scope), `:1111`→`127.0.0.1:1111`; `127.0.0.1:1111`, `192.168.1.5:1111`, `host.local:1111` and `garbage` unchanged.
- A Host-checking httptest server with the `Server: Splash` header: it answers 403 unless the `Host` hostname is `127.0.0.1`/`localhost`/`::1`, and it serves `/ready` and `/v1/models`. `HealthCheck`, `StartingUp` (503 variant) and `ListRunningModels` are probed at `0.0.0.0:<its port>` and succeed. They must fail against the base tree (bite check).
- In `server_test.go`: `identifyBackend("0.0.0.0:<port>")` returns `splash` against that server.
- In `discovery_test.go`: `TestDiscoverRunningInstances_WildcardSplash` (the `TestDiscoverRunningInstances_FindsReachable` pattern, `Defaults` host `0.0.0.0` and the Host-checking server's port, backend `splash`) drives discovery and asserts one non-Starting instance keyed `0.0.0.0:<port>` whose `ActiveModel` is the served model.
**Acceptance:**
- `go build ./... && go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestProbeAddr|TestSplash|TestIdentifyBackend|TestDiscoverRunningInstances_WildcardSplash' -count=1`
**Commit:** `fix(launcher): probe a wildcard-bound Splash over loopback`

## 2. Splash accepts the machine's hostname on a non-loopback bind — ✅ DONE (2026-09-24)

NOTES (2026-09-24): no CHANGELOG entry in this sidecar — item 4 owns the docs for items 1–3, including the CHANGELOG entry amending the Unreleased Splash bullet (same precedent as item 1).
NOTES (2026-09-24): `TestSplashBuildServerArgs` and all its subtests are now non-parallel; each case pins `hostname` through a new `pinHostname` helper (a `t.Cleanup` per subtest is safe because no subtest is parallel). Beyond the listed cases it also covers a trailing-dot hostname, an empty hostname and a `localhost` hostname; the loopback-`localhost` case uses `LocalHost` to exercise the case-insensitive match.
NOTES (2026-09-24): the `splashLoadingPID` regression test is a separate non-parallel top-level test, `TestSplashLoadingPIDMatchesBuiltArgs` (binary `/usr/local/bin/splash` prepended, host `192.168.1.5`, probed at `192.168.1.5:18731`), rather than a row in the parallel `TestSplashLoadingPID`, because it reads the pinned `hostname` seam.
NOTES (2026-09-24): a failed `os.Hostname` lookup is skipped without logging — `BuildServerArgs` has no error return and the package has no logger; the doc comment on `machineHostNames` records the behaviour. The `--allowed-host` flag name is the new constant `splashAllowedHostFlag`.

**What:** Resolves the bead's related LAN case: a client that names the machine (`Apollo-II.local`) gets a 403 unless it is listed in `--allowed-host`. Depends on item 1 (same files).
**Regression guard.** Pin `hostname` in a non-parallel scope that outlives every subtest reading it (the parent `TestSplashBuildServerArgs` without `t.Parallel`, or a separate non-parallel top-level test) — a per-subtest `t.Cleanup` restores it before paused parallel subtests resume. The existing case "host, port and context" (host `0.0.0.0`) moves under the pin and then expects both `--allowed-host` names after `--max-context`. The `splashLoadingPID` test prepends a binary path (e.g. `/usr/local/bin/splash`) to the `BuildServerArgs` argv, because `isSplashServeCommand` needs `splash`/`launcher.py` before `serve`; it uses a non-loopback host so the flags are present and probes `<that host>:<port>`. Update the `BuildServerArgs` doc comment to show `[--allowed-host NAME...]` after `--max-context` and say when the launcher adds it.
**Goal:** `(&Splash{}).BuildServerArgs` emits `--allowed-host <hostname>` for the machine's hostname, and for its first label when the name has a dot, whenever the resolved `host` is not loopback. These flags go before `extra_args`. A loopback or unset host (Splash's default `127.0.0.1`) gets none. So does an empty or failed hostname lookup.
**Approach (assumed at the header base):**
- Add a package variable `hostname = os.Hostname` in `backend_splash.go` so tests can pin it.
- A host is loopback when it is `localhost` (case-insensitive) or parses as an IP with `IsLoopback()`. A nil `params.Host` counts as loopback. Wildcard and LAN IPs are non-loopback.
- Names: the hostname with any trailing `.` removed, plus the text before its first `.` when that text is non-empty and differs from the whole name. Skip a name equal to `localhost`, and emit no duplicates. The flags go right after `--max-context`, so `extra_args` stays last and can add more (`--allowed-host` appends in Splash).
- `isSplashServeCommand` and `lastFlagValue` (ADR-0015) read only `--model`, `--host`, `--port` and `--binary`, so they are unaffected. Assert this with a test.
**Files:** internal/launcher/backend_splash.go, internal/launcher/backend_splash_test.go
**Read first:** internal/launcher/backend_splash.go — BuildServerArgs, splashLoadingPID, isSplashServeCommand, lastFlagValue; internal/launcher/backend_splash_test.go — TestSplashBuildServerArgs, TestSplashLoadingPID, splashLoadingArgs;
internal/launcher/config.go — applyFallbacks (nil host becomes defaultHost 127.0.0.1)
**Tests:**
- Extend `TestSplashBuildServerArgs`, with `hostname` pinned in a non-parallel scope that outlives every pinned subtest (no `t.Parallel` in the pinned cases):
  - host `0.0.0.0` with `Apollo-II.local` adds both names (the existing "host, port and context" case moves under the pin);
  - host `::` does the same;
  - host `192.168.1.5` with dotless `apollo` adds one name;
  - hosts `127.0.0.1`, `localhost` and nil add none;
  - a hostname error adds none;
  - `extra_args` `--allowed-host proxy.local` stays after the launcher's flags.
- Test that `splashLoadingPID` still matches an argv from `BuildServerArgs` that carries the new flags (binary path such as `/usr/local/bin/splash` prepended, non-loopback host, probed at `<that host>:<port>`).
**Acceptance:**
- `go vet ./internal/launcher/`
- `go test ./internal/launcher/ -run 'TestSplash' -count=1`
**Commit:** `feat(launcher): let Splash accept the machine hostname on a LAN bind`

## 3. Integration test covers a wildcard-bound Splash — ✅ DONE (2026-09-24)

NOTES (2026-09-24): TestSplashWildcardHost asserts discovery's ActiveModel equals INTEGRATION_MODEL_SPLASH exactly (the same name TestSplashLifecycle's list-running-models step requires), not merely non-empty.
NOTES (2026-09-24): not run live — the host already runs a user Splash (incoai/Qwen3.8-27B-Splash on 0.0.0.0:1111) on a 32 GB machine, so a second 16 GB load was not started; the test skips without INTEGRATION_MODEL_SPLASH and needs a manual host run.

**What:** The real-Splash suite binds only `127.0.0.1`, which is why it missed this defect. Add a `0.0.0.0` case. Depends on items 1 and 2.
**Goal:** `internal/launcher/integration_splash_test.go` has a test that starts a real Splash on `0.0.0.0:<free port>` and checks three things through the configured address: it waits until the server is healthy, discovery reports it Ready with the served model, and `Stop` frees the port and ends the process group. The test compiles under `-tags integration` and skips without `INTEGRATION_MODEL_SPLASH`.
**Approach (assumed at the header base):**
- Give `resolveSplashProfile` a `host string` parameter. Existing callers pass `loopbackHost`.
- Add `TestSplashWildcardHost`. It reuses `waitForSplashHealthy` and `waitForProcessGone`, and calls `DiscoverRunningInstances` (or the discovery entry point the lifecycle test uses) to assert `Starting == false` and a non-empty `ActiveModel` at `0.0.0.0:<port>`.
**Files:** internal/launcher/integration_splash_test.go
**Read first:** internal/launcher/integration_splash_test.go — resolveSplashProfile, waitForSplashHealthy, waitForProcessGone, TestSplashStopWhileLoading (DiscoverRunningInstances+findInstance pattern); internal/launcher/integration_test.go — freePort;
internal/launcher/integration_llamacpp_test.go — killServerOnCleanup; internal/launcher/discovery.go — findInstance; internal/launcher/server.go — findListeningPIDs (port-only lsof fallback)
**Tests:** the new integration test.
**Acceptance:**
- `go vet -tags integration ./internal/launcher/`
- `GOOS=windows go vet -tags integration ./internal/launcher/`
- `go test -tags integration ./internal/launcher/ -run 'TestSplash' -count=1` (skips without the env var; a manual run on the host exercises it)
**Commit:** `test(launcher): cover a Splash bound to 0.0.0.0 in the integration suite`

## 4. Docs: wildcard probing and the automatic hostname — ✅ DONE (2026-09-24)

NOTES (2026-09-24): re-derived from the assumption that the wildcard probe case sits inside the §12.1 `TestSplashHealthCheck`/`TestSplashStartingUp` rows — item 1 put it in its own `TestSplashWildcardProbe`, so it got its own §12.1 row next to them, and those two rows were left as they were.
NOTES (2026-09-24): README — the automatic hostname and wildcard probing went into a new **Network access** bullet directly after the Splash **Parameters** bullet, which now lists only the `extra_args` flags; the stale "for access by DNS name" wording is gone.
NOTES (2026-09-24): consequential edit — llama-launcher.TDD.md (`backend_http_test.go` row and §12.1 `TestDiscoverRunningInstances_*` row): made necessary by documenting item 1's `TestProbeAddr` and `TestDiscoverRunningInstances_WildcardSplash`.
NOTES (2026-09-24): item 1's `identifyBackend` wildcard subtest in `server_test.go` is not named on its own; the `server_test.go` row already lists `identifyBackend`.

**What:** Recast at the regression check (2026-09-24). This item owns every doc change for items 1–3. Depends on items 1, 2 and 3.
**Regression guard.** Item 4 owns the docs for items 1–3. It adds a §12.5 integration-table row for item 3's `TestSplashWildcardHost` (the TDD has no file-table row for `integration_splash_test.go`) and names it wherever the Splash integration tests are listed: the `INTEGRATION_MODEL_SPLASH` row and README's "The Splash test" sentence. State "Depends on items 1, 2 and 3".
Its sidecar records a CHANGELOG entry that amends the Unreleased Splash bullet (`CHANGELOG.md:7`, which still routes "allowed host" through `extra_args`): wildcard/empty-host probes dial loopback, and the launcher adds `--allowed-host <hostname>` and `<short name>` on a non-loopback bind.
**Goal:** No doc says a DNS-name client needs `extra_args` `--allowed-host` when the machine's own hostname is enough. The docs state these three things:
- Splash probes dial loopback for a wildcard host, while the instance keeps its configured address.
- The launcher adds `--allowed-host` for the machine hostname and its short form on a non-loopback bind.
- `extra_args` is needed only for other names, such as a proxy or an alias.
**Approach (assumed at the header base):**
- `llama-launcher.TDD.md`: §8 Splash command line and its "Every other Splash flag" paragraph; the `backend_http.go` and `backend_splash.go` rows of the file table; the `splash` row of the health-check table (§5.3); the §12.1 `TestSplashHealthCheck` and `TestSplashStartingUp` rows (the wildcard `0.0.0.0` probe case) beside the `TestSplashBuildServerArgs` row; a §12.5 integration-table row for item 3's `TestSplashWildcardHost`, and the `INTEGRATION_MODEL_SPLASH` row naming it.
- `README.md`: the Splash **Parameters** bullet, and the `make test-integration` paragraph (its singular "The Splash test").
- `internal/launcher/defaults/config.yaml`: the Splash note in the header comment.
- `CHANGELOG.md`: through the sidecar only (see the guard), amending the Unreleased Splash bullet.
- The rule for finding every site: `grep -rniE 'allowed[-_ ]host|DNS name|TestSplashLifecycle|TestSplashStopWhileLoading|The Splash test' README.md llama-launcher.TDD.md CONTEXT.md CHANGELOG.md internal/launcher/defaults/config.yaml skills/ launcher/`. Update every hit that is stale for items 1–3.
**Files:** llama-launcher.TDD.md, README.md, internal/launcher/defaults/config.yaml
**Read first:** llama-launcher.TDD.md — §8.1 Splash "Nothing else is mapped" paragraph, §5.2 backend_http.go/backend_splash.go rows, §12.1 TestSplashHealthCheck/TestSplashStartingUp/TestSplashBuildServerArgs rows, §12.5 TestSplashStopWhileLoading row + INTEGRATION_MODEL_SPLASH row;
README.md — Splash Parameters bullet, `make test-integration` paragraph; internal/launcher/defaults/config.yaml — Splash example comment block; CHANGELOG.md — Unreleased "Splash LLM Server" bullet
**Tests:** none (docs only). The embedded default config must still parse.
**Acceptance:**
- `go test ./internal/launcher/ -run 'Default|Config' -count=1`
- `grep -n 'allowed-host' README.md llama-launcher.TDD.md internal/launcher/defaults/config.yaml` shows the automatic hostname wording.
- `grep -n 'loopback' llama-launcher.TDD.md` hits the `backend_http.go`/`backend_splash.go` file-table rows and the §5.3 `splash` row.
- `grep -rn 'for access by DNS name' README.md internal/launcher/defaults/config.yaml` prints nothing.
- `grep -n 'hostname' README.md llama-launcher.TDD.md internal/launcher/defaults/config.yaml` hits each Splash site.
**Commit:** `docs: describe Splash wildcard probing and the automatic allowed host`
