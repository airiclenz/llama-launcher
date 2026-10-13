---
title: "ADR-0017 why: a hand-cut source-build formula was macOS-only and lived in memory; capcom's pipeline was ported, not redesigned"
---

Before this record a release was cut by hand: tag, let the formula in `airiclenz/homebrew-tap`
build from the release source tarball with Go, and edit the formula by hand, with a `revision`
bump for packaging-only changes. That made Homebrew a macOS-only source build that needed a Go
toolchain on every install, and every release step lived in memory rather than in code.
Publishing is exact, repeatable work that prose reproduces badly, so it became a program; the
judgment (the notes and the version) stays with an agent through the two skills.

capcom already had this pipeline, tested and in use, so it was ported rather than redesigned: one
program shape across the owner's tools, and fixes flow between them. apogee's recovery for a
release uploaded without `SHA256SUMS` was folded in because capcom's `publish` could not converge
from that state.

Rejected:

- **Scoop, winget and a Windows archive.** Windows is not a design target (ADR-0012): the
  launcher has no process control and no interactive menu there, so a Windows package would ship
  a binary that cannot do the job.
- **Keeping the source-build formula.** It needs Go on every install and compiles on the user's
  machine; prebuilt archives from one CGO-free `dist` cover macOS and Linux on both
  architectures, the same bytes that `smoke` checks.
- **Publishing from CI with stored secrets.** Needs a personal token for the tap, a second
  trigger the `GITHUB_TOKEN`-made tag cannot fire, and notes that an agent writes.
- **Bracketed, dated changelog headings as capcom has.** Would rewrite every existing release
  heading for no reader's benefit; `prepare` adapts to the format instead.
