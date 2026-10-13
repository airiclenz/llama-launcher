# Releasing llama-launcher

A release is cut locally, from a clone of this repository, on macOS or Linux. The
`release-llama-launcher` skill does the judgment work (release notes, the version) and hands the
rest to `go run ./cmd/release`, which builds, publishes and checks. Why it is split this way is
recorded in [ADR-0017](../.capcom/knowledge/decisions/adr-0017.md).

One release publishes to two channels:

| Channel | Where | How |
| --- | --- | --- |
| GitHub release | `airiclenz/llama-launcher` | four darwin/linux archives and `SHA256SUMS` |
| Homebrew | `airiclenz/homebrew-tap`, `Formula/llama-launcher.rb` | a direct `llama-launcher X.Y.Z` commit |

Each archive holds both binaries, `llama-launcher` and `llama-launcher-mcp`, with `LICENSE.md` and
`README.md`. The formula installs the prebuilt binaries for macOS and Linux on amd64 and arm64;
installing needs no Go toolchain. There is no Windows archive and no Scoop or winget package.

## Prerequisites

- **Go**, at the version `go.mod` names, and **git**.
- **gh**, signed in with `gh auth login` and allowed to push to `airiclenz/llama-launcher` and
  `airiclenz/homebrew-tap`. The tap is cloned with `gh repo clone` and pushed with `git push`, so
  git must be able to push over the protocol gh uses (`gh auth setup-git` sets that up for HTTPS).
- Optional, for the smoke checks: Homebrew with llama-launcher installed through it, so `smoke`
  can check that `brew upgrade llama-launcher` reaches the new version. A check whose tool is
  missing prints `SKIP`.

## One-time setup

- **The tap.** `publish` writes into `airiclenz/homebrew-tap` and never creates it. While it does
  not exist, `status` reports the homebrew step `setup-pending` and `publish` skips it and still
  succeeds. Create it, then run `publish` again for the same version:

  ```sh
  gh repo create airiclenz/homebrew-tap --public
  ```

  An existing but empty repository is fine: the formula is what `publish` writes. The tap is
  shared with other projects' releases, so fetch it before any hand change there.

## Cutting a release

Ask an agent to cut the release: the `release-llama-launcher` skill (`/release-llama-launcher` in
Claude Code; canonical file `skills/release-llama-launcher/SKILL.md`) checks the gate,
drafts the notes with `release-notes-llama-launcher`, has you approve them and confirm the
version, runs `prepare`, shows you a dry run, and runs `publish` after one explicit confirmation.
To only see what shipped, run `release-notes-llama-launcher`; it never publishes.

By hand, the same steps are:

```sh
go run ./cmd/release status                       # the gate, the baseline, each publish step
go run ./cmd/release prepare vX.Y.Z               # two local commits: CHANGELOG rollup, VERSION bump
go run ./cmd/release publish vX.Y.Z --notes-file /tmp/notes.md --dry-run
go run ./cmd/release publish vX.Y.Z --notes-file /tmp/notes.md
```

Keep the notes file outside the repository, so it never dirties the tree the gate checks.
`make dist`, `make release-status [RELEASE=vX.Y.Z]` and `make release-smoke [RELEASE=vX.Y.Z]`
wrap the matching verbs; `prepare` and `publish` are run directly.

### The verbs

- `dist [--src DIR] [--out DIR]` cross-builds `llama-launcher` and `llama-launcher-mcp` for linux
  and darwin on amd64 and arm64, each stamped with the bare `VERSION`, into
  `llama-launcher_<version>_<os>_<arch>.tar.gz` plus `SHA256SUMS`, by default under `dist/`
  (ignored by git; emptied first). The archives are byte-identical across runs over the same
  commit. `publish` runs the same build itself, in a fresh clone at the tag.
- `status [vX.Y.Z] [--json]` is read-only apart from fetching origin. It reports the gate, the
  baseline release and each publish step (tag, release, homebrew, smoke) as done, pending,
  setup-pending or unknown. It exits 0 when the gate clears and 1 when it is blocked or a check
  could not run, printing its report either way.
- `prepare vX.Y.Z` commits `docs(changelog): roll up X.Y.Z`, which moves everything under
  `## Unreleased` in `CHANGELOG.md` under a new `## X.Y.Z` heading and leaves an empty
  `## Unreleased` above it, then `chore(version): bump version to X.Y.Z`, which writes the bare
  `X.Y.Z` to `VERSION`. It fetches origin's `main` and its tags first, and refuses when that
  fails. It pushes nothing and refuses on a dirty tree, an empty `## Unreleased`, an existing
  heading for the version, or a version not above the highest `vX.Y.Z` tag.
- `publish vX.Y.Z --notes-file F [--dry-run] [--tag-timeout D]` pushes `HEAD` to `main`, waits
  (10 minutes by default) for CI to tag the `VERSION` bump, creates the GitHub release, commits
  the rendered formula to the tap, and finally runs `smoke`. It never prompts and never reads
  stdin.
- `smoke [vX.Y.Z] [--dist-dir D]` checks a published release from the outside: the annotated
  tag, the downloaded archives against `SHA256SUMS`, each binary's build stamp against the tagged
  commit, the host archive's two binaries reporting the bare version, and Homebrew.

Every verb prints its full description with `go run ./cmd/release <verb> --help`.

### The gate

`status` and `publish` clear a cut only when the working tree is clean, the gate commit (`HEAD`
with its trailing `CHANGELOG.md`/`VERSION`-only commits peeled off) is on `origin/main`,
`origin/main` is contained in `HEAD`, and `ci.yml` has a successful run for the gate commit or one
of the peeled commits. There is no bypass: red CI blocks the release until it is fixed.

### The tag

You never tag. `VERSION` holds a bare `X.Y.Z` and the tag is `v` + VERSION: when a push to `main`
changes `VERSION`, the `tag-on-version-bump` workflow creates the annotated tag on that commit.
`publish` waits for it.

### The formula

The formula is rendered whole from `cmd/release/templates/llama-launcher.rb.tmpl` with the hashes
from the release's `SHA256SUMS`; a hand edit in the tap is overwritten at the next release. A
packaging change, such as a fix to the formula, ships as a new version; there is no formula
`revision` bump.

## Recovery

`publish` converges: before each step it asks the remote whether that step is already done and
skips it if so. After a failure, fix the cause and run the **same** command again. There is no
local state to clean up, and a second run cannot duplicate a release or a tap commit.
`go run ./cmd/release status vX.Y.Z` shows which steps are done.

- A step reported `setup-pending` did not fail the run; do its one-time setup (above) and run
  `publish` again.
- The tag did not appear within `--tag-timeout`: check the `tag-on-version-bump` run on GitHub,
  then run `publish` again, with a longer `--tag-timeout` if CI is slow.
- A release that carries some archives but not all of them, or no `SHA256SUMS`, is completed by
  the next run: `publish` rebuilds at the tag, checks the rebuilt sums against the release's
  `SHA256SUMS` (or, when it has none, against the archives already uploaded) and uploads only
  what is missing. A mismatch stops the run, naming the way out, instead of overwriting an asset.
- A failed smoke check fails the run after everything else is published; fix the cause and run
  `go run ./cmd/release smoke vX.Y.Z` (or `publish` again) to confirm.

To abandon a cut before `publish` has run, undo the two `prepare` commits with
`git reset --keep HEAD~2`. Once `publish` has pushed, nothing is undone; a broken release is fixed
with a new version.
