# Plan: Splash as a fourth LLM Server

**Goal:** Add `splash` as a managed LLM Server beside `llamacpp`, `ollama` and `lmstudio`, so Splash profiles can be listed, loaded, unloaded and stopped from the CLI, the TUI and the MCP adapter. Splash is restarted per profile, like llamacpp (ADR-0003).
**Date:** 2026-09-24
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** e89f085
**Beads:** owns `llama-launcher-splash-server-missing` (labelled `planned`, spec-id = this file); closeout closes it. Closeout also resolves `llama-launcher-changelog-unreleased-heading-missing`, `llama-launcher-changelog-extra-args-claim` and `llama-launcher-changelog-log-redaction-entry` (see Standing requirements). Other beads are not owned.

**Sources:**
- `docs/handoffs/2026-09-24 - 01 - add-splash-backend.md`
- `CONTEXT.md`
- `docs/adr/0003-llamacpp-restart-per-profile.md`, `0005-profile-server-is-identity.md`, `0006-instances-are-keyed-by-address.md`, `0007-profile-activation-idempotency.md`, `0008-mcp-control-plane-adapter.md`, `0010-starting-instances-are-visible-and-stoppable.md`
- Splash source `~/Repos/splash` (branch `apple7-m1-kernels`): `install/launcher.py` (`serve`, `_parse_max_context`, `_ensure_installed`), `install/models.py` (`installed_root`, `verify_installed`), `install/paths.py` (`MODELS`, `PACKAGED`), `server/server.py` (`version_string`, `/ready`, SIGTERM handler)

**Splash facts (verified 2026-09-24 against source):**
- `splash` is a sh script that `exec`s Python, which `execve`s `server/server.py`. The PID stays the same, so the launcher's SIGTERM to the PID and its group reaches the server, which handles SIGTERM the same as SIGINT.
- `/health` returns 200 `{"status":"ok"}` even while the model is loading. `/ready` returns 200 once serving and 503 before that; it is public. Every response carries the header `Server: Splash`.
- `--max-context` accepts a plain token integer from 1 to 262144, or `NK`. The API key comes from the `SPLASH_API_KEY` env var.
- Every installed Splash package keeps a pinned Hugging Face snapshot (install/models.py _retain_snapshot_ref), so <hub>/models--<owner>--<repo>/snapshots/<rev>/manifest.json exists for any installed model in both source and packaged layouts; hub = $HF_HUB_CACHE, else $HF_HOME/hub, else $XDG_CACHE_HOME/huggingface/hub, else ~/.cache/huggingface/hub. The `splash` command on PATH may be a wrapper script (Splash's installer writes one; the source-checkout script breaks when symlinked because it derives ROOT from dirname $0), so the launcher never derives Splash's root from the binary.
- The Host-header check accepts the socket's local IP, so LAN/VM access by IP needs no `--allowed-host`; DNS names need it, passed via `extra_args`.

**Ratified design calls** (user, 2026-09-24):
- **Binary:** `splash` looked up on PATH, no config key; the not-found error says to put Splash's `splash` command on PATH (for a source checkout, a wrapper script that execs <checkout>/splash — a plain symlink breaks it).
- **Params:** `context_size` becomes `--max-context`; `host`/`port` become `--host`/`--port`; `api_key` goes through `SPLASH_API_KEY`; everything else (reasoning effort, kv-format, max-memory, allowed-host) goes through `extra_args`. Sampling params are ignored and documented as such.
- **Not-installed model:** `ResolveModel` refuses a model that isn't installed — no snapshot `manifest.json` for it in the Hugging Face cache — and names the one-time manual install command. The launcher never triggers the ~20-minute download.
- **Install check:** Hugging Face cache snapshot manifest, not Splash's root layout (user chose the HF-cache option 2026-09-24; root detection is impossible through a wrapper).
- **Docs scope:** every human- and AI-facing doc covers Splash — README, TDD, CONTEXT, ADR-0014, AGENTS.md, skills/manage-llm-server/SKILL.md, launcher/doc.go, example config, MCP help text; CHANGELOG entries travel in item sidecars (user, 2026-09-24).
- **PATH reach:** only starting a Splash server needs `splash` on PATH; profile resolution (config validate, discovery, unload, show-config) never does (writer, 2026-09-24 — narrower than the draft's ResolveModel PATH check).

**Standing requirements:**
- skills: coding-standards
- Item tests use `httptest` servers that mimic Splash's responses. No item needs a live Splash server except item 6.
- CHANGELOG.md has no Unreleased section (top is the released `## 1.7.0`); closeout adds the sidecar entries under a new `## Unreleased` heading above it — never a version number. The same closeout also adds the two pending Security entries whose verbatim text is in beads `llama-launcher-changelog-extra-args-claim` and `llama-launcher-changelog-log-redaction-entry` (they were only waiting for this heading), then closes those two beads and `llama-launcher-changelog-unreleased-heading-missing`.
- Issue tracking is beads (`bd`), not `TODO.md` (retired 2026-09-24); see `AGENTS.md`. Any follow-up an item leaves behind becomes a bead with a spoken id (`bd create --id llama-launcher-<slug>`), not a NOTES line alone.

**Out of scope:**
- `LiveParamsQuerier` for Splash (drift detection on `/status`).
- A first-class `reasoning_effort` profile field.
- A binary-path config key.
- Driving the model download or install.
- Homebrew and the Splash `main` branch.
- Writing a profile into the user's live config.

**Regression check (2026-09-24, e89f085):**
- header: decisions applied — stale other-streams bullet deleted (that work is commit 145cfdc); `MODELS`/`<root>` fact replaced by the Hugging Face cache fact; Binary and Not-installed calls reworded; Install check ratified.
- 1: guard folded (repo-ID rule mirrors Splash's `validate_repo_id`).
- 2: recast (Hugging Face cache install check, wrapper-script PATH hint; no root detection).
- 3: guard folded (Splash case in `TestIdentifyBackend`; bite-capable `StartingUp` 503 fixture; every `/health`-status comment/doc line).
- 4: guard folded (wrapper/HF-cache wording; rule over enumerating comments; generated config must load).
- 6: guard folded (liveness via the process seam; windows integration vet in Acceptance).
- 7: guard folded (ADR-0014 records the HF-cache check).
- 8: guard folded (wrapper/HF-cache wording; supersedes `llama-launcher.TDD.md` §5.3 "all discrimination must be body-based"; `splash`-on-PATH kept out of the §5.4 module table).
- header (re-check round): decision applied — **Docs scope** ratified line added.
- 2 (re-check round): guard folded (PATH-check reach into unload/discovery/show-config declared intended; installed also needs a `refs/splash/*/<rev>` ref; hub tests pin all four env vars, empty = unset).
- 7 (re-check round): recast (AGENTS.md added to Goal and Files).
- 9 (re-check round): added — documentation sweep for Splash (writer decision).
- header (re-check round 2): decisions applied — CHANGELOG `## Unreleased` standing requirement added; **PATH reach** ratified line added.
- 1 (re-check round 2): recast (binary-not-found error gains a per-server hint via optional `binaryInstallHinter`; `backend.go`, `server.go`, `server_test.go` added).
- 2 (re-check round 2): recast (ResolveModel is install-only — no PATH lookup; guard (a) replaced, (b) and (c) kept).
- 7 (re-check round 2): guard folded (Goal states ADR-0014's HF-cache check; per-file case-insensitive Acceptance check).
- 7 (re-check round 2): rejected — doc.go must list Splash's windows refusal with the "own words" paths, never under the ErrUnsupported sentence; after item 2's recast ResolveModel does no PATH check, and `startManagedServer` calls `requireProcessControl` (server.go:65) before any binary lookup, so an installed-model Splash start on windows does wrap ErrUnsupported.
- 8 (re-check round 2): guard folded (setup text notes a launchd/MCP-adapter PATH must include the wrapper's directory).
- 9 (re-check round 2): guard folded (`backend-tests-plan.md` excluded as archival; Starting/503/health-signal rule with its own grep).

## 1. Splash LLM Server core — ✅ DONE (2026-09-24)
NOTES (2026-09-24): HealthCheck also routes a 401/403 on /ready through authFailedErr for the actionable auth message (the plan says only "anything else is an error"); /ready is public in Splash, so this only fires for a foreign server.
NOTES (2026-09-24): TestGetLLMServer_Known in backend_test.go still lists only the three original servers; splash registration is covered by the new TestSplashRegistered instead.

**What:** Recast at the regression check (2026-09-24).
**Goal:** `GetLLMServer("splash")` returns a `ManagedLLMServer` that also implements `StartupProber` and `ModelLister`. It passes its own unit tests for identification, argument building, env and param specs.
**Approach (assumed at the header base):** New file `internal/launcher/backend_splash.go`, type `Splash` embedding `apiKeyHolder`, registered in `init` via `RegisterLLMServer`. Follow the `LlamaCpp` layout.
- `Name` is `"splash"`, `DisplayName` is `"Splash"`, `DefaultAddr` is `"127.0.0.1:8000"`.
- `HealthCheck`: `GET /ready` through `authedGet` returns 200 **and** the response header `Server` starts with `Splash`. Anything else is an error.
- `StartingUp`: `GET /ready` returns 503 **and** has the `Server: Splash` header.
- `ListRunningModels` delegates to `openAIModelList`.
- `ParamSpecs` returns only `specContextSize`.
- `LoadModel`, `UnloadModel`, `TryStart` and `TryStop` are no-ops, as in llamacpp (ADR-0003).
- `ServerBinary` returns `"splash"`.
- `BuildServerArgs` builds `serve --model <ModelPath> [--host H] [--port P] [--max-context N] <ExtraArgs...>`, with `ExtraArgs` last.
- `BuildServerEnv` returns `SPLASH_API_KEY=<key>` when a key is set, otherwise nil.
- `ResolveModel` for this item: an empty ref returns `""`; otherwise the ref must match `owner/repo` (exactly one `/`, both parts non-empty, characters `[A-Za-z0-9._-]`) and is returned unchanged. Item 2 adds the install check.
- `startManagedServer`'s "server binary not found: <binary>" error appends a per-server hint when the `ManagedLLMServer` implements a new optional interface `binaryInstallHinter { BinaryInstallHint() string }` (defined in `backend.go` beside the other optional interfaces). Splash implements it, returning the Binary-call hint (put Splash's `splash` command on PATH; for a source checkout a wrapper script that execs <checkout>/splash, a plain symlink breaks it; a launchd/MCP-adapter PATH must include the wrapper's directory). llamacpp does not implement it, so its error text is byte-identical.
**Regression guard.** The `owner/repo` rule mirrors Splash's `REPO_ID` (`~/Repos/splash/install/models.py` `validate_repo_id`): first and last characters of each part `[A-Za-z0-9_]`, repo at most 96 characters, and the ref is also rejected when it contains `--` or `..` or ends in `.git`. Otherwise `../..` or `a/..` escape the model path and `splash serve` dies on argparse ("server exited immediately").
**Files:** `internal/launcher/backend_splash.go`, `internal/launcher/backend_splash_test.go`, `internal/launcher/backend.go`, `internal/launcher/server.go`, `internal/launcher/server_test.go`
**Read first:** `internal/launcher/backend_llamacpp.go — LlamaCpp, BuildServerArgs; internal/launcher/backend.go — ManagedLLMServer, apiKeyHolder, RegisterLLMServer, specContextSize; internal/launcher/backend_http.go — authedGet, openAIModelList`
**Tests:** `backend_splash_test.go` table tests:
- `HealthCheck`: ready with Splash header → ok; ready without header → err; 503 → err; llama-server-shaped `/health`-only server (404 on `/ready`) → err.
- `StartingUp`: true/false cases.
- `BuildServerArgs`: exact slices with and without context, host/port and extra_args ordering.
- `BuildServerEnv`: with and without a key.
- `ResolveModel`: valid and invalid refs; the invalid cases include `../..`, `a/..`, `a--b/c`, `x/y.git`, a part starting or ending in `.` or `-`, and a 97-character repo.
- `ParamSpecs`: labels.
- `server_test.go` (`TestStartManagedServerBinaryInstallHint`): with the binary absent from PATH, a `binaryInstallHinter`'s text reaches the "server binary not found" error, and llamacpp's message is unchanged (`server binary not found: llama-server`).
**Acceptance:** `go build ./... && go test ./internal/launcher/ -run 'Splash|BinaryInstallHint' -count=1`
**Commit:** `feat(launcher): add Splash LLM Server`

## 2. Refuse a Splash model that is not installed — ✅ DONE (2026-09-24)
NOTES (2026-09-24): the existing TestSplashResolveModel is no longer parallel, and its valid refs now resolve against a temp hub that holds each ref installed, because resolving now reads the hub env vars through t.Setenv.

**What:** Recast at the regression check (2026-09-24). Depends on item 1.
**Goal:** `Splash.ResolveModel` returns the repo ID only when some `<hub>/models--<owner>--<repo>/snapshots/*/manifest.json` exists. It never looks up `splash` on `PATH`.
- When the model is missing, the error names `splash serve --model <owner/repo>` as a one-time install run in a terminal.
**Approach (assumed at the header base):**
- Resolve the hub per the header rule (`$HF_HUB_CACHE`, else `$HF_HOME/hub`, else `$XDG_CACHE_HOME/huggingface/hub`, else `~/.cache/huggingface/hub`) via `os.Getenv` / `os.UserHomeDir`.
- No root, `release.json` or `EvalSymlinks` logic.
- No `lookPath` seam; tests use `t.Setenv` and temp dirs.
- `ResolveProfile` runs in `validateAll` and discovery, so a not-installed profile appears as a config warning and is skipped by discovery, the same as a llamacpp profile with a missing `.gguf`. This is intended.
**Regression guard.** (a) ResolveModel is install-only: a Splash profile resolves wherever the HF cache is readable, independent of PATH; the PATH hint lives on the start path (item 1). (b) Installed also requires `<hub>/models--<owner>--<repo>/refs/splash/*/<rev>` for a `<rev>` whose snapshot holds `manifest.json`: `_download_snapshot` fetches `manifest.json` before the weights, and `prepare` writes that ref (`_retain_snapshot_ref`) only after `resolve_snapshot` has finished and verified the download. (c) Every hub test sets `HF_HUB_CACHE`, `HF_HOME`, `XDG_CACHE_HOME` and `HOME` with `t.Setenv`, `""` for the unused ones; the resolver treats an empty variable as unset.
**Files:** `internal/launcher/backend_splash.go`, `internal/launcher/backend_splash_test.go`
**Read first:** `internal/launcher/config.go — ResolveProfile; internal/launcher/discovery.go — matchProfileName; internal/launcher/cli.go — cmdUnload; internal/launcher/menu.go — doShowConfig;
internal/launcher/backend_llamacpp.go — ResolveModel; internal/launcher/backend_llamacpp_test.go — TestLlamaCppResolveModel;
~/Repos/splash/install/models.py — _download_snapshot, _retain_snapshot_ref`
**Tests:** temp-dir hubs (each installed fixture holds `snapshots/<rev>/manifest.json` plus `refs/splash/<x>/<rev>`; every case sets all four env vars with `t.Setenv`) covering:
- `HF_HUB_CACHE` hit → ok;
- `HF_HOME` fallback;
- `XDG_CACHE_HOME` fallback;
- home default (`~/.cache/huggingface/hub`);
- an empty `HF_HUB_CACHE` with `HF_HOME` set → `HF_HOME` fallback (empty means unset);
- snapshot dir without `manifest.json` → error containing `splash serve --model`;
- `manifest.json` present with no `refs/splash` entry → error containing `splash serve --model`;
- empty ref → `""`, nil, with no lookup.
**Acceptance:** `go test ./internal/launcher/ -run 'Splash' -count=1`
**Commit:** `feat(launcher): refuse Splash models that are not installed`

## 3. Keep llamacpp from claiming a Splash server — ✅ DONE (2026-09-24)
NOTES (2026-09-24): re-derived from the assumption that `isSplashResponse` still had to be added to `backend_splash.go` — item 1 already landed it there, so that file is unchanged and llamacpp reuses the existing helper.
NOTES (2026-09-24): consequential edit — llama-launcher.TDD.md: the identification paragraph's "bare 503 on /health" wording (StartingUp second pass) made incomplete by the new Splash-header exclusion in `LlamaCpp.StartingUp`.
NOTES (2026-09-24): ADR-0010's "bare 503 on /health" sentence left as a historical decision record; the plan's at-base site list did not name it.

**What:** Depends on item 1. Splash answers `/health` with `{"status":"ok"}` even while loading. `LlamaCpp.HealthCheck` accepts that, so `identifyBackend` (alphabetical, llamacpp first) and the `auto_stop_server` sweep would treat a Splash server as llamacpp, and could stop it on re-activation, which breaks ADR-0007.
**Goal:** `LlamaCpp.HealthCheck` and `LlamaCpp.StartingUp` both return not-llamacpp for any response carrying `Server: Splash`, and a real llama-server `/health` response is still accepted.
**Approach (assumed at the header base):** In `backend_llamacpp.go`, reject in both `HealthCheck` and `StartingUp` when `resp.Header.Get("Server")` has the `Splash` prefix. Share one small helper with `backend_splash.go` (for example `isSplashResponse(*http.Response) bool`) so both backends use the same marker. Update the `HealthCheck` comment that lists LM Studio's exclusion.
**Regression guard.** Add a Splash-shaped server case to `TestIdentifyBackend` in `server_test.go`, asserting it is identified as `splash`, not `llamacpp`. The `StartingUp` half needs a fixture that bites: `/health` 503 plus `Server: Splash` must give `StartingUp` false (it returns true at base). Every comment or doc line that describes llamacpp identification by the `/health` status body gets the Splash exclusion; find them with `grep -rn 'status":"ok"\|status field' internal/launcher llama-launcher.TDD.md docs/adr` (at base: `backend_lmstudio.go` `HealthCheck`, `llama-launcher.TDD.md` §5.3 table and §12.1 `TestLlamaCppHealthCheck` row).
**Files:** `internal/launcher/backend_llamacpp.go`, `internal/launcher/backend_llamacpp_test.go`, `internal/launcher/backend_splash.go`, `internal/launcher/server_test.go`, `internal/launcher/backend_lmstudio.go`, `llama-launcher.TDD.md`
**Read first:** `internal/launcher/backend_llamacpp.go — HealthCheck, StartingUp; internal/launcher/backend_llamacpp_test.go — TestLlamaCppHealthCheck, TestLlamaCppStartingUp; internal/launcher/server.go — identifyBackend, startingUp; internal/launcher/server_test.go — TestIdentifyBackend; internal/launcher/backend_lmstudio.go — HealthCheck`
**Tests:**
- In `backend_llamacpp_test.go`: a Splash-shaped server (`/health` 200 `{"status":"ok"}` plus `Server: Splash`) → `HealthCheck` errors and `StartingUp` is false.
- In `backend_llamacpp_test.go`: `/health` 503 plus `Server: Splash` → `StartingUp` is false.
- In `server_test.go`: a Splash-shaped case in `TestIdentifyBackend` → identified as `splash`, not `llamacpp`.
- Existing llama-server cases stay green.
**Acceptance:** `go test ./internal/launcher/ -run 'LlamaCpp|Splash|IdentifyBackend' -count=1`
**Commit:** `fix(launcher): stop llamacpp health check from claiming Splash servers`

## 4. Config template: Splash server and example profile — ✅ DONE (2026-09-24)
NOTES (2026-09-24): the sampling-defaults comment in the defaults block now reads "(llamacpp only)" instead of "(llamacpp)", so it matches the new note that Splash ignores sampling params; the matrix footnotes for batch_size/flash_attn now start with "lmstudio:" so they stay clear next to the new splash column.

**What:** Depends on item 1.
**Goal:** The example config generated by `GenerateExampleConfig` documents `splash`:
- It is listed in the `servers:` block, disabled by default.
- It appears in the supported-servers header and the default-ports comment.
- The param/backend matrix shows Splash honouring only `context_size`, and notes that sampling params are ignored.
- A commented example profile uses `model: incoai/Qwen3.8-27B-Splash`, with `extra_args` showing `--default-reasoning-effort`.
- The `models_dir` comment still says llamacpp only.
**Approach (assumed at the header base):** Edit the embedded `internal/launcher/defaults/config.yaml`. Its tests (`config_test.go` generation or parse tests) must still parse and validate it. Update any test that pins the server count or list.
**Regression guard.** Example-config comments describing Splash setup use the wrapper wording and the HF-cache install check, never "symlink". The Goal list is not closed: every comment that enumerates servers, per-server model formats, `extra_args` scope or per-server `api_key` effect names splash (`SPLASH_API_KEY`); find them with `grep -n -i "three\|lmstudio\|llamacpp only\|what the key does" internal/launcher/defaults/config.yaml`. No parse/validate test exists at base (`TestGenerateExampleConfig` only checks non-empty), so add one.
**Files:** `internal/launcher/defaults/config.yaml`, `internal/launcher/config_test.go`
**Read first:** `internal/launcher/defaults/config.yaml — servers block, profile-fields comment, api-key comment, param matrix; internal/launcher/config.go — GenerateExampleConfig, LoadConfigNotify; internal/launcher/config_test.go — TestGenerateExampleConfig; internal/launcher/defaults/embed.go — ExampleConfig`
**Tests:** a new test: `GenerateExampleConfig` to a `t.TempDir` path, then `LoadConfigNotify(path, nil)` must succeed and `cfg.Servers["splash"]` must exist with `Enabled == false`.
**Acceptance:** `go test ./internal/launcher/ -run 'Config|Example' -count=1`
**Commit:** `docs(config): add Splash to the example config`

## 5. MCP adapter accepts `splash` targets — ✅ DONE (2026-09-24)

**What:**
**Goal:** `llama-launcher-mcp` accepts `target: "splash"`. Its validation error and its `start_server` description list the four server names.
**Approach (assumed at the header base):** Add `"splash"` to `knownBackends` in `cmd/llama-launcher-mcp/validate.go` and update the name list in `validateTarget`'s error. Name llamacpp and splash as the managed servers that need a profile in the `start_server` description in `cmd/llama-launcher-mcp/main.go`. The adapter must not import the internal package (ADR-0008).
**Files:** `cmd/llama-launcher-mcp/validate.go`, `cmd/llama-launcher-mcp/validate_test.go`, `cmd/llama-launcher-mcp/main.go`
**Read first:** `cmd/llama-launcher-mcp/validate.go — knownBackends, validateTarget; cmd/llama-launcher-mcp/validate_test.go — TestValidateTarget; cmd/llama-launcher-mcp/main.go — start_server tool Description, targetArgs`
**Tests:** `validate_test.go`: `splash` is accepted, and the unknown-name error text lists it.
**Acceptance:** `go test ./cmd/llama-launcher-mcp/ -count=1`
**Commit:** `feat(mcp): accept splash as a server target`

## 6. Splash integration test — ✅ DONE (2026-09-24)
NOTES (2026-09-24): the profile is resolved through `Config.ResolveProfile` (a one-profile config), not a hand-built `ResolvedProfile` as in the llamacpp suite, so the Hugging Face cache install check runs for real; liveness checks use `IsProcessAlive` and `signalGroup(pid, 0)` (process seam), so the file vets under GOOS=windows.
NOTES (2026-09-24): ran live against `incoai/Qwen3.8-27B-Splash` (splash on PATH, no llama-server running): TestSplashLifecycle PASS in 37s. Starting was never observed: a manual `splash serve` probe got connection refused on /ready until the model had loaded, then 200 straight away — this Splash build binds its port only after loading, so no `/ready` 503 is ever served (contrary to the plan's Splash facts); the test logs this instead of failing, as the Goal allows ("when possible").

**What:** Depends on items 1–3.
**Goal:** `internal/launcher/integration_splash_test.go` (build tag `integration`) runs one full cycle against a real Splash, and skips cleanly when `splash` is not on `PATH` or `INTEGRATION_MODEL_SPLASH` (an installed `owner/repo`) is unset. The cycle:
- resolve a profile on a free port;
- start;
- wait until `HealthCheck` passes, observing Starting (`StartingUp` true) along the way when possible;
- check that `ListRunningModels` reports the repo ID;
- confirm `LlamaCpp.HealthCheck` rejects the address;
- stop via the launcher's normal stop path (SIGTERM);
- assert the port is free and no process with the start PID or its group survives within the stop window.
**Approach (assumed at the header base):** Mirror `integration_llamacpp_test.go`: `mustFindBinary`-style skip, `freePort`, a per-test `t.TempDir` log dir, and a Splash-specific healthy timeout of 3 minutes. The Makefile's `test-integration` 5-minute timeout is enough for an installed model. Add the env var to the TDD in item 8, not here.
**Regression guard.** Liveness checks on the start PID and its group go through the process seam (`signalGroup(pid, 0)`, `IsProcessAlive(pid)`, as `killServerOnCleanup` does), never `syscall.Kill` directly: `make cross` runs `GOOS=windows go vet -tags=integration ./internal/launcher/`, and `syscall.Kill` does not exist on windows.
**Files:** `internal/launcher/integration_splash_test.go`
**Read first:** `internal/launcher/integration_llamacpp_test.go — TestLlamaCppLifecycle, killServerOnCleanup; internal/launcher/integration_test.go — mustFindBinary, freePort, waitForHealthy; internal/launcher/server.go — Stop, IsProcessAlive; internal/launcher/process_windows.go — signalGroup`
**Tests:** the file itself. The executor runs it only when `splash` is on PATH, the model is installed, and no llama-server is holding GPU memory. Otherwise it records a dated NOTES line saying it was compiled only.
**Acceptance:**
- `go vet -tags=integration ./internal/launcher/`
- `GOOS=windows go vet -tags=integration ./internal/launcher/`
- `INTEGRATION_MODEL_SPLASH=incoai/Qwen3.8-27B-Splash go test -tags=integration -count=1 -timeout 10m -run Splash ./internal/launcher/` (skip allowed as above)
**Commit:** `test(launcher): add Splash integration test`

## 7. Domain docs: CONTEXT, ADR, skill, facade doc — ✅ DONE (2026-09-24)
NOTES (2026-09-24): CONTEXT.md Load/Unload verb also updated for `splash` (no API load; the model goes in `--model` at start), since it enumerated `llamacpp` as the only start-argument server; doc.go Platforms lists `splash serve` among the unstarted managed servers and adds Splash to the ErrUnsupported sentence (per the plan's rejected guard); two doc.go paragraphs re-wrapped to keep line width.

**What:** Recast at the regression check (2026-09-24). Depends on item 2.
**Goal:**
- `CONTEXT.md` names `splash` in the LLM Server term.
- The Model term lists the Splash format (a Hugging Face `owner/repo` of an installed Splash package).
- The Start/Stop and Starting verbs describe Splash: fork-and-detach; Starting while `/ready` is 503.
- New `docs/adr/0014-splash-models-must-be-installed.md` records the ratified call: the launcher refuses models that aren't installed, decides by the Hugging Face cache check (`snapshots/<rev>/manifest.json` plus `refs/splash/*/<rev>`, item 2 guard b) rather than Splash's install root because the `splash` on PATH may be a wrapper, and never triggers downloads, because the pre-bind download window is invisible and unstoppable.
- `skills/manage-llm-server/SKILL.md` and `launcher/doc.go` list Splash.
- `AGENTS.md` (the AI-agent entry point) covers Splash, per the guard below.
**Approach (assumed at the header base):** Follow the existing ADR format (read 0010 for its shape). Rule for prose: every sentence that enumerates the LLM Servers or says "three" is updated. Find them with `grep -rn -i "three\|lmstudio\|LM Studio" CONTEXT.md skills/manage-llm-server/SKILL.md launcher/doc.go`.
**Regression guard.** Add AGENTS.md to Files and Goal — AGENTS.md is the AI-agent entry point (a gitignored local CLAUDE.md forwards to it via `@AGENTS.md`); if at execution time it enumerates the LLM Servers or local setup prerequisites, add Splash there; otherwise add one line naming the supported LLM Servers (llamacpp, ollama, lmstudio, splash) with pointers to CONTEXT.md and llama-launcher.TDD.md. Never rewrite the rest of AGENTS.md (the user authors it).
**Files:** `CONTEXT.md`, `docs/adr/0014-splash-models-must-be-installed.md`, `skills/manage-llm-server/SKILL.md`, `launcher/doc.go`, `AGENTS.md`
**Read first:** `CONTEXT.md — LLM Server, Model, Start/Stop, Starting terms; launcher/doc.go — Platforms section (ErrUnsupported paragraph); skills/manage-llm-server/SKILL.md — intro line;
internal/launcher/process_windows.go — requireProcessControl; AGENTS.md`
**Tests:** none (docs). `go vet ./launcher/` covers `doc.go`.
**Acceptance:** `go vet ./launcher/`, and `for f in CONTEXT.md docs/adr/0014-*.md skills/manage-llm-server/SKILL.md launcher/doc.go AGENTS.md; do grep -qi splash "$f" || echo "MISSING $f"; done` prints nothing.
**Commit:** `docs: add Splash to the domain docs and record ADR-0014`

## 8. TDD and README

**What:** Depends on items 1–6.
**Goal:**
- `llama-launcher.TDD.md` covers Splash in these sections: §1, §4.2 `servers` / api-key bullets, §4.3 `DefaultAddr` fallback, §4.4 model resolution (the installed check), §5.1 diagram, §5.2 source files, §5.3 interface plus the "Health Check Discrimination" table (the Splash row and llamacpp's `Server: Splash` rejection), §5.4 external dependencies (`splash` on PATH), §6.2 starting, §8 argument assembly, §12.1 unit tests, §12.5 integration (`INTEGRATION_MODEL_SPLASH`), and §15 MCP.
- `README.md` covers Splash in `### Backends` (name, `127.0.0.1:8000`, `owner/repo` format, "install once via `splash serve --model …`", the PATH symlink), `### API keys`, `### CLI commands`, `## Building` and `## Architecture`.
**Approach (assumed at the header base):** Rule for prose: every passage that enumerates the servers, says "three", or describes per-server model formats, health checks or param support is updated. Find them with `grep -n -i "three\|lmstudio\|LM Studio\|ollama" llama-launcher.TDD.md README.md`.
**Regression guard.** README/TDD Splash setup text uses the wrapper wording (e.g. `printf '#!/bin/sh\nexec "$HOME/Repos/splash/splash" "$@"\n' > ~/.local/bin/splash && chmod +x ~/.local/bin/splash`) and the HF-cache install check, never "symlink". Restate the §5.3 general rule (superseding `llama-launcher.TDD.md` §5.3 "all discrimination must be body-based") to allow positive identification by a response header, noting status codes alone are still insufficient and that Splash needs the header because its `/health` body matches llama-server's. §5.4 is a Go-module table: record `splash` on PATH in §4.2 / §6.2 instead, or add a separate runtime-binaries note covering all four servers. Bead `llama-launcher-readme-requirements-host-binaries` (README Requirements omits `lsof`) is adjacent but not owned — if a runtime-binaries note is added, it may absorb it; close the bead only if it does.
**Regression guard.** README/TDD Splash setup text also notes that a launchd-started or MCP-adapter PATH must include the wrapper's directory (e.g. ~/.local/bin).
**Files:** `llama-launcher.TDD.md`, `README.md`
**Read first:** `llama-launcher.TDD.md — §5.3 Health Check Discrimination table and general rule, §4.2 api-key bullets, §4.4, §8 flag table, §12.5 env-var table; README.md — Backends table, API keys table, Building (integration paragraph)`
**Tests:** none (docs).
**Acceptance:** `grep -c -i splash llama-launcher.TDD.md README.md` (both > 0), plus a manual read of the discrimination table.
**Commit:** `docs: document the Splash LLM Server in TDD and README`

## 9. Documentation sweep for Splash

**What:** Depends on items 1–8.
**Goal:** No human- or AI-facing doc, godoc comment, CLI/MCP help string, or code comment that enumerates the LLM Servers, says "three" servers/backends, or describes per-server model formats/setup/health checks omits Splash — outside archival material (`docs/plans/`, `docs/plans/archived/`, `docs/handoffs/`, `docs/reviews/`, `docs/architecture-reviews/`, `docs/skill-runs/`, the root historical `backend-tests-plan.md`, existing ADRs 0001–0013, released CHANGELOG sections).
**Approach (assumed at the header base):** Find sites with `grep -rn -i -E "three (llm )?(servers|backends)|lm ?studio|lmstudio" --include='*.md' --include='*.go' --include='*.yaml' .` excluding the archival paths (including `backend-tests-plan.md`), and fix each straggler items 1–8 did not already cover. Known candidates at write time:
- `internal/launcher/server.go` comments naming "Ollama, LM Studio" as non-`LiveParamsQuerier` backends — Splash is also not one;
- `internal/launcher/discovery.go` Starting comment naming only llama-server's 503;
- `internal/launcher/keymigrate.go` example string is cosmetic — leave.
**Regression guard.** Comment/doc edits only — no code behaviour change; if a site would need a code change, leave a dated NOTES line instead.
**Regression guard.** The known-candidate list is not closed. Rule: every comment/doc describing the Starting window or a 503/health signal covers Splash (Starting while `/ready` is 503). Find them with `grep -rn -E '503|/health' --include='*.go' --include='*.md' internal launcher cmd README.md CONTEXT.md skills` and review each hit (at base: `README.md:226`, `internal/launcher/backend.go` `StartupProber` doc, `internal/launcher/server.go` `startManagedServer`); "e.g." examples may stay.
**Files:** `internal/launcher/server.go`, `internal/launcher/discovery.go`, `internal/launcher/backend.go`, `README.md`, plus any straggler the greps find.
**Read first:** `internal/launcher/server.go — liveParamDrift, startManagedServer; internal/launcher/discovery.go — RunningInstance.Starting, probeInstance;
internal/launcher/backend.go — LLMServer, StartupProber; internal/launcher/backend_http.go — openAIModelList; internal/launcher/menu.go — modelDisplayName`
**Tests:** `go build ./... && go vet ./...`
**Acceptance:**
- Both greps above, reviewed so every remaining non-archival hit also names Splash or is not an enumeration.
- `go build ./... && go vet ./...`
**Commit:** `docs: cover Splash in every remaining doc surface`
