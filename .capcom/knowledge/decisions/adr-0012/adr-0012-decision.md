---
title: "ADR-0012 decision: portability is the library's obligation, so it compiles everywhere and refuses with ErrUnsupported where it cannot act"
---

ADR-0011 made the launcher importable, and its first client immediately found the boundary of
that promise: the facade aliases into `internal/launcher/`, so importing it compiles the whole
package — and the package compiled on darwin only. Native Linux fails on one line (the
interactive menu's `syscall.Select` poll uses the darwin signature, `ui.go:89`); Windows fails
at seven sites (`Setsid` in the two fork paths, `syscall.Kill` in the three termination paths,
`FdSet`/`Select` in the menu poll). The facade-export plan's own Item 5 notes recorded the
Linux failure; the apogee grill (2026-07-29) confirmed both by cross-compilation. Apogee runs
its checks on Linux and keeps a Windows cross-build green, so as shipped the library is
unimportable by its own first client's CI.

**Decision: portability is the library's obligation, not each client's.** The platform-specific
seams move behind per-OS build-tagged files, and the contract becomes: the package **compiles
on darwin, linux and windows; every verb works where its mechanism exists; where it does not,
the verb returns a clean sentinel instead of failing to build.**

- **darwin** — full function, byte-identical behaviour (the status quo).
- **linux** — full function. The process-control paths are POSIX (`Setsid`, `Kill` shared with
  darwin behind a `unix` build tag); the runtime shell-outs (`lsof`, `ps`, `tail`) exist; the
  menu poll needs only the two-value `syscall.Select` form. Nothing degrades.
- **windows** — compiles, and actuates over HTTP: discovery, Ollama/LM Studio model
  load/unload, and activation against an **already-running** server all work; the `lms`
  shell-outs carry no unix dependency. What needs unix process control — forking a managed
  `llama-server` or `ollama serve`, and every kill-by-PID stop path — does not act. Every
  per-OS stub returns an error wrapping the new exported sentinel `ErrUnsupported`, and all
  four refusal paths hand it back wrapped so a client can match it: **starting a managed
  server** (`startManagedServer` asks `requireProcessControl()` before it forks), **the
  interactive menu** (`RunInteractiveMenu` asks `requireInteractiveMenu()` — the menu never
  was supported on windows; the CLI now degrades with a sentence rather than a build error),
  **auto-starting an external Ollama** (`connectExternalServer` wraps the `TryStart` error,
  whose `requireProcessControl()` refusal carries the sentinel) and **the stop verbs**
  (`stopServerAt` asks `requireProcessStop()` once the stop mechanisms have run and the
  server is still reachable, and wraps its refusal: `server at <addr> is still reachable and
  could not be stopped: stopping a server process: …`). The stop guard names the refusal
  after the mechanisms rather than before them, so LM Studio's `lms server stop` hook still
  genuinely stops the server; a stop hook that itself fails keeps its own "stop hook failed"
  error.
