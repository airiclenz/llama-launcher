---
title: "ADR-0017 decision: cmd/release builds four darwin/linux archives, gates on green CI and publishes the GitHub release and the Homebrew formula"
---

A llama-launcher release is cut from a clone of this repository with the Go toolchain, `git` and
`gh`, on the owner's machine and never in CI. The pipeline is capcom's (its ADR 0008), ported by
copying its files and renaming the project literals, with the calls the owner ratified on
2026-10-12:

- **Channels:** the GitHub release `airiclenz/llama-launcher` and the Homebrew formula
  `Formula/llama-launcher.rb` in `airiclenz/homebrew-tap`. No Scoop, no winget, no Windows
  archive.
- **Targets:** darwin and linux on amd64 and arm64: four archives, each holding both binaries
  (`llama-launcher` and `llama-launcher-mcp`) with `LICENSE.md` and `README.md`, plus
  `SHA256SUMS`.
- **Changelog:** the existing `## Unreleased` / `## X.Y.Z` headings (no brackets, no date) stay;
  `prepare` rolls up that format.
- **cobra:** `github.com/spf13/cobra` is a dependency of the root module, used only by
  `cmd/release`, wired as capcom's `main.go` is.
- **Skills:** capcom's two release skills are ported as `release-llama-launcher` (the cut) and
  `release-notes-llama-launcher` (read-only notes). They do the judgment half (what shipped, the
  notes, the version); the program does the deterministic half.

**The dev tool.** `cmd/release` runs as `go run ./cmd/release <verb>` and is never a release
asset. Its verbs:

- `dist` cross-builds both binaries for the four targets with `CGO_ENABLED=0 go build -trimpath`,
  each stamped with the bare `VERSION` through the same `-X` ldflags the Makefile uses, packs
  them into `llama-launcher_<version>_<os>_<arch>.tar.gz` and writes `SHA256SUMS`. The archives
  are byte-identical across runs over the same commit.
- `status [vX.Y.Z]` is read-only apart from fetching origin: the release gate, the baseline
  release, and each publish step (tag, release, homebrew, smoke) as done, pending, setup-pending
  or unknown.
- `prepare vX.Y.Z` makes two **local** commits, the CHANGELOG rollup and then the `VERSION` bump,
  and pushes nothing.
- `publish vX.Y.Z --notes-file F` pushes `HEAD` to `main`, waits for the CI-made tag, builds the
  archives in a fresh clone at the tag, creates the GitHub release, renders the formula from the
  release's `SHA256SUMS` and commits `llama-launcher X.Y.Z` straight to the tap, then runs
  `smoke`. `--dry-run` prints the plan instead. It never prompts and never reads stdin.
- `smoke [vX.Y.Z]` checks a published release from the outside: the annotated tag, the archives
  against `SHA256SUMS`, each binary's `vcs.revision` against the tagged commit, the host binaries'
  version, and `brew upgrade llama-launcher` where Homebrew has it installed.

`make dist`, `make release-status` and `make release-smoke` wrap the matching verbs; `prepare` and
`publish` have no make target.

**The tag.** `VERSION` holds a bare `X.Y.Z` and the tag is `v` + VERSION. The
`tag-on-version-bump` workflow tags the commit that changes `VERSION` on `main`; nothing tags by
hand.

**The gate is green CI on the gated commit.** The gate commit is `HEAD` with its trailing
first-parent commits that touch only `CHANGELOG.md` and/or `VERSION` peeled off. A cut clears
only when the tree is clean, the gate commit is on `origin/main`, `origin/main` is contained in
`HEAD`, and `ci.yml` has a successful run for the gate commit or a peeled commit `origin/main`
contains. There is no bypass.

**`publish` converges.** Before each step it asks the remote whether that step is done and skips
it if so: the tag exists; the release carries all four archives and `SHA256SUMS`; the formula
pins the version. A release that holds archives but no `SHA256SUMS` is completed after its
archives are checked against the rebuilt ones. There is no local state file. A missing tap
repository leaves the homebrew step setup-pending: `publish` names the skip and still succeeds.

**The formula is generated and installs prebuilt binaries.** It is rendered whole from
`cmd/release/templates/llama-launcher.rb.tmpl`, one `url`/`sha256` pair per platform, and
installs both binaries for macOS and Linux without a Go toolchain. A hand edit in the tap is
overwritten at the next release.

**Tests stay hermetic.** The verbs' tests never run `git`, `gh` or `go`: what they ask of the
checkout and its GitHub repository goes through the semantic `repo` seam (`repo.go`, faked by
`memRepo`), and everything else through the runner seam (`runner.go`). The one exception is
`versionbump_test.go`, which runs `.github/scripts/version-bump.sh` against a throwaway git
repository in `t.TempDir()`.
