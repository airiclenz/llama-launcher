# The library compiles everywhere and actuates where it can

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
  `llama-server` or `ollama serve`, and every kill-by-PID stop path — returns the new exported
  sentinel `ErrUnsupported`. The interactive menu is likewise not supported on windows (it
  never was); the CLI degrades with a sentence rather than a build error.

Two alternatives were rejected. **Client-side build tags** (each importer fences the launcher
behind `//go:build darwin || linux` and stubs the rest) pushes one library's platform knowledge
into every client forever, and the first client would have had to break its own no-build-tags
posture to adopt it. **Full Windows process management** (Job objects / `taskkill` in place of
signals) implements capability nobody has asked for on a platform this repo has no host to
prove it on; the sentinel keeps the door open — if a Windows user appears, the stub is the
place the real implementation lands, additively.

Riding the same release because the first client's UX needs it: the activation timeout error
(`server did not become healthy…`, which deliberately leaves the server running and names the
PID and log file) becomes `errors.Is`-able as the exported sentinel **`ErrStartupTimeout`**, so
a client can distinguish "the load failed" from "the load outlived my patience and may yet
finish" — the case its own observe loop can complete later. Strict semver would call new
exported API a minor; the owner's call (2026-07-29) is that this ships as **v1.6.1** — the
1.6.x line is the library-facade line, and portability plus the two sentinels complete that
line's own promise of importability rather than open a new one.

Consequences: the facade's platform contract is documented in `launcher/doc.go` and TDD §16;
the build gate grows a GOOS matrix (linux, windows, darwin) so a platform regression fails in
this repo rather than in a client's; the layer-1 tests, being httptest/tempdir-only, now run
natively on Linux and CI can too.
