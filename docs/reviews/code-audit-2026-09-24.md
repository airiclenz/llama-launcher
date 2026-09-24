# Code Review — llama-launcher — 2026-09-24

**Scope:** the full module — `cmd/llama-launcher-mcp`, `internal/keystore`, `internal/launcher`, the `launcher` public facade, the root entry point and `Makefile` — 74 source files.
**Mission:** a single-user, local-machine Go process manager that starts/stops/loads/unloads four local LLM servers (llama.cpp, Ollama, LM Studio, Splash) as detached background processes via named Profiles, with a TUI menu, a small CLI, an optional MCP control-plane adapter, and a curated public library facade — explicitly not a router or proxy.
**Files reviewed:** 74

## Executive Summary

The most dangerous findings are the two shell-execution seams: `api_key_cmd` is run whole through `sh -c` and the keystore write pipes a shell-parsed command line into `security -i`, so a hostile config file (a cloned repo, a dotfile sync, a pasted snippet) gains code execution as the user on every launch, before any interaction. The optional MCP adapter, the component built to contain a prompt-injectable agent, fuses the CLI's stderr into its JSON tool results on any plaintext-key config — silently returning invalid JSON as success — and caps neither in-flight subprocesses nor two of its own entry paths. The launcher core itself is mostly sound — fail-closed allowlist, strict charset validation, sanitized surfaces and a race-fixed API-key holder hold up — but the local control plane has real holes: auth-refusing servers vanish so the unconditional `stop` refuses, `logs clean --days 0` deletes every non-active log, a TOCTOU PID-reuse window can turn `stop` into an arbitrary kill, and a whitespace-split process-table parser hides a loading Splash. No critical finding remains after checking the single candidate against ADR-0015's documented limitation; the twenty findings are almost all small, independent fixes.

## Intent & Architecture Findings

### High — A server that refuses auth becomes invisible, so the unconditional `stop` refuses `[Intent & Structure + Correctness]`

- **Where:** `internal/launcher/discovery.go:132-145` + `internal/launcher/server.go:228-251` + `internal/launcher/cli.go:350-378`; message gap at `internal/launcher/backend_llamacpp.go:26-45`
- **What:** `probeInstance` drops any instance whose `HealthCheck` errors (with only a `startingUp` fallback), and `identifyBackend` returns `ErrNotRunning` unless a backend's health check passes — so a server answering 401/403 (wrong/rotated `api_key`, key set server-side but missing in config) is indistinguishable from an absent one. On top of that, the llamacpp health check never maps 401/403 through `authFailedErr`, unlike every sibling backend, so even the message that would name the fix ("authentication failed … check api_key in the servers section") is missing on that path.
- **Why it matters:** `stop` — documented unconditional (ADR-0001: "Stop is unconditional… stop means stop") — reports "No server running." while the offending server is up and serving; the launcher's own `BuildServerEnv` doc states a keyed llama-server "rejects client requests without Authorization", so the trigger is routine: start `llama-server --api-key X`, run `stop` with a config carrying a different or missing key. `unload` inherits the same refusal.
- **Fix:** discovery/`identifyBackend` must treat a 401/403 responder as *present-but-auth-failing* — surface the `authFailedErr` message and let the stop path proceed (`stopServerAt` stops by lsof+PID and needs no key) — and map `LlamaCpp.HealthCheck`/`StartingUp` through `authFailedErr` like the sibling backends do.

### High — `logs clean --days 0` deletes every non-active `.log` file `[Intent & Structure]`

- **Where:** `internal/launcher/cli.go:733-739,755` + `internal/launcher/log_cleanup.go:43-55`
- **What:** the digit parse loop accepts `0` even though its own refusal text says "--days value must be a positive integer"; `maxAge` becomes 0 and `cleanupLogs` deletes every `.log` not protected as active — exactly `--all` behaviour. The automatic path encodes the opposite policy: `autoCleanupLogs` (log_cleanup.go:121-132) returns when `LogRetention <= 0`, with the comment "0 must never mean 'delete everything'".
- **Why it matters:** a user typing `--days 0` meaning "no retention" silently destroys the log history. *(independently verified)*
- **Fix:** reject `days == 0` in the parse loop, matching the printed error text; keep `--all` as the only delete-everything spelling.

### High — "Edit config" is macOS-only but offered in every menu variant `[Intent & Structure]`

- **Where:** `internal/launcher/menu.go:568`, plus the Simple fallbacks (`menu.go:952/1038/1087`) and the key-migration prompt
- **What:** the action shells out to `exec.Command("open", cfg.ConfigPath)` unconditionally; on linux the menu renders (`requireInteractiveMenu` returns nil) but the verb fails with a raw `exec: "open": executable file not found`.
- **Why it matters:** violates the "every verb works where its mechanism exists; where it does not, it returns a clean sentinel (`ErrUnsupported`) instead of failing to build" invariant (ADR-0012 / TDD §16.6) — the verb is offered in all six menu variants and the migration prompt, so a linux user hits it the first time they try to edit their config from the menu.
- **Fix:** use `$EDITOR`/xdg-open per platform, or return a sentinel-wrapped error; at minimum don't offer the verb where it cannot work.

### Medium — Two diverged copies of the config validator `[Intent & Structure]`

- **Where:** `internal/launcher/config.go:391-453` (`validate`) vs `internal/launcher/config.go:705-776` (`validateAll`)
- **What:** the load-time fast-fail `validate()` and the collecting `validateAll()` duplicate every check (deprecated fields, servers, api-key sources, `defaults.server` fallback, `log_retention`, `LogDir` default) while only one of them also runs `ResolveProfile` per profile. The drift has already happened once: the 2026-08-14 security plan records that the api-key-source refusals were added to one copy and had to be manually carried to the other.
- **Why it matters:** a check added to one path today is silently missing from `config validate`; the two copies have already produced asymmetric behaviour once.
- **Fix:** `validateAll` should derive from `validate` (collect `validate()`'s error plus the extra profile resolutions), or both should share one check list.

### Medium — The facade's "never writes to stderr" contract is reachably false through the `Config` alias `[Intent & Structure]`

- **Where:** `launcher/doc.go:17` with `launcher/launcher.go` (`type Config = core.Config`) and `internal/launcher/config.go:359-361,382-384`
- **What:** `Config` is a type alias, so the exported core method `Config.Reload()` is callable by library clients; `Reload` delegates to `LoadConfig`, whose default notice sink writes "warning: …" to stderr and re-runs every enabled `api_key_cmd`. The only guard is a prose sentence two screens below the headline contract ("Do not use Config.Reload: it is the CLI's entry point and prints warnings to stderr").
- **Why it matters:** a client that re-reads its config the natural way — a `Reload` method right on the documented `Config` type — gets stderr pollution the `NoticeFunc` design promises to route through callbacks, contradicting the package headline "The library never writes to its host's stderr".
- **Fix:** state the carve-out in the same sentence as the invariant, or drop `Reload` from the surfaced method set by wrapping `Config` instead of aliasing it — structural.

## Critical & High Findings

### High — `api_key_cmd` executed whole via `sh -c` from the config file `[Security]`

- **Where:** `internal/launcher/config.go:578-584` (`keyCommandArgv`), reached via `resolveKeyCommands` (config.go:525-556) from every `LoadConfig`
- **What:** `keyCommandArgv` returns `["sh", "-c", command]` with the string taken verbatim from the config file; the api-key-source refusal checks reject only an empty command or both-sources, never the command's content. It runs on every launch — CLI and menu alike — before any command dispatch, including read-only subcommands.
- **Why it matters:** a hostile config file — cloned repo, dotfile sync, pasted snippet (the bundle's named "malicious repo/config" attacker) — gains local code execution as the user on every `llama-launcher` run, with full login privileges, keychain and SSH-agent access. The 0600 config mode guards against other local users, not the config's own provenance; the migration's own `ReadCmd` lines land in that same file. *(independently verified)*
- **Fix:** run the command through a safe argv split (no shell), or gate the no-shell form behind an explicit opt-in and refuse/escrow the pipeline form; serve the `pass show … | head -1` use case with a quoted-argv resolver or a tiny wrapper.

### High — Keychain write pipes a shell-parsed command line into `security -i` `[Security]`

- **Where:** `internal/keystore/keystore.go:205-216` (`writeCommand`) + `securityWord` (keystore.go:284-290)
- **What:** the macOS keychain write builds `add-generic-password -U -s llama-launcher -a <entry> -w <key>` and pipes it through `security -i`; `securityWord` is a quotes-and-backslashes *escape* function, deliberately not a whitelist, so an entry name or key containing a `"` does not stay one argument — the tool's parser honors double quotes and backslashes.
- **Why it matters:** a hostile config whose `api_key` or entry name is e.g. `x" ; touch /tmp/pwn ; "` runs an injected `security` subcommand under the user's keychain rights during migration (a *Manage* subcommand), before the write-read-back verification executes. The misfire may be detected by the read-back, but it executes first.
- **Fix:** whitelist the key and entry with the `isPlainWord` character class; when a value is not whitelisted, refuse the migration instead of escaping; never hand a non-whitelisted value to `security -i` stdin.

### High — TOCTOU PID-reuse kill on the stop path `[Security]`

- **Where:** `internal/launcher/server.go:333-362` (`stopServerAt`)
- **What:** the listening PID is resolved by `findListeningPID(addr)` and signalled later via `terminatePID`, with health checks and backend probings in between; `IsProcessAlive(pid)` checks only existence, never identity, and nothing re-validates between SIGTERM and SIGKILL.
- **Why it matters:** a hostile server on a configured port (a named attacker in the threat model) — or the MCP `stop` verb, driven unauthenticated from an allowlisted container agent — can accept one request, exit, and have a decoy process reuse the freed PID in the SIGTERM→SIGKILL window; the launcher then sends both signals to a process that was never the server: an arbitrary kill of an unrelated local process. *(independently verified)*
- **Fix:** capture the PID's identity (start time) when the lookup runs — `proc_pidinfo(pid, PROC_PIDTBSDINFO)` on macOS, `/proc/<pid>/stat` field 22 on Linux — and verify it before signalling and again between SIGTERM and SIGKILL; a mismatch means the PID was reused and must not be signalled.

### High — Glob-matched delete of user files in the config directory `[Security]`

- **Where:** `internal/launcher/server.go:1171-1185` (`CleanupLegacyStateFiles`), invoked from cli.go:66 on every launch and by every MCP control verb
- **What:** the cleanup runs `filepath.Glob(dir, "state-*.json")` over `DefaultConfigDir()` and `os.Remove`s every match — no name whitelist, no ownership check, no confirmation. Erasing the launcher's own legacy `state-<backend>.json` names (ADR-0006) needs a fixed set, not an open pattern.
- **Why it matters:** any user file whose name starts with `state-` and ends in `.json` in the config dir — `state-notes.json`, `state-backup-2024.json` from scripts or sync tools — can be deleted; via the MCP adapter's source-IP allowlist, a prompt-injectable container agent can trigger it without touching the terminal. *(independently verified)*
- **Fix:** delete exactly the known legacy names the launcher itself created (an explicit list under ADR-0006's scheme), optionally verifying the file owner is the current uid, instead of globbing an open pattern.

### High — Same-second start reuses the log path and truncates a live server's log `[Correctness]`

- **Where:** `internal/launcher/server.go:1252-1253` (`createLogPath`, format `20060102-150405`) and server.go:104 (`os.Create`)
- **What:** two `start` calls within one second — an `auto_stop_server` + `--restart` loop, or a scripted stop/start — produce the identical path and the second `os.Create` (truncate) truncates the still-running first server's log in place.
- **Why it matters:** the earlier start's shutdown trace, or the crash tail if the new start dies, is lost; `logs`, the start-crash `readLastLines` and status then read the truncated file while the earlier PID is still tracked. *(independently verified)*
- **Fix:** add sub-second precision or a uniquifier to the name, or open with O_APPEND instead of truncate.

### High — Process-table parser splits on whitespace and hides a loading Splash `[Correctness]`

- **Where:** `internal/launcher/server.go:310-313` (`parseProcessTable`) with `internal/launcher/backend_splash.go:86-118` (`splashLoadingPID`)
- **What:** `ps -o pid=,pgid=,command=` output is split on whitespace (`strings.Fields`) and read positionally, so a command line containing a space shifts the fields, and `isSplashServeCommand`/`lastFlagValue` misread them. The realistic breaker is the `--binary`/`server.py` loading form (a Splash checkout under a spaced path, or a `--binary` value with a space); the wrapper/launcher.py forms are position-resilient. ADR-0015 already records the spaced-path limitation as a known consequence — which narrows the trigger, but the loader path itself is not the documented limitation, only "`--host`/`--port` value containing spaces".
- **Why it matters:** a genuinely-loading Splash is invisible to `identifyBackend`/`stop` — the ADR-0015 promise ("a loading Splash is found by its process, not its address") silently fails, so a loading server is not stoppable. *(independently verified)*
- **Fix:** parse `command=`'s standard quoting, keep the existing column list, and add a table test with a spaced path.

### High — The MCP adapter fuses stderr into its JSON tool results `[Intent & Structure + Correctness]`

- **Where:** `cmd/llama-launcher-mcp/config.go:200-204`, with `internal/launcher/cli.go:80-81`
- **What:** on the success/exit-1 path, `run` appends any captured stderr to the stdout content (`text := out; if errOut != "" { text += "\n" + errOut }`). The CLI prints "warning: the api_key for … stored in plain text in …" to stderr on every subcommand whenever a config holds a literal `api_key` without `plaintext_key_ok` — the project's own documented deployment (keys in config, migration offer).
- **Why it matters:** `list_profiles` and `server_status`, whose tool descriptions promise "as JSON", return `[{"name":…}]\nwarning: …` as a *success* result with no error signal — invalid JSON handed to the prompt-injectable agent the adapter exists to contain. Trigger: any plaintext-key config plus any of the four read/mutate verbs. *(independently verified)*
- **Fix:** on exit 0/1, keep stdout verbatim as the sole tool content (append only the truncation notice when the cap hit); carry stderr as a separate content item or drop it, returning it as an error only on exit ≥ 2 as already done.

### High — `ErrStartupTimeout` is not wrapped on the external arm; retries spawn orphan daemons `[Correctness]`

- **Where:** `launcher/launcher.go:118-121` with `internal/launcher/server.go:157-175` (`connectExternalServer`) and `internal/launcher/backend_ollama.go:89-127`
- **What:** on the external arm, the 15 s post-start health wait fails as a plain `"%s not reachable at %s after start attempt: %w"` — no `ErrStartupTimeout`, no PID, no log, no "server was left running" note — while `TryStart` has already forked a detached `ollama serve` (Setsid) that keeps running.
- **Why it matters:** the documented `errors.Is(err, ErrStartupTimeout)` retry-and-observe pattern (launcher/doc.go:51-55) misfires against a stopped Ollama/LM Studio whose cold start exceeds 15 s: the caller treats the load as failed, and each retry spawns another daemon that dies on the port conflict only later, while the first keeps running invisibly with no way to find its PID or log. The managed arm grants 30 s and decorates its timeout with PID+log via `startupTimeoutErr`; the external arm should match. *(independently verified)*
- **Fix:** on the post-start wait timeout, wrap `ErrStartupTimeout` and decorate with the spawned PID and log path exactly as the managed arm does; also surface `TryStart`'s real error (e.g. "ollama binary not found in PATH") instead of discarding it for the generic not-reachable text.

## Medium Findings

### Medium — No bound on in-flight MCP tool subprocesses `[Security]`

- **Where:** `cmd/llama-launcher-mcp/main.go:158` (`cfg.run` from every tool handler) with `config.go:129` (`exec.CommandContext`)
- **What:** every tool call forks a `llama-launcher` subprocess; request bodies and subprocess output are capped (1 MiB each) but concurrency is not.
- **Why it matters:** the attacker this design exists to restrain — a prompt-injectable containerized agent admitted by the allowlist — can drive unbounded parallel host processes: a retry loop on `load_profile` (a call can legitimately run ~5 min) or `tail_log` leaves dozens of resident CLI processes for minutes, exhausting CPU and process table on a single-user machine.
- **Fix:** bound in-flight executions with a semaphore around `c.run` in `newServer` — one acquisition per tool call, released after `cmd.Run` returns — sized 2–4 for the host; optionally make the size a flag. Per-tool rate-limiting is unnecessary; the cap on simultaneous subprocesses is the point.

### Medium — Data race on Ollama's last-started PID and log fields `[Concurrency]`

- **Where:** `internal/launcher/backend_ollama.go:134-135` (writes) with readers at :145-146
- **What:** `TryStart` writes `b.lastPID`/`b.lastLogFile` as plain fields while `LastStartedPID`/`LastStartedLogFile` read them lock-free — on the same struct where a real data race was found and fixed with an RWMutex for the API key (CHANGELOG 1.4.6), leaving these two fields unprotected.
- **Why it matters:** concurrent `Start` verbs for Ollama profiles on *different* addresses both fail their health check and both call the shared singleton's `TryStart` (ADR-0011's per-address serialization does not cover different addresses), racing the `LastStartedPID()` read in `connectExternalServer` (server.go:175-177); `go test -race` flags it, and even without a crash one profile can be reported with the other's PID/log file.
- **Fix:** move the two fields under the existing mutex and have `LastStartedPID`/`LastStartedLogFile` take `RLock`.

### Medium — The menu's memory readout holds its cache mutex across unbounded subprocesses `[Concurrency]`

- **Where:** `internal/launcher/sysmem.go:43-55` (`ReadMemStats`) with the subprocess calls at sysmem.go:60-97, invoked from menu.go:933-937 on every status tick (1 s) and keystroke
- **What:** `ReadMemStats` holds the process-wide `memCacheMu` while running up to four external subprocesses sequentially with no timeout and no context (`sysctl`, `vm_stat`, `ioreg` — plain `exec.Command(...).Output()`); a second caller blocks behind the held mutex even though it only wanted the cache line.
- **Why it matters:** a stalled `vm_stat` or `ioreg -c IOAccelerator` (system memory pressure is exactly when the readout is wanted; ioreg hangs on AGXAccelerator are a known macOS failure mode) freezes the whole menu indefinitely — the call is synchronous inside `selectMenu`'s render loop, and the error is swallowed so nothing surfaces. Every other child in scope is bounded (2 s health probes, 60 s key commands).
- **Fix:** run each command via `exec.CommandContext` under a short `context.WithTimeout`, treat a timeout as skip-this-tick rather than an error, and compute the snapshot into locals before taking the lock only to publish — the mutex must not cover the subprocess span.

### Medium — The documented "cancel with Stop" is exactly the forbidden concurrent call `[Concurrency]`

- **Where:** `launcher/doc.go:37-41` and `launcher/launcher.go:113-116` vs the serialization rule at `launcher/doc.go:78-80` and `internal/launcher/server.go:955`
- **What:** doc.go tells callers to cancel an in-flight `LoadProfile` with `Stop(addr)` ("an instance that is still coming up is discoverable and stoppable"), in the same breath as the rule that concurrent lifecycle calls against the same host:port race each other. The in-flight `loadProfileManaged` health wait has no cancellation channel and keeps polling to its 30 s deadline, then returns `startupTimeoutErr` claiming "it was left running (PID …)" for a server the concurrent Stop already killed.
- **Why it matters:** a user who starts a large-model load and follows the documented advice to cancel blocks another ~30 s and gets an `ErrStartupTimeout` that misdescribes reality — the server is no longer running.
- **Fix:** either have the health wait observe the target address going dark mid-wait and return a distinct cancellation outcome promptly, or remove the "cancel with Stop" guidance and state that an in-flight load runs to its timeout.

### Medium — `--config` parses any readable file; its content can surface in errors `[Security]`

- **Where:** `internal/launcher/cli.go:26` + `internal/launcher/config.go:311`
- **What:** `--config` accepts any path the user can read (`~/.ssh/id_rsa`, `/etc/passwd`), which `LoadConfig` then `yaml.Unmarshal`s in full; yaml.v3 type errors embed a slice of the offending value, and `validate`'s quoted entry names echo file content.
- **Why it matters:** a confused user or a script running `llama-launcher --config <anyfile>` prints fragments of the file (an `id_rsa` or `/etc/passwd` line that happens to parse as YAML still surfaces its content) into the session log.
- **Fix:** refuse paths that do not look like a launcher config (known keys/header) before parsing, or never echo parsed values whose source might be an arbitrary file.

### Medium — Unbounded server-reported strings reach the terminal and the MCP surface `[Security]`

- **Where:** `internal/launcher/server.go:733,740` (model name) and server.go:764-767,871-874 (`formatFloatPtr`)
- **What:** `models[0].Name` is sanitized for control characters but never length-bounded, and live-params floats format with `strconv.FormatFloat(*p, 'g', -1, 64)` — a hostile server answering `temperature: 1e300` (a legal JSON float) produces ~304 digits.
- **Why it matters:** a hostile server squatting a configured port turns one small loopback response into unbounded memory and output amplification: a multi-megabyte model id is held in `RunningInstance.ActiveModel`, re-allocated on every menu repaint, and shipped to the MCP agent's tool result; the float inflates the drift notice on every activation.
- **Fix:** cap the accepted model-id length after sanitization (a few KB, truncate the rest); reject non-finite or excessively-large exponents when parsing live params, or format with bounded precision (`'g', -1, 8`).

### Medium — `Unload(backend, addr)` acts on whatever the address actually hosts `[Correctness]`

- **Where:** `launcher/launcher.go:130-137` with `internal/launcher/server.go:1154-1168`
- **What:** the named backend picks managed-vs-external routing only; both arms resolve the real occupant through `identifyBackend` and never verify the occupant matches the name given.
- **Why it matters:** `launcher.Unload("llamacpp", "127.0.0.1:11434")` against an Ollama serving there takes the managed branch and SIGTERM-kills the entire Ollama daemon (`ServerStopped: true`); the mirror case `Unload("ollama", …)` against a llama.cpp reports "model unloaded, server still running" while the model was never touched (llamacpp's `UnloadModel` is a no-op).
- **Fix:** after resolving the occupant, verify `occupant.Backend == backend` and return a clear "no <backend> at addr" error instead of acting on a foreign server.

## Recommended Action Order

1. **Close the shell-execution seams first** (`api_key_cmd` `sh -c`, keystore `security -i`): config-file code execution, both independently verified; both collapse once a no-shell argv resolver exists — the first is the prerequisite for the second.
2. **Restore the adapter's JSON contract** (stderr fuse, in-flight subprocess cap): the two quick fixes with the most deployment impact, on the only network-reachable surface.
3. **Reinstate unconditional stop** (auth-refusing servers, process-table quoting): both block the ADR-0001/ADR-0015 promise; the process-table fix needs its table test alongside.
4. **Data-loss quick wins**: `--days 0`, same-second log truncation (uniquifier or O_APPEND), and the explicit legacy-name list for the config-dir cleanup.
5. **Attack-surface hardening needing design input**: the PID-identity check (platform-specific helpers) and the per-platform "Edit config" action (editor or sentinel — a product decision).
6. **Facade contract corrections**: `ErrStartupTimeout` wrapping, cancel-via-Stop, `Unload` occupant check, and the no-stderr carve-out; the facade envelope change needs design discussion first.
7. **Concurrency hardening**: Ollama last-started fields under the mutex, sysmem subprocess timeouts — each verifiable under `go test -race`.
8. **Hostile-server bounds**: model-name/float caps and the `--config` refusal.
9. **Architecture candidates for /improve-codebase-architecture**: the diverged config validators (`validateAll` deriving from `validate`) and the facade `Config` alias (wrap instead of alias).

## What Looked Good

The adapter's security surface is genuinely strong — fail-closed allowlist, strict charset validation, no shell in its own spawning, capped streams — and it is adversarially tested across 28 tests. The keystore is careful: write-read-back verification, redaction on log surfaces, 97% package coverage. The API-key data race was found and fixed properly (RWMutex, CHANGELOG 1.4.6), and health checks discriminate by response content and headers, not status alone, so a squatting LM Studio-style responder is not misidentified. The ADR-0009 `activationOps` seam keeps orchestration logic testable with fakes instead of real servers, and the alias-over-internal facade compiles across all three GOOS targets. These are the parts to leave alone while fixing the findings above.
