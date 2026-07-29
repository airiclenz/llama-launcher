# Plan: Portability release (ADR-0012) — compile everywhere, actuate where it can

**Date:** 2026-07-29 · **Source:** the apogee-side integration grill (owner, 2026-07-29) — apogee's `make check` runs on Linux and keeps a Windows cross-build green, so the v1.6.0 facade is unimportable by its first client's CI.
**Design authority:** [ADR-0012](../adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md) (written in the same session as this plan). The client-side plan is `docs/plans/2026-07-29 - 00 - llama-launcher-integration-plan.md` *in the apogee repo*; its item 2 blocks on this plan's release item.

**Conventions for every code item:** follow the `coding-standards` skill; no AI attribution anywhere; CHANGELOG + TDD + README updates are collected in Item 5. The core rule, inherited from the facade plan: **darwin behaviour must not change** — the CLI's output and the facade's darwin semantics stay byte-identical; this plan moves code behind build tags, it does not redesign it.

**Resolved decisions (context for implementers):**

1. **Platform contract (ADR-0012):** darwin full · linux full · windows compiles + HTTP-actuated verbs work, unix-process paths return the new `ErrUnsupported`.
2. **Seam shape:** per-OS files with build tags — a `unix` tag shares darwin+linux process control; windows gets stubs. No `runtime.GOOS` branching in shared code paths.
3. **Two new facade sentinels:** `ErrUnsupported` (the windows stubs) and `ErrStartupTimeout` (the activation health-wait timeout, so a client can attach "may still come up" handling). New exported API ⇒ the release is **v1.7.0** (minor), not v1.6.1.
4. **Verification floor:** the repo gains a cross-compile gate (GOOS linux/windows/darwin) and the layer-1 tests must pass **natively on linux** (they are httptest/tempdir-only — this plan makes that claim checkable for the first time).

---

## Part A — Per-OS seams (behaviour-preserving on darwin)

- [ ] **1. Process control behind a `unix` seam** — `internal/launcher/server.go`, `internal/launcher/backend_ollama.go`, new `internal/launcher/process_unix.go` + `internal/launcher/process_windows.go`
  - `process_unix.go` (`//go:build unix`): `detachedSysProcAttr() *syscall.SysProcAttr` returning `{Setsid: true}`; `signalPID(pid int, sig …) error` and `signalGroup(pid int, …) error` wrapping today's `syscall.Kill(pid, …)` / `syscall.Kill(-pid, …)` calls verbatim (the SIGTERM→SIGKILL escalation in `terminatePID`, `server.go:253-289`, and the legacy-cleanup kill at `server.go:977`).
  - `process_windows.go` (`//go:build windows`): the same signatures; `detachedSysProcAttr` returns `nil` (harmless — the fork paths error before use), every signal func returns an error wrapping the internal unsupported sentinel (Item 3 exports it). `terminatePID` on windows therefore fails fast with that error rather than a build failure.
  - Rewire the five sites: `server.go:74` and `backend_ollama.go:109` (`SysProcAttr: detachedSysProcAttr()`), the three `syscall.Kill` sites through the wrappers. `startManagedServer` and ollama's `TryStart` gain an early windows guard returning the unsupported error (fork-then-fail would leak a child with no process-group control).
  - Tests: existing darwin/linux tests unchanged and green natively on linux (this item is what makes that possible together with Item 2); a windows-tagged compile check rides Item 4's gate (no windows test host — the stubs are error-return one-liners).

- [ ] **2. The menu poll behind per-OS files** — `internal/launcher/ui.go` → `internal/launcher/ui_poll_darwin.go`, `ui_poll_linux.go`, `ui_poll_windows.go`
  - Extract the `FdSet`/`Select` stdin-poll block (`ui.go:87-89` and its setup) into a single `pollStdin(…)` seam; darwin file keeps today's one-value form verbatim; linux file uses the two-value `n, err := syscall.Select(…)` form (the one-line fix the facade plan's Item 5 already prototyped via overlay); windows file returns "interactive menu is not supported on windows" — and `RunInteractiveMenu` surfaces that sentence through the CLI's existing error path (exit code 2), which was the de-facto status quo expressed as a build failure.
  - Tests: menu tests (if any touch the poll) stay green on darwin AND linux; the CLI's zero-arg windows behaviour is asserted only at the compile level (Item 4).

## Part B — Facade additions

- [ ] **3. Two sentinels: `ErrUnsupported`, `ErrStartupTimeout`** — `internal/launcher/` + `launcher/launcher.go`, `launcher/doc.go`
  - Internal: a package sentinel for unsupported-on-this-platform (Item 1's stubs wrap it); `startupTimeoutErr` (`server.go:822-825`) becomes wrapping-based so `errors.Is(err, ErrStartupTimeout)` holds while the message keeps naming the PID and log path byte-identically.
  - Facade: re-export both **by value** beside `ErrConfigNotFound`/`ErrNotRunning` (same `errors.Is`-across-the-boundary discipline); `doc.go` gains the ADR-0012 platform-contract paragraph (the three-platform table in prose) and one sentence on `ErrStartupTimeout` ("the server was left running; a later health success completes the load — observe it yourself").
  - Tests (`launcher/launcher_test.go`): `errors.Is` crosses the boundary for both new sentinels — the timeout one via the existing httptest stand-in forced to stay unhealthy with a shortened wait if the 30 s const is reachable in test, else via the internal error constructor (document which); the unsupported one unit-level on unix by calling the windows constructor directly (the sentinel, not the stub, is the contract).

## Part C — Gate, docs, release

- [ ] **4. Cross-compile gate + native-linux test run** — `Makefile`
  - `make check` (or the closest existing aggregate target) gains a `cross` step: `GOOS=linux`, `GOOS=windows`, `GOOS=darwin` `go build ./...` (+ `go vet` per OS where cheap). Record in the item note the first native `make test` pass on linux — the claim ADR-0012 makes checkable.

- [ ] **5. Documentation pass** — `llama-launcher.TDD.md`, `README.md`, `CHANGELOG.md`
  - TDD §16 gains the platform contract + the two sentinels in the surface table; §5.2 file rows for the new per-OS files. README: a "Supported platforms" note in the library section (darwin/linux full; windows = HTTP verbs + `ErrUnsupported` on process control). CHANGELOG `## 1.7.0`: portability (ADR-0012), the two sentinels, the cross-compile gate.

- [ ] **6. Release v1.7.0** — **owner-run**, same convention as the facade plan's Item 8
  - VERSION → 1.7.0, CHANGELOG dated, tag `v1.7.0` pushed, Homebrew formula bump. Unblocks apogee's integration plan item 2 (`go.mod` requires the tag — apogee never uses a `replace`).
