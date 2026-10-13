---
title: "ADR-0017 consequences: Homebrew ships prebuilt macOS and Linux binaries, and a new VERSION replaces the formula revision bump"
---

- Homebrew ships prebuilt binaries for macOS and Linux on amd64 and arm64; installing needs no Go
  toolchain, and the formula installs both `llama-launcher` and `llama-launcher-mcp`.
- A new VERSION, and so a new tag, is the only way to change what the formula installs. The
  formula `revision` bump for packaging-only changes (the former practice in the TDD's §13
  Homebrew text) is superseded: a packaging change is cut as a patch release.
- The formula in `airiclenz/homebrew-tap` is generated; edit
  `cmd/release/templates/llama-launcher.rb.tmpl`, never the tap, and fetch the tap before
  touching it by hand, since other projects' releases push there too.
- A cut needs green CI on `main`; red CI blocks it until fixed.
- An aborted cut before `publish` is undone with `git reset --keep HEAD~2`; once `publish` has
  pushed, a broken release is fixed with a new version.
- `github.com/spf13/cobra` is a root-module dependency that only `cmd/release` imports; no
  shipped binary links it.
- The maintainer runbook is `docs/releasing.md`.
