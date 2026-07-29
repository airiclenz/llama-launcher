# Plan: Public library facade (ADR-0011) — make llama-launcher importable

**Date:** 2026-07-29 · **Source:** grill session with the owner (all design branches resolved; no `needs-design-call` items remain).
**Design authority:** [ADR-0011](../adr/0011-public-library-facade.md) (written in the design session that produced this plan). The first client is apogee (`/workspace/repos/apogee`, its `TODO.md` "Local server start/stop" entry, decided 2026-07-28); the apogee-side integration plan is **separate and later, in that repo** — this plan is the launcher-side export only.

**Conventions for every code item:** follow the `coding-standards` skill; no AI attribution anywhere; CHANGELOG + TDD + README updates are collected in Item 6 (individual items note what they owe). Layer-1 tests only (`make test`); the integration layer (`-tags=integration`) is untouched by this plan. The core rule of the whole plan: **`internal/launcher/` behaviour must not change** — the CLI's output stays byte-identical; the only internal edits are the two notice seams (Items 1–2).

**Resolved decisions (context for implementers):**

1. **Shape:** new public package `launcher/` at the repo root (import path `github.com/airiclenz/llama-launcher/launcher`) — type aliases + thin wrappers over `internal/launcher`. No wholesale move, no root restructure; `main.go` and `cmd/llama-launcher-mcp/` untouched.
2. **Surface (minimal verbs, frozen from v1.6.0):** `LoadConfig(path, notice)`, `DefaultConfigDir()`, `DefaultConfigPath()`, `Config`/`Profile`/`ProfileParams`/`ResolvedProfile` aliases, `ErrConfigNotFound`; `DiscoverRunningInstances(cfg)`, `RunningInstance`; `LoadProfile(cfg, profile, restart, progress, notice)`, `Stop(addr)`, `Unload(backend, addr)`, `StopResult`, `ProgressFunc`, `NoticeFunc`, `ErrNotRunning`. Nothing else — no `LLMServer`/registry, no `TailLog`/log cleanup, no memstats, no `GenerateExampleConfig`, no `QueryLiveParams` (apogee's heartbeat owns the observe half).
3. **Notices:** callbacks, never stream writes. Internal print sites become a threaded `NoticeFunc` sink; the CLI entry points bind stderr printers with **byte-identical** output. The drift notice is delivered to the sink as ONE call carrying the full formatted text exactly as printed today (header line, indented field lines, the `--restart` guidance line); config warnings are delivered one call per warning with the RAW warning text (the `"warning: "` prefix belongs to the CLI printer, not the sink).
4. **No `context.Context`:** verbs block as today; cancellation of an in-flight load is `Stop(addr)` on the Starting instance (ADR-0010). Do not thread ctx anywhere.
5. **Global state stated honestly:** one Config per process (API keys land on the process-global backend registry; last `LoadConfig` wins). Read verbs concurrency-safe; lifecycle verbs serialized per address by the caller. Documented, not restructured.
6. **Release:** full normal release — VERSION → 1.6.0, CHANGELOG, tag `v1.6.0` pushed, Homebrew formula bump. Tag/push/brew are **owner-run** (Item 8), same convention as the integration-test layer.

---

## Part A — Internal notice seams (behaviour-preserving; do first)

- [x] **1. Config warnings gain a notice sink** — `internal/launcher/progress.go`, `internal/launcher/config.go` — ✅ DONE (2026-07-29)
  - In `progress.go`, next to `ProgressFunc`: add `type NoticeFunc func(notice string)` with a doc comment ("delivers user-facing notices — config warnings, the ADR-0007 drift notice — to the UI layer; nil discards") and a `reportNotice(fn NoticeFunc, notice string)` helper mirroring `reportStep`.
  - In `config.go`: add `LoadConfigNotify(path string, notify NoticeFunc) (*Config, error)` containing today's `LoadConfig` body (config.go:324–337) with the warning loop replaced by `reportNotice(notify, w)` per warning — raw warning text, no prefix. `LoadConfig(path)` becomes a one-line delegation binding the stderr printer: `func(w string) { fmt.Fprintf(os.Stderr, "warning: %s\n", w) }` — byte-identical output. `Config.Reload` (config.go:341) stays exactly as it is (it delegates to `LoadConfig`; the facade documents that library clients call `LoadConfig` again instead).
  - Tests (`config_test.go`): `LoadConfigNotify` with a config that triggers the `defaults.server` deprecation warning (fixture pattern already exists in the validate tests) collects the raw warning through the sink; a nil sink is safe; `LoadConfig`'s stderr behaviour is already pinned by existing tests — verify they still pass unchanged, don't rewrite them.

- [x] **2. The drift notice goes through the sink** — `internal/launcher/server.go` — ✅ DONE (2026-07-29)
  - `printDriftNotice` (server.go:689–695) splits into a pure text builder `driftNotice(profileName, addr string, drifts []string) string` returning the full three-part text exactly as printed today (same lines, same trailing newline), and delivery via the sink.
  - `loadProfile` (server.go:449) gains a trailing `notify NoticeFunc` parameter; the `printDriftNotice(...)` call site becomes `reportNotice(notify, driftNotice(...))` — one call, full text (resolved decision 3).
  - Exported entry points: `LoadProfile` keeps its exact current signature (server.go:441) and delegates binding the stderr printer `func(s string) { fmt.Fprint(os.Stderr, s) }` — byte-identical. Add `LoadProfileNotify(cfg *Config, profile *ResolvedProfile, restart bool, progress ProgressFunc, notify NoticeFunc) (*RunningInstance, bool, error)` delegating to `loadProfile` with the caller's sink. CLI (`cli.go:130`) and menu call sites stay on `LoadProfile`, untouched.
  - NOTES (2026-07-29): `LoadProfile` binds the stderr printer and delegates through the new `LoadProfileNotify` (which then calls `loadProfile(realOps{}, …)`), mirroring Item 1's `LoadConfig` → `LoadConfigNotify` shape, rather than calling `loadProfile` directly — same behaviour, one delegation chain instead of two copies of `realOps{}`.
  - Tests (`server_test.go`): the ADR-0009 fake-ops drift test gains a sibling driving `loadProfile` with a recording sink — asserts exactly one notice containing the drifted field name and the `--restart` guidance; the existing drift tests keep passing unchanged. The returned `(inst, started, err)` triple is unaffected by the new parameter — no assertion changes elsewhere.
  - NOTES (2026-07-29): `TestLoadProfile_DriftNoticeContent` could not stay unchanged — it asserts on captured stderr, and the internal `loadProfile` no longer prints. Its assertions are unchanged, but it now drives the exported `LoadProfile` (the stderr-binding path, so the CLI behaviour is actually covered) against an httptest llama-server stand-in serving `/health`, `/v1/models` and `/props` — the ADR-0007 idempotent path, forking nothing — instead of the fake-ops seam. Every other `loadProfile` call site only gained the trailing `nil` sink.

## Part B — The facade package

- [ ] **3. Public package `launcher/`** — new files `launcher/doc.go`, `launcher/launcher.go`
  - `launcher/launcher.go`, `package launcher`, importing `core "github.com/airiclenz/llama-launcher/internal/launcher"`:
    - Aliases: `type Config = core.Config`, `Profile`, `ProfileParams`, `ResolvedProfile`, `RunningInstance`, `StopResult`, `ProgressFunc`, `NoticeFunc` (Item 1's type).
    - Errors: `var ErrConfigNotFound = core.ErrConfigNotFound`, `var ErrNotRunning = core.ErrNotRunning` (same values, so `errors.Is` works across the boundary).
    - Functions, each a one-line delegation with its own doc comment (worst-case blocking durations on the lifecycle verbs): `LoadConfig(path string, notice NoticeFunc) (*Config, error)` → `core.LoadConfigNotify`; `DefaultConfigDir() string`; `DefaultConfigPath() string`; `DiscoverRunningInstances(cfg *Config) []*RunningInstance`; `LoadProfile(cfg *Config, profile *ResolvedProfile, restart bool, progress ProgressFunc, notice NoticeFunc) (*RunningInstance, bool, error)` → `core.LoadProfileNotify`; `Stop(addr string) (*StopResult, error)`; `Unload(backend, addr string) (*StopResult, error)`.
  - `launcher/doc.go`: the package doc carries the contract (ADR-0011): supported surface = documented symbols (alias-reachable extras excluded); one Config per process — last `LoadConfig`'s API keys win; verbs block (activation up to ~30 s health wait plus stop escalation — call from a goroutine; cancel an in-flight load with `Stop(addr)` per ADR-0010); read verbs concurrency-safe, lifecycle verbs serialized per address by the caller; re-read config via `LoadConfig`, not `Config.Reload` (which prints to stderr); remote control belongs to the MCP adapter (ADR-0008); the launcher manages servers on the local machine only and is not a router (ADR-0002).
  - Wrappers contain **zero logic** (the ADR-0009 `realOps` discipline, applied to the facade): if a wrapper wants an `if`, the seam is in the wrong place.
  - Owes Item 6: TDD §5.2/§16, README library section, CHANGELOG.

- [ ] **4. Facade tests** — new file `launcher/launcher_test.go`, external test package (`package launcher_test`) so the tests prove the *client's* view compiles and behaves
  - Config + notices: write a temp `config.yaml` (two servers enabled, one profile missing `server:` with `defaults.server` set — the deprecation-warning shape) → `LoadConfig` returns a usable Config, the sink received the warning, and `ResolveProfile` merges params (assert `ContextSize` lands — the "apogee reads model context info from the resolved profile" path). Nil-sink call is safe.
  - Errors: `LoadConfig` on a missing path → `errors.Is(err, launcher.ErrConfigNotFound)`; `Stop` on a loopback address obtained from `net.Listen`-then-`Close` (guaranteed dead) → `errors.Is(err, launcher.ErrNotRunning)`.
  - Discovery: httptest llama-server stand-in (`/health` → `{"status":"ok"}`, `/v1/models` → one id; pattern exists in `discovery_test.go`) with a temp config templated to its host:port → one `RunningInstance` with `ActiveModel` set and `Addr()` correct.
  - Drift-notice threading end-to-end: extend the stand-in with `/props` (per-slot `n_ctx` × `total_slots` differing from the profile's `context_size`; response shape is pinned in `TestLlamaCppQueryLiveParams`) and name the model so the basename matches the profile's — a plain `LoadProfile` then takes the ADR-0007 idempotent path against the real `realOps` probes, forks nothing, and must deliver exactly one drift notice to the sink while nothing reaches the test's stderr.
  - No real processes anywhere: every test is httptest/tempdir-only, `make test`-safe in a container.

- [ ] **5. External-import smoke check** — verification only, **nothing committed**
  - In a scratch directory outside the repo: a throwaway module (`go.mod` requiring `github.com/airiclenz/llama-launcher` with a `replace` to the local checkout) whose `main.go` imports the facade, references each documented symbol (compile coverage of the whole surface: the aliases in var declarations, the funcs called or assigned), and runs `errors.Is(err, launcher.ErrConfigNotFound)` against a missing path. `go build` + `go run` must succeed.
  - This is the one risk the in-repo tests cannot see: the alias-over-internal pattern compiling *from another module*. Record pass/fail in the item note; delete the scratch module afterwards.

## Part C — Docs, version, release

- [ ] **6. Documentation pass** — `llama-launcher.TDD.md`, `README.md`, `CHANGELOG.md`, `TODO.md`
  - TDD: §1 overview gains the library sentence (importable via the `launcher/` facade, ADR-0011); §2 Goals gains the bullet; new **§16 "Public library facade"** documenting the surface table, the four contract decisions (notices/blocking/global-state/documented-surface), the two internal seams (`LoadConfigNotify`, `LoadProfileNotify`), and the MCP composition note (in-process = same machine, ADR-0008 adapter = remote; they compose); §5.2 file table gains rows for `launcher/doc.go` + `launcher/launcher.go` and the changed `progress.go`/`config.go`/`server.go` row texts; §15 gets a one-line cross-reference to §16.
  - README: "Using llama-launcher as a Go library" section — `go get github.com/airiclenz/llama-launcher/launcher@v1.6.0`, a ~15-line example (LoadConfig with a notice func → ResolveProfile → LoadProfile in a goroutine with progress → Stop), and the one-Config-per-process + blocking-verbs caveats in two sentences.
  - CHANGELOG: `## 1.6.0` — public library facade (ADR-0011), the two notice seams (internal, no behaviour change), docs.
  - TODO.md: delete the stale loose-end line "Stop/unload cannot target a still-loading (503) server …" — shipped by ADR-0010 (verify against the CHANGELOG 1.4.x/1.5.0 entries before deleting). Doc-accuracy fix owed by the TDD §14 convention, nothing more.
  - CONTEXT.md: **deliberately untouched** — the facade is implementation, not domain language; no new terms emerged from the grill.

- [ ] **7. Version bump + full verification sweep** — `VERSION`, whole repo
  - `VERSION` → `1.6.0`.
  - Sweep: `make build` (CLI), `make build-mcp` (adapter unaffected but must still build), `make test`, `go vet ./...`, `go vet -tags=integration ./internal/launcher/` (the container-side integration check, per TDD §12), `gofmt -l .` clean.
  - Confirm byte-identical CLI notice output by inspection of the two bindings (Items 1–2) — the stderr format strings must match the pre-plan ones exactly.

- [ ] **8. Release v1.6.0 — OWNER-RUN** — nothing here is executed by an agent
  - Owner: commit review, `git tag v1.6.0`, push with tags, then the `brew-release` skill for the formula bump (source-tarball hash, both binaries as today).
  - After the tag is public: the **apogee-side integration plan** is unblocked — it lives in the apogee repo, needs its own grill (per apogee's TODO: where profiles surface in `/server`/`/model`, marking launcher-backed `servers:` entries, the load-latency UX between `LoadProfile` returning and the first heartbeat), and its `go.mod` requires `github.com/airiclenz/llama-launcher v1.6.0` (never `replace`; local cross-repo dev via untracked `go.work`).
