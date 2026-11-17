# P3 features sweep: Ollama context size, log names, per-Profile retention, reload

**Goal:** Ship four clearly scoped P3 features: Ollama Profiles send `context_size`; log names carry the instance port and the Profile; a Profile may override `log_retention`; a `reload` subcommand restarts an instance with its current Profile.
**Date:** 2026-11-17
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** 9fdb457

**Sources:**
- `.capcom/issues/ollama-context-size.md`, `log-name-instance.md`, `per-profile-log-retention.md`, `reload-subcommand.md`
- `llama-launcher.TDD.md` §3.2, §4.2, §5.3, §9
- capcom Notes `adr-0007` (idempotent activation), `glossary`

**Ratified design calls** (user, 2026-11-17):
- **Scope:** the four issues above; `menu-mouse-ignored`, `launchd-autostart`, `vllm-server` stay out.
- **Log owner:** managed logs are named `{backend}-{port}-{profile}-{timestamp}.log` (Profile name made filename-safe); a log with no Profile (`ollama serve`) is `{backend}-{port}-{timestamp}.log` and uses the global retention.
- **Ollama context:** send `options.num_ctx` on load; TDD and README state that clients must send the same `num_ctx` (or the server must run with `OLLAMA_CONTEXT_LENGTH`) for it to hold, since Ollama reloads a model whose request options differ.
- **Ollama inherited context:** an Ollama Profile's context_size counts only when the Profile itself sets it; defaults.context_size is not inherited for Ollama (user, 2026-11-17).

**Standing requirements:**
- skills: coding-standards
- Use glossary terms (LLM Server, Profile, Model) in code, docs and messages.
- Each item's user-visible change gets a CHANGELOG entry via its sidecar.

**Out of scope:**
- `menu-mouse-ignored`, `launchd-autostart`, `vllm-server`.
- Renaming or migrating existing log files; old names are only read.
- A `LiveParamsQuerier` for Ollama (drift detection stays model-name only).

**Regression check (2026-11-17, 9fdb457):**
- 1: recast — Ollama context_size counts only when the Profile sets it; supersedes the CHANGELOG.md 1.4.6 entry removing Ollama's context_size display
- 2: guard folded
- 3: guard folded
- 4: guard folded
- 1 (re-check): guard folded — capability check inside mergeParams so list --json and the context column follow the Profile-only rule; launcher/launcher.go and the defaults-merge doc lines added

## 1. Ollama Profiles send their context size — ✅ DONE (2026-11-17)

NOTES (2026-11-17): renamed TestFormatProfileParams_OllamaShowsNoParams to TestFormatProfileParams_OllamaShowsOnlyContextSize — the old name asserts the behaviour this item reverses; it now also asserts a non-Ollama param (Threads) stays hidden.
NOTES (2026-11-17): TestBuildProfileItems_NoContextColumn's "a context size only Ollama carries does not open the column" subtest became "a defaults-only context size on Ollama does not open the column", and TestBuildSimpleProfileLines_ContextColumn's "no displayable profile" subtest dropped the Ollama Profile's own context_size (it now opens the column); added TestBuildProfileItems_OllamaIgnoresDefaultContextSize, TestMergeParams_OllamaContextSizeFromProfileOnly, TestResolveProfile_OllamaIgnoresDefaultContextSize, TestCmdListJSON_OllamaIgnoresDefaultContextSize and TestOllamaParamSpecs_ListsOnlyContextSize.
NOTES (2026-11-17): README gained a short "#### Ollama" subsection under Backends carrying the num_ctx / OLLAMA_CONTEXT_LENGTH caveat; menu.go's profileContextCells comment ("merged (defaults + profile) context size") left as is — it reads mergeParams, which now applies the rule.

**What:** Recast at the regression check (2026-11-17).
**Goal:** An Ollama Profile's `context_size` reaches Ollama on load as `options.num_ctx` (omitted when unset), and Ollama's `ParamSpecs` returns exactly `[specContextSize]`, so the Profile-list context column and "Show model config" show it for Ollama rows. Only a context_size the Ollama Profile sets itself counts (defaults.context_size is not inherited), so `list --json` omits `context_size` for a defaults-only Ollama Profile and the public `ResolvedProfile.ContextSize` is nil for it.
**Approach (assumed at the header base):** In `internal/launcher/backend_ollama.go`, `(*Ollama).LoadModel` adds `"options": {"num_ctx": n}` to the `/api/generate` payload when `profile.ContextSize != nil`; `(*Ollama).ParamSpecs` returns `[]ProfileParamSpec{specContextSize}` (comment updated). `UnloadModel` is unchanged. Existing menu tests that pin the blank Ollama cell or the empty Ollama spec (e.g. `TestBuildProfileItems_NoContextColumn`'s Ollama-only case, the empty-spec `formatProfileParams` case) are updated to the new behaviour, not deleted. TDD §3.1 (context column paragraph), §5.3 (`ParamSpecs` ownership) and README's Ollama section describe the change and the client `num_ctx` caveat from the header.
**Regression guard.** The Goal states that num_ctx is sent, and the context column / "Show model config" / `list --json` show a value, only when the Ollama Profile sets context_size itself; a defaults-only context_size sends no options key and shows a blank cell. Approach: the resolved Profile's ContextSize for an Ollama Profile comes from the Profile only — decided through an optional backend capability (a small interface in backend.go that Ollama implements, checked inside `mergeParams` keyed on `merged.Server`, which equals `resolveProfileServer`, so `ResolveProfile`, `profileContextCells` (menu.go) and `cmdListJSON` (cli.go) all get it with no menu.go or cli.go edit), never a backend-name check in config.go; add internal/launcher/config.go and config_test.go to Files, with a test that a defaults-only context_size resolves to nil for an Ollama Profile and is still inherited for llamacpp. Also add internal/launcher/cli_test.go to Files and `CmdList` to the Acceptance -run regex (update TestCmdList_ContextColumnAlignment, do not delete it). Replace the closed doc list with the rule "every doc line, comment or config-matrix cell saying Ollama has no context spec / renders a blank cell, and every line describing ResolveProfile / the context column / `list --json` as a plain defaults merge (launcher/launcher.go ResolveProfile doc, README.md ResolveProfile example, TDD §3.1 context-column paragraph)" plus the reviewer's grep (`grep -rniE 'ollama.*(blank|spec list is empty|lists none|empty Ollama spec|no context length)'` plus the matrix), adding internal/launcher/backend.go and internal/launcher/defaults/config.yaml to Files. The item's CHANGELOG sidecar entry states that it deliberately supersedes the 1.4.6 entry (CHANGELOG.md, removal of Ollama's context_size display) now that the value is actually sent, and names the client-visible change: `ResolvedProfile.ContextSize` (and `list --json` `context_size`) is nil/absent for an Ollama Profile that does not set it.
**Files:** internal/launcher/backend_ollama.go, internal/launcher/backend_ollama_test.go, internal/launcher/backend.go, internal/launcher/config.go, internal/launcher/config_test.go, internal/launcher/menu_test.go, internal/launcher/cli_test.go, internal/launcher/defaults/config.yaml, launcher/launcher.go, llama-launcher.TDD.md, README.md
**Read first:** internal/launcher/config.go — mergeParams, ResolveProfile; internal/launcher/backend_ollama.go — LoadModel, ParamSpecs; internal/launcher/backend.go — serverShowsContextSize;
internal/launcher/menu.go — profileContextCells; internal/launcher/cli.go — cmdListJSON; internal/launcher/cli_test.go — TestCmdList_ContextColumnAlignment
**Tests:** httptest server in `backend_ollama_test.go` capturing the load body: with `context_size` set it carries `options.num_ctx`; without it there is no `options` key. A test asserts `ParamSpecs` labels equal `["Context size"]`. Menu tests show the Ollama context cell for a Profile-own `context_size`. `config_test.go` (test name contains `Ollama`): a defaults-only `context_size` resolves to nil for an Ollama Profile and is still inherited for llamacpp. `TestCmdList_ContextColumnAlignment` is updated to the new Ollama cell, not deleted. A `buildProfileItems` case (test name contains `ProfileItems`) and a `cmdListJSON` case (test name contains `CmdListJSON`): a defaults-only Ollama Profile gets a blank context cell and no `context_size` key.
**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher/ -run 'Ollama|ProfileItems|SimpleProfileLines|FormatProfileParams|CmdList'`
**Closes:** ollama-context-size
**Commit:** `feat(ollama): send a Profile's context_size as num_ctx on load`

## 2. Log names carry the instance port and the Profile — ✅ DONE (2026-11-17)

NOTES (2026-11-17): `parseLogTimestamp` is replaced at its callers by `parseLogName` (plus a private `parseLogStamp`); `TestParseLogTimestamp` became `TestParseLogName`, alongside new `TestLogName_FileNameRoundTrips`, `TestLogProfileSlug`, `TestCleanupLogs_AgesEveryShape` and `TestFindManagedLogFile_NoLegacyFallbackPastOtherPorts`.
NOTES (2026-11-17): cleanup now ages only names `parseLogName` accepts; a non-launcher `.log` whose second `-` part is not a positive port (e.g. `foo-bar-<stamp>.log`) was aged before and is now left alone.
NOTES (2026-11-17): log times are still written in local time and parsed as UTC (pre-existing, unchanged), so `log_retention` ages are off by the local UTC offset.

**What:**
**Goal:** New logs are named `{backend}-{port}-{profile}-{timestamp}.log` for managed starts and `{backend}-{port}-{timestamp}.log` for `ollama serve`; one parser reads all three shapes (these two plus legacy `{backend}-{timestamp}.log`, ms and second stamps); an instance's log is the newest file for its backend whose port matches, or a legacy file when none matches; cleanup ages every shape.
**Approach (assumed at the header base):** `internal/launcher/log_cleanup.go` owns the format (no new module): a `logName` value (backend, port, profile slug, start time) with a formatter and `parseLogName(filename) (logName, bool)`. Split the stem on `-`: the last two parts are the stamp; after the backend, 0 extra parts = legacy, 1 = port, ≥2 = port + profile (rejoined with `-`). Backend names have no `-`. `logProfileSlug(name)` maps every rune outside `[A-Za-z0-9._-]` to `_`. `parseLogTimestamp` becomes a thin wrapper or is replaced at its callers. `createLogPath(cfg, backend, port, profile)` in `server.go` writes the new name; callers: `startManagedServer`'s call (passes `*profile.Port`, `profile.Name`) and `(*Ollama).TryStart` (port from `splitHostPort(addr)`, profile `""`). `findManagedLogFile` in `discovery.go` takes the instance port and picks by parsed start time, not lexical order (old and new names do not sort together); `fillRuntimeDetails` passes `inst.Port`. TDD §9 (naming, example, how `logs` finds a file) and README's log section describe it.
**Regression guard.** `stillStartingUpErr` (server.go) is a second `findManagedLogFile` caller: it passes the port from `splitHostPort(addr)`. The two `TestCreateLogPath_*` tests in `server_test.go` build their expected/occupied names with the new formatter (same backend, port, Profile as the `createLogPath` call) and parse the stamp via `parseLogName`. Docs: every TDD/README site that spells a log name shape, the log glob or `parseLogTimestamp` — `grep -nE '\{backend\}-|<backend>-|llamacpp-20[0-9]{6}|parseLogTimestamp|lexicographic' llama-launcher.TDD.md README.md`.
**Files:** internal/launcher/log_cleanup.go, internal/launcher/log_cleanup_test.go, internal/launcher/server.go, internal/launcher/server_test.go, internal/launcher/backend_ollama.go, internal/launcher/discovery.go, internal/launcher/discovery_test.go, llama-launcher.TDD.md, README.md
**Read first:** internal/launcher/server.go — createLogPath, startManagedServer, stillStartingUpErr; internal/launcher/discovery.go — findManagedLogFile;
internal/launcher/log_cleanup.go — parseLogTimestamp, cleanupLogs; internal/launcher/backend_ollama.go — (*Ollama).TryStart; internal/launcher/server_test.go — TestCreateLogPath_CollisionRetriesWithoutTruncating
**Tests:** table test for `parseLogName` over all three shapes, both stamp precisions, a Profile with `-` in its name and a non-log file; `logProfileSlug` cases; `createLogPath` writes the new shape; `findManagedLogFile` with mixed legacy/new files and two ports returns the port's newest file; `cleanupLogs` ages and removes files of every shape; `TestCreateLogPath_SameSecondStartsGetDistinctFiles` and `TestCreateLogPath_CollisionRetriesWithoutTruncating` updated as in the guard (the collision test occupies a new-shape name).
**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher/ -run 'Log|ManagedLog|Cleanup'`
**Closes:** log-name-instance
**Commit:** `feat(logs): name log files by instance port and Profile`

## 3. Per-Profile log retention — ✅ DONE (2026-11-17)

NOTES (2026-11-17): cleanupLogs keeps its signature (the regression guard's second option), so cli.go (cmdLogsClean) needed no change and is not in FILES; the per-file variant cleanupExpiredLogs sits beside it, both delegating to a new removeLogs.
NOTES (2026-11-17): Profile log_retention errors are reported in Profile-name order ("profile %q: log_retention must be 0 or positive"), placed right after the global log_retention check in configChecks.

**What:** Depends on item 2.
**Goal:** A Profile may set `log_retention: N` (N ≥ 0); automatic cleanup ages each log whose name carries that Profile's slug against the Profile's value, and every other log against the global value; a value ≤ 0 or unset means keep (never "delete everything"); a negative Profile value is a config error.
**Approach (assumed at the header base):** Add `LogRetention *int yaml:"log_retention,omitempty"` to `Profile` in `config.go` — not to `ProfileParams` (not a server parameter: no defaults tier, no `ParamSpecs`, no drift compare). Validation mirrors the global check, naming the Profile. `autoCleanupLogs` runs when the global value or any Profile override is positive; `cleanupLogs` gains a per-file max-age function built from config: parse with `parseLogName`, look up Profiles by `logProfileSlug(name)`; when several Profiles share a slug, the longest retention wins and any keep (≤ 0) wins over all. Manual `logs clean` (default 7 days, `--days`, `--all`) stays uniform and ignores overrides, as it already ignores the global value. Example config `internal/launcher/defaults/config.yaml` shows a commented Profile override. TDD §4.2 schema, §9.1 and README's config reference describe the override.
**Regression guard.** Goal correction: a Profile without `log_retention` defers to the global value; only an explicitly set Profile value overrides; in a slug collision unset Profiles count as the global value. If `cleanupLogs`' signature changes, its manual caller `cmdLogsClean` (cli.go) passes a constant max-age func — or keep the signature and add the per-file variant beside it. Docs: every TDD/README site naming log_retention or automatic cleanup — `grep -n 'log_retention\|autoCleanupLogs\|automatic cleanup' llama-launcher.TDD.md README.md`.
**Files:** internal/launcher/config.go, internal/launcher/config_test.go, internal/launcher/log_cleanup.go, internal/launcher/log_cleanup_test.go, internal/launcher/cli.go, internal/launcher/defaults/config.yaml, llama-launcher.TDD.md, README.md
**Read first:** internal/launcher/log_cleanup.go — autoCleanupLogs, cleanupLogs, activeLogFiles; internal/launcher/cli.go — cmdLogsClean;
internal/launcher/config.go — Profile, (*Config).configChecks; internal/launcher/defaults/config.yaml — log_retention; internal/launcher/log_cleanup_test.go — TestAutoCleanupLogs_PositiveRetention
**Tests:** config: Profile `log_retention: -1` fails validation, `0` and positive pass. Cleanup: with global 7 and a Profile at 30, a 10-day log of that Profile survives while a 10-day log of another Profile and a legacy log are removed; Profile 0 keeps its logs with global 7; with global 7 and a Profile with no override, a 10-day log of that Profile is removed; global unset with a Profile at 1 removes only that Profile's old logs; running-server logs stay protected.
**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher/ -run 'Config|Log|Cleanup'`
**Closes:** per-profile-log-retention
**Commit:** `feat(config): let a Profile override log_retention`

## 4. `reload` subcommand — ✅ DONE (2026-11-17)

NOTES (2026-11-17): cmdLoad's resolve/LoadProfile/cmdLoadFinish tail moved into activateProfileCLI, shared by load and reload, so reload carries no copy of the load logic (the Loading progress label is the same for both).
NOTES (2026-11-17): a Starting instance and an idle instance (no Model) exit 3 like the unmatched-Profile case; the plan named their messages but not their exit codes.
NOTES (2026-11-17): resolveTargetInstance's verb for logs is "show logs for" (matching cmdLogs' own bare-target listing); logs only calls it with an explicit target, so that line is unreachable from logs today.
NOTES (2026-11-17): refuseAuthFailedUnload reused as-is for reload (doc comment now says "unload or reload"); not renamed, per the no-rename rule.

**What:**
**Goal:** `llama-launcher reload [target]` re-activates the target instance's active Profile with the current config, exactly as `load <profile> --restart` would; with no running instance, an ambiguous target, an unmatched target, or an instance whose active Profile is unknown, it prints a clear error and exits non-zero.
**Approach (assumed at the header base):** In `internal/launcher/cli.go`, dispatch `"reload"` to `cmdReload`: resolve the instance with `resolveTargetInstance` (same target forms as `stop`/`logs`; exit codes mirror `cmdStop`'s for its errors), then `fillRuntimeDetails`/`matchProfileName` as `status` does to get `ActiveProfile`; empty → `no Profile matches the Model running at <addr>` and exit 3. Then `cfg.ResolveProfile`, `LoadProfile(cfg, profile, true, progress)` and `cmdLoadFinish` — no copy of load logic. Add `reload` to `printUsage`, to the completion subcommand list in `completions.go`, and complete enabled backend names after it as for `stop`/`logs`. TDD §3.2 table row, README usage block and `skills/manage-llm-server/SKILL.md`'s command table cover it.
**Regression guard.** Before discovery, reject a `-`-prefixed arg with `unknown flag` (exit 2) and a second positional, as `cmdLoad` does. Drop `fillRuntimeDetails`; read `inst.ActiveProfile` from `resolveTargetInstance`'s result. Before matching: AuthFailed → the `authFailedErr` text, exit 3 (`refuseAuthFailedUnload` pattern); Starting → its own message; `ActiveModel == ""` → `no Model loaded at <addr>`. `resolveTargetInstance` takes the verb for its ambiguity line (`stop` keeps "stop"; doc comment updated). Every doc sentence listing the commands that complete backend names (`grep -n 'after .stop. and .logs.'`) gains `reload`.
**Files:** internal/launcher/cli.go, internal/launcher/cli_test.go, internal/launcher/completions.go, internal/launcher/completions_test.go, llama-launcher.TDD.md, README.md, skills/manage-llm-server/SKILL.md
**Read first:** internal/launcher/cli.go — Run, cmdLoad, cmdLoadFinish, resolveTargetInstance, refuseAuthFailedUnload; internal/launcher/server.go — LoadProfile;
internal/launcher/completions.go — completionSubcommands, commandCandidates
**Tests:** `cmdReload` with no instance → non-zero and message; with an instance whose Profile is unmatched → exit 3 and message; an AuthFailed instance → `authFailedErr` text and exit 3; a Starting instance → its own message; an idle instance (no Model) → `no Model loaded at <addr>`; `reload --x` → `unknown flag`, exit 2; a second positional rejected; with several instances the ambiguity line names reload, not stop. `__complete` lists `reload` and offers backend names after it.
**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher/ -run 'Reload|Complet'`
**Closes:** reload-subcommand
**Commit:** `feat(cli): add reload to re-activate a running instance's Profile`
