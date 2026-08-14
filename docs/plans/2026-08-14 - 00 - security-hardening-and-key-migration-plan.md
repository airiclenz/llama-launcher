# Security hardening and key migration plan

**Goal:** Fix security-audit findings #2 (MCP cross-origin protection), #3 (outbound redirect hardening) and #4 (ps-visible api_key), and adopt apogee's plaintext-key auto-migration (secret-store offer + `api_key_cmd` config source) for llama-launcher.

**Date:** 2026-08-14 · **Status:** TODO · **sized for:** ~200k-context host

**Authoritative sources**

- Audit report: `docs/skill-runs/security-audit/2026-08-13/report.md` (findings #2, #3, #4). Finding #1 was triaged as by-design and is out of scope.
- MCP go-sdk v1.6.1 `mcp/streamable.go`: `StreamableHTTPOptions.CrossOriginProtection *http.CrossOriginProtection` (line 186); when non-nil, `Check(req)` is applied to every request (lines 267–269).
- llama.cpp `common/arg.cpp`: `--api-key` maps to env var `LLAMA_API_KEY`; a command-line flag always beats the env var, so an `extra_args` `--api-key` override keeps winning.
- Apogee (reference implementation, pinned at commit `1c0037b`, checkout at `../apogee`): `cmd/apogee/keymigrate.go` (migration policy, ADR 0047), `internal/keystore/` (store probe/write/read-cmd), `internal/config/configwrite_keysource.go` (surgical entry rewrite contract). Apogee code is REFERENCE material: where apogee's naming/spelling disagrees with an item's What, the item text wins (llama-launcher uses `api_key`/`api_key_cmd` underscore keys, not apogee's `api-key`/`api-key-cmd`).
- llama-launcher pinned at commit `3cb87ff`; project details in `llama-launcher.TDD.md`.

**Ratified design calls** (user, 2026-08-14, via AskUserQuestion during plan authoring)

1. Item 4 scope: **full adoption** of apogee's key migration — argv→env fix PLUS vendored keystore, `api_key_cmd` config source, and the startup offer/notice with write→read-back→rewrite ordering.
2. Offer UX: **menu prompt + notices** — the interactive `llml` menu raises the yes/no offer at startup; non-interactive commands print a one-line stderr notice instead.
3. MCP cross-origin protection: **always on**, no disable flag.

**Author-ratified calls** (plan author, 2026-08-14 — mechanical/internal)

- `api_key_cmd` resolves **once at config load** (config loads once per process, `cli.go:49`); a failing command is a load-time error naming the entry.
- The keystore is **copied** into `internal/keystore/` (Go `internal/` packages cannot be imported across modules); service name becomes `llama-launcher`.
- Key-command execution uses `sh -c` on unix and `cmd /C` on Windows; the vendored live test drops apogee's `github.com/google/shlex` dependency in favour of that same exec path.

**Standing requirements**

- skills: coding-standards
- Any authorized deviation from item text must land as a dated NOTES line under the item.
- Per-item Acceptance is targeted; the repo's full gate (`make check`) runs once at closeout.

**Out of scope**

- Audit finding #1 (setsid orphaning) — by design; an ADR-0001 amendment documenting it is a separate, user-owned edit.
- Extracting the keystore into a shared module for apogee and llama-launcher.
- Folding migration notices into `llml config validate`'s problem list.
- Any version bump (see closing note).

---

## 1. Enable cross-origin protection on the MCP HTTP handler — ✅ DONE (2026-08-14)

NOTES (2026-08-14): Deviation — the protection is installed as `http.NewCrossOriginProtection().Handler(...)` middleware (helper `crossOriginHandler`) rather than via `mcp.StreamableHTTPOptions{CrossOriginProtection: ...}` as the item's code block specifies. In the pinned go-sdk v1.6.1 that option field is marked `Deprecated:` (mcp/streamable.go:180-186), will be removed in SDK v1.8.0, silently ignores any deny handler, and its own doc comment names this middleware as the replacement. Behaviour is identical (403 on a cross-origin non-safe request) and the middleware form additionally satisfies the item's stated test criterion literally: the request never reaches the MCP handler at all, instead of being rejected inside it. Ratified call 3 (always on, no flag) is unchanged.

NOTES (2026-08-14): The package doc comment in `cmd/llama-launcher-mcp/main.go` was updated alongside TDD §15.3 — it enumerates the adapter's access gates and would otherwise have been left stating the IP allowlist and narrow bind as the whole story.

**What:** In `cmd/llama-launcher-mcp/main.go` (handler construction at lines 51–53), pass options as the second argument to `mcp.NewStreamableHTTPHandler`:

```go
mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
    return server
}, &mcp.StreamableHTTPOptions{CrossOriginProtection: http.NewCrossOriginProtection()})
```

Always on, no flag (ratified call 3). Non-browser MCP clients send no cross-origin `Origin`/`Sec-Fetch-Site` headers and are unaffected. Update the test helper `startAdapter` (`cmd/llama-launcher-mcp/integration_test.go:31`) to construct the handler with the same options so every existing session test exercises production wiring. Add tests following the inline-handler pattern of `TestEndToEndAllowlistBlocksConnect` (integration_test.go:130): a POST to `/mcp` carrying `Origin: https://attacker.example` (host mismatch) is rejected with a 4xx and never reaches the MCP layer; a request with no `Origin` header still works; a same-origin request still works. Update `llama-launcher.TDD.md`'s MCP adapter section (security posture paragraph) to state that cross-origin browser requests are refused by `http.CrossOriginProtection` in addition to the IP allowlist.

**Files:** `cmd/llama-launcher-mcp/main.go`, `cmd/llama-launcher-mcp/integration_test.go`, `llama-launcher.TDD.md`

**Tests:** New cross-origin rejection + no-Origin pass-through tests as above; existing adapter tests keep passing through the updated `startAdapter`.

**Acceptance:** `go build ./cmd/llama-launcher-mcp/ && go test ./cmd/llama-launcher-mcp/`

**Commit:** `fix(mcp): enable cross-origin protection on the streamable HTTP handler`

## 2. Stop following redirects from configured LLM servers — ✅ DONE (2026-08-14)

**What:** In `internal/launcher/backend_http.go`, both `authedGet` (line 67) and `authedPostJSON` (line 80) build `&http.Client{Timeout: timeout}` with the default redirect policy (follows up to 10 hops). Give both a shared client constructor whose `CheckRedirect` returns `http.ErrUseLastResponse`, so the first 3xx response is returned as-is and no second request is issued. Callers already treat any non-200 as an error, so a squatter's 3xx surfaces as an unexpected-status error instead of steering an outbound GET (audit finding #3). Add a short comment stating the constraint: whatever answers on a configured port is untrusted, so the client must never follow its redirects.

**Files:** `internal/launcher/backend_http.go`, `internal/launcher/backend_http_test.go`

**Tests:** An `httptest.Server` that answers 302 with a `Location` pointing at a second `httptest.Server`; assert the helper returns the 302 response itself and the second server records zero requests — for both `authedGet` and `authedPostJSON`.

**Acceptance:** `go test ./internal/launcher/`

**Commit:** `fix(launcher): stop following redirects from configured LLM servers`

## 3. Deliver the llamacpp api_key via environment, not argv — ✅ DONE (2026-08-14)

NOTES (2026-08-14): Deviation — two files beyond the item's Files list were corrected because the change made their user-facing claims false: `README.md` (the config-sample comment block, the per-backend `api_key` table row, and the "visible in ps" caveat) and `internal/launcher/defaults/config.yaml` (the same comment block, which is the template written into a fresh user config). Item 5 also edits `defaults/config.yaml`, but for a different reason (documenting `api_key_cmd`); this edit only rewrites the existing llamacpp `api_key` comment.

NOTES (2026-08-14): Deviation — `BuildServerArgs`'s first parameter became unused once the `--api-key` append was removed, so it was renamed `cfg` → `_` to match the file's own idiom (`ServerBinary(_ *Config)`). Signature and interface conformance are unchanged.

NOTES (2026-08-14): The TDD §4.2 llamacpp bullet also corrected the env-var name it named as a "possible future alternative": it said `LLAMA_ARG_API_KEY`, but llama.cpp `common/arg.cpp` binds `--api-key` to `LLAMA_API_KEY`, which is what the code now sets.

NOTES (2026-08-14): Pre-existing failure in this sandbox, unrelated to the item: `TestStopServerAt_StartingOccupant` fails on a clean tree too (verified via `git stash`) — the container's `nc` never binds the test port.

**What:** Close audit finding #4 (ps-visible credential). In `internal/launcher/backend_llamacpp.go`:

- Remove the `--api-key` append from `BuildServerArgs` (lines 256–260).
- Implement `BuildServerEnv` (currently `return nil`, line 151) to return `[]string{"LLAMA_API_KEY=" + key}` when `cfg.APIKeyFor(b.Name())` is non-empty, else nil. `startManagedServer` already consumes it (`internal/launcher/server.go:99`).
- Carry the override comment forward: llama-server reads `LLAMA_API_KEY` only when no `--api-key` flag is present, so a user's `extra_args` `--api-key` override still wins (llama.cpp `common/arg.cpp` is the authority).

`redactAPIKeyArgs` in `backend_http.go` stays — `extra_args` may still carry a literal `--api-key`. The HTTP-probe path (`applyAPIKeys`, `backend.go:170`) is untouched. Update `llama-launcher.TDD.md`: §4.2's `api_key` per-backend meaning and the flag-assembly paragraph (~line 749) that currently says `--api-key` is emitted on argv.

**Files:** `internal/launcher/backend_llamacpp.go`, `internal/launcher/backend_llamacpp_test.go`, `llama-launcher.TDD.md`

**Tests:** `BuildServerArgs` output contains no `--api-key` even with a configured key; `BuildServerEnv` returns exactly `LLAMA_API_KEY=<key>` when configured and nil when not; existing arg-assembly tests updated.

**Acceptance:** `go test ./internal/launcher/`

**Commit:** `fix(llamacpp): deliver api_key to llama-server via environment, not argv`

## 4. Vendor apogee's keystore package — ✅ DONE (2026-08-14)

NOTES (2026-08-14): Deviation — `github.com/google/shlex` was imported by `keystore_test.go` too, not only by `live_test.go` as the item's text assumes: `TestReadCmdReadsBackWhatWriteStored` split `ReadCmd`'s line with it. Both round trips now go through one shared helper, `readKeyThroughShell`, which hands the line whole to a shell exactly as item 5's resolver will, so the item's "No new module dependency" holds (`go.mod`/`go.sum` are untouched).

NOTES (2026-08-14): The read-back helper names the shell by absolute path (`/bin/sh`) rather than by PATH lookup, because the test fixture REPLACES PATH with the fake-tool directory — the store tool named INSIDE the line is still resolved through that replaced PATH, which is the lookup the round trip exists to exercise. It skips on windows, matching the item's rule for the live test.

NOTES (2026-08-14): Comment prose that cited apogee's own decisions and packages (ADR 0042, `internal/config/keyresolve.go`, `internal/tools`, the `internal/present` opener idiom, "the fixture idiom internal/config and internal/mcp already use") was rewritten rather than copied — those references name nothing in this repository. Every behaviour-bearing line is unchanged; `api-key:`/`api-key-cmd:` became `api_key:`/`api_key_cmd:` per the plan's precedence rule.

NOTES (2026-08-14): The fake-tool steering env vars were renamed with the live gate (`APOGEE_KEYSTORE_FAKE_*` → `LLAMA_LAUNCHER_KEYSTORE_FAKE_*`), as was the fixture's temp-dir prefix (`llama-launcher-keystore-tools`); the item names only the live-test gate, but the same rename rule applies.

NOTES (2026-08-14): No CHANGELOG entry — this item adds an internal package with no caller and no user-visible behaviour; the migration story belongs to items 7 and 8, where it reaches the user.

**What:** Copy `../apogee/internal/keystore/` (at commit `1c0037b`: `keystore.go`, `run.go`, `keystore_test.go`, `live_test.go`) into `internal/keystore/`, adapted:

- `const service = "llama-launcher"` (was `"apogee"`); probe account `"__llama_launcher_probe__"`; every user-facing error prefix `apogee:` → `llama-launcher:`.
- Live test gate env `APOGEE_LIVE_KEYSTORE` → `LLAMA_LAUNCHER_LIVE_KEYSTORE`; its entry name → `llama-launcher-live-test`; keep the skip-by-default guard so `make check` never touches a real keychain.
- Drop the `github.com/google/shlex` import in `live_test.go`: run the read-back command through `sh -c <ReadCmd>` (skip the live test on Windows), matching how item 5's resolver executes key commands. No new module dependency.

Public surface stays apogee's: `Probe() (Store, bool)`, `Store.Name()`, `Store.Write(entry, key string) error`, `Store.ReadCmd(entry string) string`. Package doc comment updated to name this repo and cite apogee `1c0037b` as origin.

**Files:** `internal/keystore/keystore.go`, `internal/keystore/run.go`, `internal/keystore/keystore_test.go`, `internal/keystore/live_test.go`

**Tests:** The copied fake-tool unit tests pass unmodified apart from renames; live test skips without the gate env.

**Acceptance:** `go build ./... && go test ./internal/keystore/`

**Commit:** `feat(keystore): vendor apogee's secret-store package`

## 5. Add api_key_cmd and plaintext_key_ok config sources — ✅ DONE (2026-08-14)

NOTES (2026-08-14): Deviation — the two refusals also feed `validateAll`, not only `validate()` as the item's text names. `validateAll` is what `llama-launcher config validate` prints, and it duplicates every other load-fatal check in the file; leaving these two out would have made `config validate` report a clean bill on a config that `LoadConfig` refuses.

NOTES (2026-08-14): Deviation — `runKeyCommand` also refuses a command that times out (60s) or prints more than 64 KiB, beyond the item's "non-zero exit or empty output". This runs on the startup path before the launcher has drawn anything, so an unbounded wait is indistinguishable from a hung binary, and an uncapped read turns a typo in a config file (`cat /dev/urandom`, the wrong program on PATH) into an out-of-memory kill. Both are apogee's own bounds on the same command, carried over with its `cappedWriter` sink.

NOTES (2026-08-14): Deviation — `README.md` was edited beyond the item's Files list, for the same reason item 3 edited it: the README reproduces `internal/launcher/defaults/config.yaml` and states the file is written *verbatim* from it, so documenting the new keys only in the template would have made that claim false. The two blocks are byte-identical again (verified by diff).

NOTES (2026-08-14): The TDD file-map rows for `config.go` and `config_test.go` were updated alongside §4.2; the item names §4.2 only, but the map is the repo's per-file responsibility index and both files gained responsibilities here.

NOTES (2026-08-14): Pre-existing failure in this sandbox, unrelated to the item and already recorded under item 3: `TestStopServerAt_StartingOccupant` fails because the container's `nc` never binds the test port.

**What:** In `internal/launcher/config.go`:

- `ServerConfig` (line 55) gains `APIKeyCmd string` and `PlaintextKeyOK bool`; the custom `UnmarshalYAML` inline struct (lines 63–75) gains `yaml:"api_key_cmd"` and `yaml:"plaintext_key_ok"`.
- Validation (with `validate()`'s existing error/warning machinery): an entry setting both `api_key` and `api_key_cmd` is refused naming the entry; a whitespace-only `api_key_cmd` is refused.
- Resolution at load (author-ratified): after validation, for each **enabled** server with `APIKeyCmd`, run the command via `sh -c` (unix) / `cmd /C` (Windows), capture stdout, trim whitespace. Non-zero exit or empty output → a load error naming the entry and a capped stderr excerpt. Store the result in an unexported `resolvedKey` field; `APIKeyFor` (line 214) returns the trimmed literal `APIKey` when set, else `resolvedKey`. Config loads once per process (`cli.go:49`), so this is one subprocess per configured entry per run; the menu's `Reload()` re-resolves, which is correct (the store may have changed).
- Document both keys in the example config template `internal/launcher/defaults/config.yaml` (comments beside `api_key`) and in `llama-launcher.TDD.md` §4.2 (the `servers` map paragraph at ~line 342 and the plaintext note at ~line 344).

**Files:** `internal/launcher/config.go`, `internal/launcher/config_test.go`, `internal/launcher/defaults/config.yaml`, `llama-launcher.TDD.md`

**Tests:** Parse both scalar and mapping forms with the new keys; mutual-exclusion and whitespace-only refusals; resolver success (`api_key_cmd: "echo  sk-test "` → `APIKeyFor` = `sk-test`), failing command → load error naming the entry; `plaintext_key_ok` round-trips.

**Acceptance:** `go test ./internal/launcher/`

**Commit:** `feat(config): add api_key_cmd and plaintext_key_ok key sources`

## 6. Surgical config writer for a server entry's key source — ✅ DONE (2026-08-14)

NOTES (2026-08-14): No CHANGELOG entry — this item adds an internal writer with no caller and no user-visible behaviour; the migration reaches the user in items 7 and 8, where it is announced.

NOTES (2026-08-14): The refusal for "an `api_key` value spanning more than its own line" is enforced by re-reading the key's own line as a document of its own and comparing the scalar it yields (value and style) with the one the whole file yields. yaml.v3 reports where a scalar STARTS but not where it ends, so apogee's `ValueNode.Line == keyLine` test alone passes a folded plain scalar (`api_key: sk-a` continued on the next line) whose tail the rewrite would leave behind; the block-scalar and quoted-value-closing-further-down shapes fall out of the same check.

NOTES (2026-08-14): The item's "mode 0600" is written as a fixed mode rather than apogee's carry-the-existing-mode-forward, so a config that was 0644 comes back 0600. The file names a key source and may still hold a literal beside it, and the rewrite is the moment the launcher touches it — narrowing never widens, and 0600 is the mode `GenerateExampleConfig` creates the file with.

NOTES (2026-08-14): `llama-launcher.TDD.md` was deliberately not touched: the item names only the two Go files, its file map is not an exhaustive index (it already omits `cli.go` and `frame.go`), and item 8 owns the TDD updates for the migration feature this writer serves.

NOTES (2026-08-14): Pre-existing failure in this sandbox, unrelated to the item and already recorded under items 3 and 5: `TestStopServerAt_StartingOccupant` fails because the container's `nc` never binds the test port. Every other test in `./internal/launcher/` passes, the 13 new ones included.

**What:** New file `internal/launcher/configwrite.go` with two functions mirroring the contract of apogee's `internal/config/configwrite_keysource.go` (reference at `1c0037b`), addressed to llama-launcher's config shape — `servers:` is a **map** (`servers.<name>` mapping value), not apogee's named list:

- `SaveServerKeyCommand(path, name, command string) error` — replace the entry's `api_key: …` line, in place, with an `api_key_cmd: <command>` line (YAML-marshalled scalar so quoting is owned by the marshaller).
- `SaveServerPlaintextKeyOK(path, name string) error` — append `plaintext_key_ok: true` to the entry's mapping (or rewrite an existing `false` line).

Shared contract: read the file; splice text so every comment, key order and other entry comes back byte-identical; refuse anything non-surgical (entry absent, scalar-form entry `llamacpp: true`, flow-style mapping, an `api_key` value spanning more than its own line) with an "edit the file by hand" error that names the path and leaves the file untouched; re-parse the spliced bytes with the package's own config parser and verify the entry now declares the intended source before writing; write atomically (temp file + rename in the same directory, mode 0600). An entry already in the target state is a no-op, so a re-offer cannot churn the file.

**Files:** `internal/launcher/configwrite.go`, `internal/launcher/configwrite_test.go`

**Tests:** Golden before/after fixtures with comments and multiple entries proving byte-identical preservation outside the edited line; each refusal case; idempotent no-op; atomic-write mode check.

**Acceptance:** `go test ./internal/launcher/`

**Commit:** `feat(config): surgical writer for a server entry's key source`

**Depends on item 5** (the parser must know the new keys for the re-parse verification).

## 7. Key-migration engine and plaintext-key notice — ✅ DONE (2026-08-14)

NOTES (2026-08-14): Deviation — `plaintextKeyServers` tests `strings.TrimSpace(sc.APIKey) == ""` rather than the item's literal `APIKey != ""`. An entry whose literal is only whitespace holds no key at all — `APIKeyFor` trims it to "" and the launcher sends nothing — so counting it would name it in a notice about a secret that is not there, and taking the offer would ask `keystore.Write` to store a key it refuses as empty.

NOTES (2026-08-14): Deviation — `migrateKey` trims the key once, up front, and it is the trimmed form that is both written to the store and compared against the read-back. The item says "compare to what went in"; trimming makes what went in the one form the round trip could ever hand back, since `runKeyCommand` trims what a command prints. Without it a literal the file surrounded with whitespace would fail its own read-back and be reported as a store mismatch.

NOTES (2026-08-14): `plaintextKeyNotice` returns text with no `llama-launcher:` or `warning:` prefix of its own, where apogee's carries one: item 8 prints it on both paths through the existing `warning:` sink, which supplies the prefix.

NOTES (2026-08-14): `llama-launcher.TDD.md` was deliberately not touched — the item names only the two Go files, and item 8 explicitly owns the TDD updates for the migration feature this engine serves (same call as item 6).

NOTES (2026-08-14): No CHANGELOG entry — this item adds an internal engine with no caller and no user-visible behaviour, matching the call made for items 4 and 6; the migration reaches the user in item 8, where it is announced.

NOTES (2026-08-14): Pre-existing failure in this sandbox, unrelated to the item and already recorded under items 3, 5 and 6: `TestStopServerAt_StartingOccupant` fails because the container's `nc` never binds the test port. Every other test in `./internal/launcher/` passes, the 11 new ones included.

**What:** New file `internal/launcher/keymigrate.go`, the policy layer adapted from apogee's `cmd/apogee/keymigrate.go` (reference at `1c0037b`), UI-free:

- `type secretStore interface { Name() string; Write(entry, key string) error; ReadCmd(entry string) string }` plus a production probe adapter over `internal/keystore`'s `Probe()`.
- `plaintextKeyServers(cfg *Config) []string` — names of **enabled** servers whose key literal sits in the file (`APIKey != ""`) without `PlaintextKeyOK`, sorted by name (the config is a map; sorted order is the stable one).
- `migrateKey(store secretStore, path, name, key string) error` — apogee's three-step order, verbatim in spirit: (1) write the secret into the store; (2) read it back by executing the exact `store.ReadCmd(name)` line about to be persisted, through the same exec path as item 5's resolver, and compare to what went in; (3) only then `SaveServerKeyCommand`. A failed read-back or mismatch leaves the config untouched and says so in the error.
- `plaintextKeyNotice(path string, reason string, names []string) string` — adapted wording: names the entries and the config path actually read (`--config`/`LLAMA_LAUNCHER_CONFIG` both move it), the reason no offer is coming, and the manual alternatives: `api_key_cmd: <command that prints the key>`, `chmod 600 <path>`, and `plaintext_key_ok: true` to answer for good. Two reason constants: no store on this machine; non-interactive commands never prompt (point at running `llama-launcher` with no arguments for the offer).

**Files:** `internal/launcher/keymigrate.go`, `internal/launcher/keymigrate_test.go`

**Tests:** Against a fake `secretStore`: happy path rewrites the config (via a temp config file); read-back mismatch and read-back failure leave the file byte-identical and return the telling error; `plaintextKeyServers` skips disabled entries, `plaintext_key_ok` entries, and `api_key_cmd` entries; notice text names entries, path and reason.

**Acceptance:** `go test ./internal/launcher/`

**Commit:** `feat(launcher): key migration engine and plaintext-key notice`

**Depends on items 4, 5, 6.**

## 8. Raise the offer in the interactive menu; notice everywhere else — ✅ DONE (2026-08-14)

NOTES (2026-08-14): Deviation — `README.md` was edited beyond the item's Files list, as in items 3 and 5. Item 5's README text already forward-referenced this feature ("set `plaintext_key_ok: true` … so nothing offers to move it into a secret store"), so shipping the offer without describing it would have left the README promising a behaviour it never explained. Per the user's decision on the retry, the edit is exactly two sentences appended to the existing "### API keys" section — the offer's three answers, and the subcommand notice — and the rest of the file is untouched at HEAD.

NOTES (2026-08-14): Deviation — the TDD file map (§5.2) gained rows for `configwrite.go`, `keymigrate.go`, `configwrite_test.go` and `keymigrate_test.go`, plus a clause each on the `main.go` and `menu.go` rows. The item names §3.1 and §4.2 only, but items 6 and 7 each recorded that item 8 owns the TDD updates for the feature they serve, and the map is the repo's per-file responsibility index (item 5's precedent) — after this item four files carrying the feature would otherwise appear nowhere in it.

NOTES (2026-08-14): The no-store notice on the interactive path goes to stderr through the same `warning:` printer the config warnings use moments earlier, per the item's "print the notice … and continue into the menu". On a TTY the menu's first repaint clears the screen, so it shares those warnings' fate; that sink is the only one that exists before the menu takes the terminal, and the alternative — a popup demanding a keypress — would make a machine that simply has no store harder to launch than one that does.

NOTES (2026-08-14): The result of an answer is shown through `showPopup` and a failure is reported the same way rather than returned from `RunInteractiveMenu`: an error returned there is printed by `Run` as `Error:` and exits 3, so a store that refused a write would take the whole menu down over a key the launcher can still read from the file. "Stop on the first error with its message" is honoured — the run stops, the message is shown, and the entries that already moved are shown with it.

NOTES (2026-08-14): Pre-existing failure in this sandbox, unrelated to the item and already recorded under items 3, 5, 6 and 7: `TestStopServerAt_StartingOccupant` fails because the container's `nc` never binds the test port. Every other test in `./internal/launcher/` passes, the 7 new ones included.

**What:** Wire item 7 per ratified call 2:

- **Non-interactive commands:** in `internal/launcher/cli.go` after `LoadConfig` succeeds (~line 49, before the subcommand switch at line 74): when `plaintextKeyServers` is non-empty, print `plaintextKeyNotice` (non-interactive reason) to stderr in the existing `warning:`-sink style. No store probe on this path — a probe is a subprocess, and a command that will never prompt has nothing to gain from the answer (apogee's headless rule).
- **Interactive menu:** in the no-args branch (`cli.go:66-72`) or at the top of `RunInteractiveMenu` (`internal/launcher/menu.go:33-36`, after the platform gate, BEFORE the loop so it fires once, not per repaint): when candidates exist, probe the keystore. No store → print the notice (no-store reason) and continue into the menu. Store found → raise one offer listing the candidate entries with three choices, built on the existing primitives (`selectMenu`, `ui.go:105`, on a TTY; `readLine`/`parseChoice`, `menu.go:973/981`, on the Simple path): **(a)** move the key(s) into `<store name>` — run `migrateKey` per entry, report each rewritten file, and stop on the first error with its message; **(b)** not now — continue, offer returns next launch; **(c)** never for these entries — `SaveServerPlaintextKeyOK` per entry. The menu loop's existing `cfg.Reload()` (menu.go:37) picks up the rewritten config; no special reload handling.
- Update `llama-launcher.TDD.md`: the interactive-menu section gains the startup offer; the configuration section's plaintext note (touched by item 5) gains the migration/notice behaviour.

**Files:** `internal/launcher/cli.go`, `internal/launcher/menu.go`, `internal/launcher/keymigrate.go`, `internal/launcher/keymigrate_test.go`, `llama-launcher.TDD.md`

**Tests:** The offer-decision function (extracted so it is testable without a TTY): candidates + no store → notice text; candidates + store → offer raised with the entries; no candidates → silence and no probe call (assert via a counting fake probe). CLI notice path covered by a test driving `Run` with a plaintext-key temp config and asserting the stderr notice.

**Acceptance:** `go test ./internal/launcher/`

**Commit:** `feat(cli): raise the key-migration offer in the menu and notices elsewhere`

**Depends on item 7.**

---

**Suggested version bump:** minor (1.6.3 → 1.7.0) — `api_key_cmd` and `plaintext_key_ok` are new public config surface and the migration offer is new user-facing behaviour; the public contract moves, so this steps off the patch line. The user decides; no item changes VERSION.
