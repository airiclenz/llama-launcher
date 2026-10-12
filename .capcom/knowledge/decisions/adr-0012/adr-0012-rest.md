---
title: "ADR-0012 alternatives and consequences: client-side build tags and full Windows process management were rejected, and ErrStartupTimeout ships in v1.6.1"
---

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
