# vLLM server, menu mouse, launchd autostart

**Goal:** Make vLLM a supported managed LLM Server; let the interactive menu take the mouse (click selects, wheel moves); add an `autostart` command that installs a launchd agent starting a Profile at login.
**Date:** 2026-11-17
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** 95cd3fe

**Regression check (2026-11-17, 95cd3fe):**
- 1: guard folded
- 2: guard folded
- 3: guard folded; supersedes adr-0015-decision.md's "Splash only" bullet; yields to ADR-0015's match conditions
- 4: guard folded
- 5: guard folded (writer decision on the shared default address)
- 6: guard folded (writer decision on the read API)
- 7: guard folded
- 8: guard folded
- 9: guard folded (writer decision on absolute log paths)
- 10: guard folded; supersedes TDD §16.6 Platform contract and ui_poll_windows.go's "every CLI subcommand" comment

**Sources:**
- `.capcom/issues/vllm-server.md`, `menu-mouse-ignored.md`, `launchd-autostart.md`
- `llama-launcher.TDD.md` §3.1, §4.2, §5.3, §5.4, §6.2, §7, §12.1, §15
- capcom Notes `adr-0003`, `adr-0010`, `adr-0012`, `adr-0014`, `adr-0015`, `glossary`

**Ratified design calls** (user, 2026-11-17):
- **Menu mouse:** hand-rolled SGR mouse reporting on the existing `x/term` raw mode; no bubbletea.
- **Autostart surface:** `autostart install <profile>` writes `~/Library/LaunchAgents/<label>.plist` and bootstraps it with `launchctl`; `autostart remove <profile>` boots it out and deletes the file; `autostart print <profile>` writes the plist to stdout. install/remove are macOS-only and refuse elsewhere with `ErrUnsupported`; print works on every OS.
- **vLLM mode:** managed fork, restart-per-Profile like llamacpp/Splash: `vllm serve <model>`; unload = stop; `context_size` → `--max-model-len`, `parallel` → `--max-num-seqs`; API key via `VLLM_API_KEY` env; default address `127.0.0.1:8000`.
- **vLLM Model refs:** a Hugging Face `owner/repo` id or an absolute local path, passed as-is; no install check (vLLM downloads itself; first start bounded by `startup_max_wait`).

**Standing requirements:**
- skills: coding-standards
- Use glossary terms (LLM Server, Profile, Model, Load/Unload) in code, docs and messages.
- Each item's user-visible change gets a CHANGELOG entry via its sidecar.
- `make cross` must stay green: darwin-only code sits behind build tags with a non-darwin stub.

**Out of scope:**
- A vLLM `LiveParamsQuerier` (drift stays Model-name only), multi-Model vLLM, driving vLLM's download.
- Mouse in the still-loading popup and the numbered fallback; mouse drag/motion; scrolling long lists.
- launchd `KeepAlive` / supervision; Linux systemd units.

## 1. vLLM backend — ✅ DONE (2026-11-17)

NOTES (2026-11-17): consequential edit — internal/launcher/backend_http.go: made necessary by backend_vllm.go's ListRunningModels delegating to openAIModelList (its comment said "all three adapters").
NOTES (2026-11-17): the shared validateHFRepoID / isHFRepoIDPart / hfMaxRepoLen stay in backend_splash.go (renamed from validateSplashRepoID / isSplashRepoIDPart / splashMaxRepoLen) rather than a new file, since the item's Files list names no new file for them.
NOTES (2026-11-17): vLLM's HealthCheck, like ListRunningModels, dials a wildcard addr over loopback (probeAddr), as Splash does; the plan did not specify this.

**What:**
**Goal:** `GetLLMServer("vllm")` returns a `ManagedLLMServer` with display name `vLLM`, `DefaultAddr` `127.0.0.1:8000`, binary `vllm`, argv `serve <model> --host <h> --port <p> [--max-model-len N] [--max-num-seqs N] <extra_args…>`, env carrying `VLLM_API_KEY` when a key is set, `ParamSpecs` = `[Context size, Parallel]`, a `ModelLister`, an implementation of `binaryInstallHinter`, and a `HealthCheck` that passes only for a server answering `GET /health` 200 and `GET /version` 200 with a JSON non-empty `version` string, and not carrying a `Server: Splash` header.
**Regression guard.** `HealthCheck` first does an authed `GET /v1/models` and maps 401/403 through `authFailedErr`, as `(*LMStudio).HealthCheck` does (backend_lmstudio.go:30-39): vLLM's api-key middleware guards only `/v1/*`, so `/health` and `/version` alone never surface ADR-0010's AuthFailed row. `validateHFRepoID` takes the server name (or the caller wraps a bare error) so Splash keeps "invalid splash model %q: want a Hugging Face id owner/repo" (backend_splash.go:311). The install hint is the `binaryInstallHinter` interface (`BinaryInstallHint()`, backend.go:261), which `startManagedServer` reads.
**Approach (assumed at the header base):** New `internal/launcher/backend_vllm.go`, registered in `init()` via `RegisterLLMServer`, embedding `apiKeyHolder`; modelled on `backend_splash.go` (`LoadModel`/`UnloadModel`/`TryStart`/`TryStop` no-ops; restart-per-Profile via `loadProfileManaged`). `ListRunningModels` uses `openAIModelList`. `ResolveModel` accepts an absolute path that exists, else validates an HF `owner/repo` id; extract `validateSplashRepoID`/`isSplashRepoIDPart` into a shared `validateHFRepoID` (one reason to change: HF's id grammar) called by both backends — no install-cache check for vLLM. The install hint says to put `vllm` on `PATH` (e.g. `uv tool install vllm`) and that a launchd or MCP-adapter `PATH` must include its directory. Use `authedGet` / `backendClient` / `healthCheckTimeout`; no new HTTP helper.
**Files:** internal/launcher/backend_vllm.go, internal/launcher/backend_vllm_test.go, internal/launcher/backend_splash.go, internal/launcher/backend_splash_test.go
**Read first:** internal/launcher/backend_splash.go — Splash.HealthCheck, validateSplashRepoID; internal/launcher/backend.go — binaryInstallHinter, apiKeyHolder; internal/launcher/backend_http.go — authedGet, authFailedErr, openAIModelList; internal/launcher/backend_splash_test.go — TestSplashResolveModel
**Tests:** `backend_vllm_test.go` in the style of `backend_splash_test.go`: `TestVLLMRegistered` (interface assertions including `srv.(binaryInstallHinter)` as `TestSplashRegistered` does, DisplayName, DefaultAddr); `TestVLLMHealthCheck` with httptest fakes — vLLM-shaped passes; llama-server-shaped `/health` JSON, LM Studio-shaped (`/version` 200 with no `version` field), Ollama-shaped, Splash-header and dead address fail; a fake answering 401 only on `/v1/*` wraps `ErrAuthFailed`; `TestVLLMBuildServerArgs`, `TestVLLMBuildServerEnv` (key never in argv), `TestVLLMResolveModel` (HF id, absolute path, missing path, relative path, bad id), `TestVLLMParamSpecs`. Splash's existing ResolveModel tests stay green after the extraction, and `TestSplashResolveModel` gains a case pinning "invalid splash model".
**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher/ -run 'VLLM|Splash'`
**Commit:** `feat(vllm): add vLLM as a managed LLM Server`

## 2. Other LLM Servers never claim a vLLM address — ✅ DONE (2026-11-17)

NOTES (2026-11-17): only LM Studio's health check needed a guard; llamacpp (empty `/health` body), Ollama (`/` is 404) and Splash (no `Server: Splash` header) already reject the vLLM fake, which the shared-port discovery test confirms by asserting exactly one `vllm` instance with all five LLM Servers enabled at the address.
NOTES (2026-11-17): the TDD's §7 discrimination table (lmstudio row) and §12.1 test descriptions (`TestLMStudioHealthCheck`, `TestDiscoverRunningInstances_*`) do not yet mention the vLLM exclusion; item 5 owns both ("§7 detection (vLLM's `/version` discriminator and LM Studio's exclusion), §12.1 test list"), so they are left to it.
NOTES (2026-11-17): the `/version` shape check is written inline in `(*LMStudio).HealthCheck`, matching its existing llamacpp and Ollama exclusions, rather than shared with `(*VLLM).checkVersion` in backend_vllm.go, which the item's Files list does not name.

**What:** Depends on item 1.
**Goal:** `identifyBackend` and config-driven discovery name a vLLM server `vllm` — never `lmstudio`, `llamacpp`, `ollama` or `splash` — and still name each of those correctly with vLLM registered.
**Regression guard.** Discovery probes a backend only at addresses the config assigns it (`discoveryTargets`, discovery.go:173), so the `discovery_test.go` case configures `lmstudio`, `llamacpp`, `ollama` and `splash` as enabled at the same address as `vllm` (the shared-port setup) and asserts exactly one instance there, with Backend `vllm`.
**Approach (assumed at the header base):** `identifyBackend` (server.go) probes registered backends in sorted name order, so `lmstudio` is tried before `vllm` and its `HealthCheck` (`/v1/models` 200, minus llama.cpp/Splash and Ollama look-alikes) passes a vLLM server. Add a third exclusion to `(*LMStudio).HealthCheck`: `/version` returning 200 with a JSON non-empty `version` string means vLLM. Confirm llamacpp's `/health` `status` check, Ollama's `/` body check and Splash's header check already reject a vLLM fake; add a guard only where a test shows one does not.
**Files:** internal/launcher/backend_lmstudio.go, internal/launcher/backend_lmstudio_test.go, internal/launcher/server_test.go, internal/launcher/discovery_test.go
**Read first:** internal/launcher/backend_lmstudio.go — LMStudio.HealthCheck; internal/launcher/server.go — identifyBackend, probeConcurrently; internal/launcher/discovery.go — discoveryTargets, DiscoverRunningInstances; internal/launcher/server_test.go — TestIdentifyBackend; internal/launcher/backend_lmstudio_test.go — TestLMStudioHealthCheck; internal/launcher/cli_test.go — startingCfg
**Tests:** `TestLMStudioHealthCheck` gains a "vLLM-shaped server is not LM Studio" case. `TestIdentifyBackend` gains "vLLM is identified as vllm, not lmstudio" and keeps every existing subtest green. A `discovery_test.go` case with a vLLM fake at an address configured for `vllm` and for every other LLM Server (shared port), asserting exactly one instance there, with Backend `vllm`.
**Acceptance:**
- `go test ./internal/launcher/ -run 'LMStudio|IdentifyBackend|Discover|VLLM'`
**Commit:** `fix(lmstudio): do not mistake a vLLM server for LM Studio`

## 3. A loading vLLM is visible and stoppable — ✅ DONE (2026-11-17)

NOTES (2026-11-17): vLLM does not answer HTTP 503 while loading; it builds its engine before it listens, so a loading vLLM refuses connections. `StartupProber` was therefore not implemented, as the approach's "if" allowed; `LoadingProcessFinder` alone covers the window.
NOTES (2026-11-17): consequential edit — internal/launcher/server.go: made necessary by vLLM implementing LoadingProcessFinder (the start-path comment named the process-table lookup as for a loading Splash only)
NOTES (2026-11-17): consequential edit — internal/launcher/process_unix.go: made necessary by vLLM implementing LoadingProcessFinder (listProcesses' comment said it backs the search for a loading Splash only)
NOTES (2026-11-17): consequential edit — internal/launcher/process_windows.go: made necessary by vLLM implementing LoadingProcessFinder (listProcesses' comment said only a loading Splash stays invisible on windows)
NOTES (2026-11-17): the consequences Note's title and its "Only Splash addresses pay this cost" sentence were also amended, because vLLM addresses now pay the same ps cost.

**What:** Depends on item 1. Extends ADR-0015's process-table match from Splash to vLLM: a `vllm serve` process whose API does not answer yet is a Starting instance (ADR-0010).
**Goal:** `(*VLLM).LoadingPID(addr)` returns the PID of a `vllm serve` process whose `--port` (default `8000`) matches the address's port while no HTTP server answers there, and 0 otherwise; Splash's `LoadingPID` still never matches a `vllm serve` process and vLLM's never matches `splash serve`.
**Regression guard.**
- Match when the word before `serve` has basename `vllm` (as `isSplashServeCommand` does): a pip/uv `vllm` is a shebang script whose kernel argv (`withTrueArgv`) is `…/bin/python3 …/bin/vllm serve …`. The item yields to ADR-0015's match conditions (adr-0015-decision.md:7-17): PGID == PID, last `--port` == port (default 8000), last `--host` == host when present; an absent `--host` is vLLM's all-interfaces default and matches any host.
- "While no HTTP server answers" is `loadingPID(b, addr)`'s listener gate (server.go:356-373), never an HTTP probe inside `LoadingPID`; the table legs run on a pure `vllmLoadingPID(procs, addr, defaultAddr)`, the listener leg non-parallel with `processTable`/`addrHasListener` pinned (the `pinLoadingSplash` pattern).
- Amend every prose naming the process-table lookup or `LoadingProcessFinder` as Splash-only — superseding adr-0015-decision.md:19 ("Splash only") — found with `grep -rn 'only Splash\|Splash only\|LoadingProcessFinder\|loading Splash' .capcom internal/launcher/*.go` (README/TDD fall to item 5).
**Approach (assumed at the header base):** Implement `LoadingProcessFinder` on the vLLM backend with `processTable()` and `lastFlagValue` (backend_splash.go); add `isVLLMServeCommand(args)` recognising `vllm serve …` and `python … -m vllm.entrypoints.openai.api_server …` launch forms (argv[0] basename `vllm`, or a python argv carrying `vllm.entrypoints`). If vLLM answers HTTP 503 while loading, also implement `StartupProber` for that. Amend `.capcom/knowledge/decisions/adr-0015/adr-0015-consequences.md` with a dated line: the match now also covers vLLM's launch forms.
**Files:** internal/launcher/backend_vllm.go, internal/launcher/backend_vllm_test.go, .capcom/knowledge/decisions/adr-0015/adr-0015-consequences.md, .capcom/knowledge/decisions/adr-0015/adr-0015-decision.md, .capcom/knowledge/decisions/adr-0010/adr-0010-amend-0924.md, .capcom/knowledge/glossary/glossary-verbs/starting.md, internal/launcher/backend.go, internal/launcher/discovery.go
**Read first:** internal/launcher/backend_splash.go — Splash.LoadingPID, splashLoadingPID, isSplashServeCommand, lastFlagValue; internal/launcher/server.go — loadingPID, addrHasListener, processTable; internal/launcher/server_test.go — pinLoadingSplash
**Tests:** `TestVLLMLoadingPID` table-driven over the pure `vllmLoadingPID(procs, addr, defaultAddr)`: `vllm serve m --port 8001` at `:8001` matches; `…/bin/python3 …/bin/vllm serve m --host 127.0.0.1 --port 8001` matches; default port; a non-leader (PGID ≠ PID) does not; a `vllm` process on another host does not; an absent `--host` matches any host; a `splash serve` process does not; a `vllm` process on another port does not. A non-parallel leg through `loadingPID(b, addr)` with `processTable`/`addrHasListener` pinned: a listening address returns 0. `TestSplashLoadingPID` unchanged and green.
**Acceptance:**
- `go test ./internal/launcher/ -run 'VLLM|Splash'`
- `capcom check`
**Commit:** `feat(vllm): find a loading vLLM by its process`

## 4. vLLM in the default config and the MCP adapter — ✅ DONE (2026-11-17)

**What:** Depends on item 1.
**Goal:** The generated default config documents vLLM (server block `vllm: false`, default port, API-key note, parameter applicability column, `server` field enum, an example Profile), and the MCP adapter accepts `vllm` as a backend name in validation and tool descriptions.
**Regression guard.** `validate_test.go:187` (`TestEndToEndMutatingToolsRejectInjection`'s `stop_server` target `"vllm"`) is among the cases switched to `tgi`, and `TestValidateTargetUnknownNameListsBackends` (validate_test.go:65) adds `vllm` to its name loop; Acceptance runs the whole adapter package. The config.yaml rule is every site that enumerates or counts the server types, found with `grep -n -iE 'four|splash' internal/launcher/defaults/config.yaml` (config.yaml:3 "Four server types are supported").
**Approach (assumed at the header base):** `internal/launcher/defaults/config.yaml` — every site listing the four server types (grep `splash` in it). `cmd/llama-launcher-mcp/validate.go` `knownBackends` and the `validateTarget` error text; `cmd/llama-launcher-mcp/main.go` `start_server` description ("managed backends (llamacpp, splash, vllm) require a profile"). `validate_test.go` currently asserts `vllm` is unknown — switch those cases to a still-unknown name (e.g. `tgi`) and add a `vllm`-accepted case. `internal/launcher/backend_test.go` `TestGetLLMServer_Known` lists `vllm`.
**Files:** internal/launcher/defaults/config.yaml, cmd/llama-launcher-mcp/validate.go, cmd/llama-launcher-mcp/validate_test.go, cmd/llama-launcher-mcp/main.go, internal/launcher/backend_test.go
**Read first:** cmd/llama-launcher-mcp/validate.go — knownBackends, validateTarget; cmd/llama-launcher-mcp/validate_test.go — TestValidateTargetUnknownNameListsBackends, TestEndToEndMutatingToolsRejectInjection; cmd/llama-launcher-mcp/main.go — start_server tool Description; internal/launcher/config_test.go — TestGenerateExampleConfig_LoadsWithSplashDisabled; internal/launcher/backend_test.go — TestGetLLMServer_Known; internal/launcher/defaults/config.yaml — servers block
**Tests:** the validate_test changes above, including `TestEndToEndMutatingToolsRejectInjection`'s `"vllm"` target switched to `tgi` and `vllm` in `TestValidateTargetUnknownNameListsBackends`'s name loop; `TestGetLLMServer_Known` includes `vllm`; existing default-config load tests stay green (the generated config still parses and validates).
**Acceptance:**
- `go build ./...`
- `go test ./cmd/llama-launcher-mcp/`
- `go test ./internal/launcher/ -run 'Validate|GetLLMServer|Default|Config'`
**Commit:** `feat(vllm): list vLLM in the default config and MCP adapter`

## 5. Document vLLM — ✅ DONE (2026-11-17)

NOTES (2026-11-17): the TDD vLLM argv/env subsection is §8.2 (beside §8.1 Splash); the rule's grep also hit glossary `start.md` and `skills/release-notes-llama-launcher/prompts/_llama-launcher.md`, both of which list the servers, so both gained vLLM; `starting.md` already named vLLM (item 3) and needed no edit.
NOTES (2026-11-17): README's integration-test list and the TDD §12.5 integration tables are left without vLLM: there is no vLLM integration test.
NOTES (2026-11-17): retry — README's Windows row in Supported platforms now reads "stopping a running llama-server, Ollama, Splash or vLLM server is refused the same way".

**What:** Depends on items 1–4.
**Goal:** README, TDD, the `llm-server` glossary Note, `skills/manage-llm-server/SKILL.md` and `launcher/doc.go` list vLLM alongside `llamacpp`, `ollama`, `lmstudio` and `splash`, with README's vLLM subsection under Backends stating the ratified calls (managed restart-per-Profile, Model refs, parameter mapping, `VLLM_API_KEY`, `PATH` under launchd, first-start download time).
**Regression guard.**
- README's and TDD's default-address rows state that vLLM and Splash both default to 127.0.0.1:8000, so running both side by side needs a distinct `port` on one Profile (otherwise ADR-0006's address-slot rule applies)
- The locator is case-insensitive — `grep -rniE 'splash'` over the same paths — and the rule covers every count of the servers ("four", e.g. README.md:415). The TDD gains a §8.x vLLM subsection beside §8.1 Splash for the argv/env mapping.
- **Files:** is the floor and the rule the scope; glossary `model.md`, `load.md` and `starting.md` enumerate the servers and are listed.
**Approach (assumed at the header base):** Rule: every doc line that enumerates the supported LLM Servers or the managed ones (grep `-rnE 'splash' README.md llama-launcher.TDD.md AGENTS.md skills/ launcher/doc.go .capcom/knowledge/glossary`) gains vLLM. TDD: §4.2 schema, §5.3 backend list, §5.4 binaries table row `vllm`, §6.2 managed start, §7 detection (vLLM's `/version` discriminator and LM Studio's exclusion), §12.1 test list. README: backend table plus a `#### vLLM` subsection after `#### Splash`.
**Files:** README.md, llama-launcher.TDD.md, AGENTS.md, skills/manage-llm-server/SKILL.md, launcher/doc.go, .capcom/knowledge/glossary/glossary-language/llm-server.md, .capcom/knowledge/glossary/glossary-language/model.md, .capcom/knowledge/glossary/glossary-verbs/load.md, .capcom/knowledge/glossary/glossary-verbs/starting.md
**Read first:** README.md — Backends table, `#### Splash` subsection, Architecture paragraph; llama-launcher.TDD.md — §5.3 detection table, §5.4 Runtime binaries table, §8.1 Splash; skills/manage-llm-server/SKILL.md — intro server list; launcher/doc.go — process-control paragraph
**Tests:** none (docs).
**Acceptance:**
- `grep -c -i vllm README.md llama-launcher.TDD.md AGENTS.md skills/manage-llm-server/SKILL.md launcher/doc.go .capcom/knowledge/glossary/glossary-language/llm-server.md` — each nonzero
- `grep -n '^#### vLLM' README.md` — one hit
- `go build ./... && capcom check`
**Closes:** vllm-server
**Commit:** `docs(vllm): document vLLM as a supported LLM Server`

## 6. Decode SGR mouse input — ✅ DONE (2026-11-17)

NOTES (2026-11-17): consequential edit — llama-launcher.TDD.md: made necessary by the new file internal/launcher/ui_input.go, added as a row to the §internal/launcher file table beside ui.go
NOTES (2026-11-17): menu.go needed no change — stillLoadingWatch.readKey keeps its keyCode signature and readKeyTimeout stays a keyCode wrapper; no CHANGELOG entry because nothing user-visible changes until mouse reporting is switched on (escMouseOn is defined but not yet emitted)
NOTES (2026-11-17): read API for items 7 and 8 — readInput() / readInputTimeout(d) / readInputRaw() (inputEvent, error) / waitForInput() (inputEvent, error, skips keyMouseIgnored); keyCode wrappers readKey, readKeyTimeout, readKeyRaw, waitForAnyKey
NOTES (2026-11-17): side effect of decoding one event at a time — several bytes arriving in one read (typed fast or pasted) now each decode as their own key, where the old 4-byte read turned any multi-byte read into keyNone; a truncated CSI is ended early at a control byte so it never swallows a following Enter or Ctrl+C, and a CSI is capped at 32 bytes

**What:**
**Goal:** A pure decoder turns stdin bytes into input events: every existing key (arrows, Enter, Esc, Ctrl+C, q, digits) decodes as before; `ESC[<0;x;yM` is a left-click press with 1-based x,y; `ESC[<64;…M` / `ESC[<65;…M` are wheel up/down; any other SGR mouse report (release `m`, motion, other buttons) decodes to an ignored-mouse event; a sequence split across reads is reassembled, and several events in one read come out one at a time; a lone ESC ending the bytes read is Esc.
**Regression guard.**
- item 6 fixes the read API once for items 7 and 8 — inputEvent-returning variants of every raw-mode reader the menu and popups use (the readKeyTimeout path for selectMenu, the readKeyRaw path for the confirm popup, the waitForAnyKey path) plus keyCode wrappers for callers needing no coordinates; stillLoadingWatch.readKey keeps its keyCode signature; items 7 and 8 consume this API and never reshape it
- A lone ESC ending the bytes read decodes as `keyEscape` (complete). `readKeyTimeout` skips `pollStdin` only while pending holds a complete event; while it holds an incomplete prefix it polls, and its timeout still returns `keyNone` — so Esc keeps cancelling `selectMenu` and the still-loading popup (ui.go:55-60, menu.go:770-772).
- Any other `ESC[` sequence is consumed through its final byte (0x40–0x7E) and decodes to `keyNone`, as today's 4-byte read does (ui.go:72-81); ESC followed by a non-`[` byte stays `keyNone`, not `keyEscape` — so PgUp's `5~` never selects row 5.
**Approach (assumed at the header base):** In `internal/launcher/ui.go` (or a new `ui_input.go`), add `type inputEvent struct{ key keyCode; x, y int }`, new codes `keyClick`, `keyWheelUp`, `keyWheelDown`, `keyMouseIgnored`, and `decodeInput(buf []byte) (ev inputEvent, consumed int, complete bool)`. `readInput()` reads stdin into a package-level pending buffer (≥64 bytes per read) and decodes from it; `readKeyTimeout` skips `pollStdin` while pending bytes remain. `readKey()` stays as a wrapper returning `ev.key` for callers that need no coordinates. Add `escMouseOn = "\x1b[?1000h\x1b[?1006h"` and `escMouseOff = "\x1b[?1006l\x1b[?1000l"` beside the other `esc*` constants. Binding: every existing `readKey` caller treats `keyMouseIgnored` as a no-op — in particular `drawConfirmPopup` must not cancel on it and `waitForAnyKey` must not dismiss on it.
**Files:** internal/launcher/ui.go, internal/launcher/ui_input.go, internal/launcher/ui_test.go, internal/launcher/menu.go
**Read first:** internal/launcher/ui.go — readKey, readKeyTimeout, waitForAnyKey, drawConfirmPopup; internal/launcher/menu.go — stillLoadingWatch, waitStillLoading; internal/launcher/ui_poll_darwin.go — pollStdin; internal/launcher/menu_test.go — scriptedWatch
**Tests:** table-driven `TestDecodeInput`: each key; click, wheel up/down, release, motion, right-click; multi-digit coordinates; split sequence (incomplete → `complete=false`); two events in one buffer; lone ESC (complete, `keyEscape`); `ESC[5~`, `ESC[3~`, `ESC[15~`, `ESC[C` and `ESC O A` each consumed whole and decoding to `keyNone`. `TestSwitchModel_ConfirmPopup` gains a `keyMouseIgnored` that does not cancel.
**Acceptance:**
- `go test ./internal/launcher/ -run 'DecodeInput|ConfirmPopup|StillLoading|Popup'`
- `GOOS=linux go vet ./internal/launcher/ && GOOS=windows go vet ./internal/launcher/`
**Commit:** `feat(menu): decode SGR mouse input`

## 7. Click and wheel in menus and pickers — ✅ DONE (2026-11-17)

NOTES (2026-11-17): menuGeometry holds only the first body row and the frame's column span (borders included); the item count the approach listed is dropped because menuItemAt bounds the row by len(items); newMenuGeometry(frame, startRow, startCol, frameWidth) derives it from the drawn Frame's Title and Header
NOTES (2026-11-17): the Up/Down separator-skipping loops moved into moveSelection(items, selected, step), which the wheel shares; selectMenu's defer now writes escMouseOff + escCursorShow (menuRawRestore) before term.Restore, where it printed escCursorShow after it; selectMenu's doc comment now says headerFn runs on every input event and describes mouse handling
NOTES (2026-11-17): TestMenuItemAt renders the real Frame for each layout and checks every rendered line, so the hit-test is pinned to Frame.Render's layout; TDD/README wording on mouse use is left to item 8, which owns those docs; footer hints ("↑↓ select · enter …") unchanged
NOTES (2026-11-17): a selectMenu that ignores a mouse event still re-runs headerFn and redraws, as for any other key — a click outside a row costs one extra headerFn call and redraw for its press and, when the terminal reports it, its release (liveStatusHeaderFn's backend probe stays throttled to refresh_duration)

**What:** Depends on item 6.
**Goal:** While `selectMenu` holds raw mode, mouse reporting is on and is switched off before raw mode is restored on every return path; a left click on a selectable row selects it and returns it as Enter would; a click on a separator, the title, the header, the footer or outside the frame does nothing; a wheel notch moves the selection one selectable row as Up/Down do.
**Regression guard.** `selectMenu` reads through item 6's `readInputTimeout(timeout) inputEvent`; `readKeyTimeout` keeps its `keyCode` signature as the wrapper `stillLoadingWatch.readKey` is assigned (menu.go:551, 727), so menu.go does not change. The restore order is tested through a named helper, e.g. `menuRawRestore(w io.Writer)` writing `escMouseOff` then `escCursorShow`, called from `selectMenu`'s defer — `selectMenu` itself does `MakeRaw` on the real stdin (ui.go:106-118) and has no injection seam.
**Approach (assumed at the header base):** `selectMenu` writes `escMouseOn` after `term.MakeRaw` and `escMouseOff` in the same deferred restore as `escCursorShow`. Record the last draw's geometry (first body row, frame column span, item count) alongside `lastMenuRect` and hit-test with a pure `menuItemAt(geom menuGeometry, items []menuItem, x, y int) (int, bool)`: body row = `startRow + 1 + (title?1:0) + (header? len(header)+1 : 0) + i`, using the header length of the latest draw (it changes between refresh ticks). Wheel reuses the Up/Down separator-skipping code. Callers (`runStoppedMenu`, `runLoadedMenu`, `runIdleMenu`, `doSwitchModel`, `doStopServer`, `doUnloadModel`, `askKeyMigration`) need no change.
**Files:** internal/launcher/ui.go, internal/launcher/ui_test.go
**Read first:** internal/launcher/ui.go — selectMenu, lastMenuRect, readKeyTimeout, firstSelectable; internal/launcher/frame.go — Frame.Render; internal/launcher/menu.go — stillLoadingWatch; internal/launcher/ui_test.go — TestRenderMenuRowsMultibyteLabelColumn, stripEscapes
**Tests:** `TestMenuItemAt`: first/last selectable row, separator row, title/header/footer rows, left/right of the frame, header of varying length, centered origin. `TestMenuRawRestore`: `menuRawRestore` writes `escMouseOff` before `escCursorShow` to the writer it is given.
**Acceptance:**
- `go test ./internal/launcher/ -run 'MenuItemAt|SelectMenu|RenderMenuRows'`
**Commit:** `feat(menu): select menu rows with the mouse`

## 8. Mouse in popups, and docs — ✅ DONE (2026-11-17)

NOTES (2026-11-17): showPopup and confirmPopup each wrap their whole popup (draw, wait, deferred clear) in one call to a new withRawStdin(fn) helper, so escMouseOn and escMouseOff are both written inside raw mode and Off lands before cooked mode returns, as the Goal requires; showPopup's wait is waitForAnyKey, which now only runs awaitDismiss(readInput) on the already-raw stdin, and readInputRaw is unchanged and nests inside withRawStdin. When raw mode cannot be entered the popup is no longer drawn-and-cleared but skipped, with the same visible result (confirmPopup still cancels)
NOTES (2026-11-17): readKeyRaw and waitForInput became unused (confirmPopup now passes readInputRaw; waitForAnyKey calls awaitDismiss(readInput)) and were deleted as dead code
NOTES (2026-11-17): drawPopupAndWait's comments no longer name raw mode as the cause of a wait failure, since showPopup now enters raw mode before it is called
NOTES (2026-11-17): drawPopup returns a new screenRect{row, col, width, height} with a contains(x, y) hit-test; lastMenuRect keeps its anonymous struct type untouched. The rect assertion lives in a sibling test, TestDrawPopupReturnsDrawnRect, which parses the cursor positions drawPopup writes
NOTES (2026-11-17): menu.go needed no change — enterStillLoadingRawMode never writes escMouseOn
NOTES (2026-11-17): TDD §5.2's ui.go row now also describes item 7's selectMenu mouse handling (menuRawRestore, menuItemAt, moveSelection), which item 7 left to this item; the ui_input.go row's readKeyRaw reference became readInputRaw; the ui.go row names waitForAnyKey/awaitDismiss/readInput as drawPopupAndWait's wait and says showPopup and confirmPopup each wrap the whole popup in one withRawStdin call (showPopup skips, confirmPopup cancels, without raw mode)
NOTES (2026-11-17): in the confirm popup a wheel notch is ignored (neither confirms nor cancels), as other non-deciding keys are

**What:** Depends on item 7.
**Goal:** In the confirm popup a left click inside the box confirms as Enter does and a click outside is ignored; in an any-key popup a click or wheel dismisses it and a release never does; mouse reporting is on only while these popups hold raw mode and off before it is restored. TDD §3.1 and README's Interactive mode describe mouse use.
**Regression guard.**
- `drawConfirmPopup`'s `wait` becomes `func() (inputEvent, error)`, filled by item 6's inputEvent variant of the `readKeyRaw` path (this item does not reshape item 6's API); the `TestSwitchModel_ConfirmPopup` fake returns `inputEvent{key: …}`.
- The popups write `escMouseOn`/`escMouseOff` to their `w` around `wait()`, Off before `escCursorShow` in the deferred clear; the any-key loop is an injectable `awaitDismiss(read func() inputEvent)` that `waitForAnyKey` calls, and the tests drive it (`waitForAnyKey`/`readKeyRaw` read the real stdin).
- The docs rule: every TDD/README sentence describing the menu's input keys or the popups' close/confirm keys, found with `grep -niE 'arrow-key|any key|only on Enter|enter switch' llama-launcher.TDD.md README.md` (e.g. TDD §2 Goals, the §5.2 `ui.go` row plus a `ui_input.go` row, the deps table).
**Approach (assumed at the header base):** `drawPopup` returns the box rect it drew (row, col, width, height) instead of discarding it; `drawConfirmPopup` hit-tests `keyClick` against it. `waitForAnyKey` and `readKeyRaw` write `escMouseOn`/`escMouseOff` around their raw section. The still-loading popup (`enterStillLoadingRawMode`) and the numbered fallback never enable mouse reporting. TDD §3.1: amend the opening "arrow-key navigation" sentence and the switch-confirm paragraph; README `### Interactive mode`: one short paragraph on click/wheel.
**Files:** internal/launcher/ui.go, internal/launcher/ui_test.go, internal/launcher/menu.go, llama-launcher.TDD.md, README.md
**Read first:** internal/launcher/ui.go — drawPopup, drawConfirmPopup, readKeyRaw, waitForAnyKey, drawPopupAndWait; internal/launcher/ui_test.go — TestSwitchModel_ConfirmPopup, TestDrawPopupAndWaitRestoresCursor; internal/launcher/menu.go — enterStillLoadingRawMode
**Tests:** `TestSwitchModel_ConfirmPopup` (fake returning `inputEvent`) gains click-inside confirms and click-outside is ignored; `TestDrawPopupAndWaitRestoresCursor` (or a sibling) asserts the drawn rect and that `w` carries `escMouseOff` before `escCursorShow` after the wait; `TestAwaitDismiss`: `keyMouseIgnored` does not dismiss, a click or wheel does.
**Acceptance:**
- `go test ./internal/launcher/ -run 'Popup|ConfirmPopup|StillLoading|AwaitDismiss'`
- `grep -niE 'arrow-key|any key|only on Enter|enter switch' llama-launcher.TDD.md README.md` — every hit that describes the input keys also describes mouse use
**Closes:** menu-mouse-ignored
**Commit:** `feat(menu): take the mouse in popups`

## 9. `autostart print` renders a launchd plist — ✅ DONE (2026-11-17)

NOTES (2026-11-17): `TestBuildLaunchAgentSpec` is split into `TestBuildLaunchAgentSpec_*` functions (program/label, slug, relative paths, unknown Profile, PATH table, missing server binary, hub variables); `env` is a `getenv func(string) string` that supplies PATH and the forwarded variables, while binary lookups use the process PATH.
NOTES (2026-11-17): every `buildLaunchAgentSpec` failure exits 2 (unknown or unresolvable Profile, plus the near-impossible `os.Executable` failure), matching `load`'s resolve path.
NOTES (2026-11-17): completion offers `install print remove` as the plan says, though `install` / `remove` only land with item 10; README/TDD docs for `autostart` are left to item 10 ("and docs"); only `printUsage` gained the `autostart print` line.

**What:**
**Goal:** `llama-launcher autostart print <profile>` writes a valid launchd plist to stdout (exit 0) for an existing Profile and exits 2 for an unknown one; the plist has Label `com.github.airiclenz.llama-launcher.<profile-slug>`, `ProgramArguments` `[<launcher>, --config, <absolute config path>, load, <profile>]`, `RunAtLoad` true, no `KeepAlive`, `AbandonProcessGroup` true, `StandardOutPath`/`StandardErrorPath` `<log_dir>/autostart-<profile-slug>.log`, and `EnvironmentVariables` with `PATH` (the current `PATH` with the launcher's directory and the Profile's managed server binary's directory prepended when missing) plus `HF_HOME` / `HF_HUB_CACHE` / `XDG_CACHE_HOME` when set.
**Regression guard.**
- buildLaunchAgentSpec makes StandardOutPath/StandardErrorPath absolute (log_dir with ~ expanded, then filepath.Abs) — item 10's directory creation and launchd both rely on it
- The config path is `filepath.Abs`'d too: `cfg.ConfigPath` and `cfg.LogDir` are only tilde-expanded (config.go:374-376), and launchd runs the job from `/`.
- `EnvironmentVariables` carries every variable `splashHubDir` reads — `HF_HUB_CACHE`, `HF_HOME`, `XDG_CACHE_HOME` — when set, so a launchd `load` resolves the same hub cache as the shell.
**Approach (assumed at the header base):** New `internal/launcher/autostart.go`: `launchAgentSpec` built by `buildLaunchAgentSpec(cfg, configPath, profileName, env)` (resolves the Profile via `ResolveProfile`; slug via `logProfileSlug`; server binary via `ManagedLLMServer.ServerBinary` + `exec.LookPath`, skipped when not found or not managed) and a pure `renderLaunchAgentPlist(spec) string` with XML escaping. Launcher path: `exec.LookPath("llama-launcher")` when `os.SameFile` with `os.Executable()` (keeps Homebrew's stable symlink), else `os.Executable()`. `cmdAutostart(cfg, configPath, args)` in `cli.go`; `printUsage` line; `completions.go` adds `autostart` with subcommands `install print remove` then Profile names (`completionProfiles`).
**Files:** internal/launcher/autostart.go, internal/launcher/autostart_test.go, internal/launcher/cli.go, internal/launcher/cli_test.go, internal/launcher/completions.go, internal/launcher/completions_test.go
**Read first:** internal/launcher/cli.go — Run, printUsage; internal/launcher/config.go — ResolveProfile, parseConfig; internal/launcher/log_cleanup.go — logProfileSlug; internal/launcher/backend_splash.go — splashHubDir; internal/launcher/completions.go — completionProfiles; internal/launcher/cli_test.go — captureStdout
**Tests:** `TestRenderLaunchAgentPlist` (golden-style field checks; `plutil -lint` on darwin via a temp file, skipped elsewhere); XML-escaping of a Profile name with `&<`; `TestBuildLaunchAgentSpec` (PATH prepends, `HF_HOME` / `HF_HUB_CACHE` / `XDG_CACHE_HOME` only when set, a relative config path and `log_dir` made absolute, unknown Profile error); `TestCmdAutostart_Print` via `captureStdout`; completions test.
**Acceptance:**
- `go test ./internal/launcher/ -run 'Autostart|LaunchAgent|Complet'`
**Commit:** `feat(cli): add autostart print for a launchd plist`

## 10. `autostart install` / `remove`, and docs — ✅ DONE (2026-11-17)

NOTES (2026-11-17): the LaunchAgents directory is not a swappable package-level variable: `launchAgentPath` derives it from `os.UserHomeDir()`, and every install/remove test isolates it with `t.Setenv("HOME", t.TempDir())` (via the shared `isolateAutostart` helper in cli_test.go, which also swaps `runLaunchctl`). `runLaunchctl` lives in the untagged autostart.go so the shared helper compiles on every OS.
NOTES (2026-11-17): `autostart remove` does not resolve the Profile (the label is built from the typed name's slug), so an agent outlives a Profile deleted from the config; with no plist installed it prints "No launch agent is installed for <profile>" and exits 0. A launchctl or disk failure exits 3, a usage error or unresolvable Profile on install exits 2.
NOTES (2026-11-17): consequential edit — llama-launcher.TDD.md: §5.2 gains rows for autostart.go and autostart_darwin.go / autostart_other.go (item 9 left the TDD to this item); a new §3.4 names the plist fields.
NOTES (2026-11-17): launcher/doc.go and README "Supported platforms" still say linux does "everything": both describe the library facade, which does not expose `autostart`, so they were left unchanged.

**What:** Depends on item 9.
**Goal:** On macOS `autostart install <profile>` writes the plist to `~/Library/LaunchAgents/<label>.plist` (replacing and booting out an existing one first), runs `launchctl bootstrap gui/<uid> <path>` — which, with `RunAtLoad`, runs `load <profile>` at once, so install activates the Profile now — and prints the path, that the Profile is being loaded now, and that it starts at login; `autostart remove <profile>` runs `launchctl bootout gui/<uid>/<label>` (a not-loaded agent is not an error), deletes the file, and says the running server is left alone (`stop` stops it). On other OSes both exit 3 with an error wrapping `ErrUnsupported`. README documents print/install/remove; TDD gains the command.
**Regression guard.**
- A `bootout` exiting 3 or 113, or printing "No such process" / "Could not find specified service", means not loaded and is ignored (this host: exit 3, "Boot-out failed: 3: No such process"). install runs `os.MkdirAll(log_dir, 0o700)` and `MkdirAll(~/Library/LaunchAgents)` before bootstrap (today only `createLogPath` creates log_dir).
- Install loads the Profile at once (`RunAtLoad`), replacing what holds its address and sweeping under `auto_stop_server`: `skills/manage-llm-server/SKILL.md` Safety rule 1 lists `autostart install` among the state-changing calls.
- The `launchctl` runner and the LaunchAgents dir are package-level seams; every `cli_test` install/remove case swaps the runner and sets a temp `HOME` (`t.Setenv`). The non-darwin stub test sits in `autostart_other_test.go` (`//go:build !darwin`; CI runs ubuntu-latest).
- Supersedes TDD §16.6 "Platform contract" (linux "Everything", the "four windows refusal paths", the build-tagged file families) and ui_poll_windows.go:4-7 ("every CLI subcommand … keeps working"): update every such site (`grep -n 'Everything\|build-tagged\|refusal path\|every CLI subcommand' llama-launcher.TDD.md internal/launcher/*.go`), plus the §3.2 completions row (Profile names after `autostart <sub>`).
**Approach (assumed at the header base):** `autostart_darwin.go` implements `installLaunchAgent(spec)` / `removeLaunchAgent(label)` with an injectable `launchctl` runner; `autostart_other.go` (`//go:build !darwin`) returns errors wrapping `ErrUnsupported` (ADR-0012). README: a `### Start at login (macOS)` section under Usage replacing nothing; TDD: the CLI command list and a short subsection naming the plist fields from item 9.
**Files:** internal/launcher/autostart_darwin.go, internal/launcher/autostart_other.go, internal/launcher/autostart_darwin_test.go, internal/launcher/autostart_other_test.go, internal/launcher/autostart.go, internal/launcher/cli.go, internal/launcher/cli_test.go, internal/launcher/ui_poll_windows.go, README.md, llama-launcher.TDD.md, skills/manage-llm-server/SKILL.md
**Read first:** internal/launcher/server.go — ErrUnsupported, loadProfile, createLogPath; internal/launcher/cli.go — activateProfileCLI; internal/launcher/ui_poll_windows.go — requireInteractiveMenu; llama-launcher.TDD.md — §16.6 Platform contract; skills/manage-llm-server/SKILL.md — Safety rules; .github/workflows/ci.yml — make check matrix
**Tests:** darwin: install writes the file to a temp `HOME` holding neither `log_dir` nor `~/Library/LaunchAgents` (both get created) and calls the fake runner with `bootout` then `bootstrap gui/<uid> <path>`; remove calls `bootout` and deletes; bootout "not loaded" tolerated, faked with exit 3 and "Boot-out failed: 3: No such process"; every `cli_test` install/remove case swaps the runner and sets a temp `HOME`. Non-darwin stub test in `autostart_other_test.go` asserting `errors.Is(err, ErrUnsupported)`.
**Acceptance:**
- `go test ./internal/launcher/ -run 'Autostart|LaunchAgent'`
- `GOOS=linux go vet ./internal/launcher/ && GOOS=windows go vet ./internal/launcher/`
**Closes:** launchd-autostart
**Commit:** `feat(cli): install and remove the autostart launchd agent`
