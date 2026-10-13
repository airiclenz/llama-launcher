# llama-launcher facts — the repository this run describes

A shared include, verified against the tree when the skill was written. Read it
after `_shared.md`; when the tree contradicts a line here, trust the tree and
say so on your `FLAG` line.

## The baseline (`BASELINE`) and the diff base

The file is JSON. Use `baseline.tag` (the last **published** GitHub release,
e.g. `v1.8.1`), `baseline.published` (its publish time), `baseline.range`
(`<tag>..HEAD`) and `baseline.commits`. Ignore `gate`, `steps` and
`highestTag`. `version` is the checkout's `VERSION` file with a `v` in front;
it is never the release's number — that is the dispatch's `VERSION:` key,
`unreleased` when absent.

**No published release yet.** Then `baseline.tag` and `baseline.published` are
`""` and `baseline.range` is `HEAD`: the baseline is the **whole history**.
This is a normal state, never a reason to stop. In it:

- **The log range** is `baseline.range` either way: `git log HEAD` lists every
  commit when there is no tag.
- **The diff base** is `baseline.tag`, or with no tag the root commit the
  dispatch passes as `ROOT:` (from `git rev-list --max-parents=0 HEAD`).
  Every `git diff <diff base>` compares that commit with the working tree;
  `git diff <diff base> HEAD` covers the committed range alone.
- Where a prompt writes `<baseline.tag>` into a heading or title, write
  `the first commit` instead.

## Changelog

- File: `CHANGELOG.md`. Unreleased heading, literally: `## Unreleased` (no
  brackets).
- Release heading shape: `## X.Y.Z` (no `v`, no brackets, no date), e.g.
  `## 1.8.1`. Entries collect under `## Unreleased` until `prepare` rolls them
  under a new heading in its `docs(changelog): roll up X.Y.Z` commit.
- Under each heading the entries are grouped into `### Added`, `### Changed`
  and `### Fixed` subsections (`### Removed`, `### Deprecated` and
  `### Security` are read the same way when present).
- An entry is one `- ` bullet: plain prose naming commands, flags, config keys
  and files in backticks, often several sentences long. Most entries open with
  a bold lead (`- **Menu:** …`, `- **Configurable startup wait.** …`); some
  have none.

## Commit conventions

Conventional commits, `type(scope): subject` — types `feat`, `fix`, `docs`,
`ci`, `build`, `refactor`, `test`, `chore`; scopes are package or area names
(`menu`, `mcp`, `launcher`, `config`, `release`, …). Bookkeeping that ships
nothing: `chore(plans)`, `docs(plans)`, `docs(plan)`, `chore(issues)`,
`docs(knowledge)`, `docs(changelog)` (including `prepare`'s
`docs(changelog): roll up X.Y.Z`), `chore(version)` (including `prepare`'s
`chore(version): bump version to X.Y.Z`), and any commit touching only
`.capcom/issues/`, `.capcom/knowledge/` (except the glossary,
`.capcom/knowledge/glossary/`), `docs/plans/`, `docs/handoffs/` or
`docs/skill-runs/`. Ticket ids are capcom issue ids (llama-launcher tracks its
issues with capcom): the slug of a file under `.capcom/issues/` (e.g.
`api-key-cmd-reruns`), with no prefix; an issue may also carry an old
`llama-launcher-…` beads id as an alias. No AI attribution trailers. Plans
live in `docs/plans/`, decision records in `.capcom/knowledge/decisions/`.

## User surfaces — where a change becomes visible

| Surface | Paths |
|---|---|
| CLI commands & flags | `internal/launcher/cli.go` (the verbs `load`, `unload`, `start`, `stop`, `status`, `list`, `logs`, `validate`, `init`, `reset` and their flags; skip `_test.go`), `main.go` |
| Interactive menu | `internal/launcher/menu.go`, `internal/launcher/ui.go`, `internal/launcher/frame.go`, `internal/launcher/progress.go` (what the TUI shows and the keys it takes) |
| Servers driven | `internal/launcher/backend*.go` (llamacpp, ollama, lmstudio, splash), `internal/launcher/server.go`, `internal/launcher/discovery.go` |
| Config keys & defaults | `internal/launcher/config.go`, `internal/launcher/configwrite.go`, `internal/launcher/defaults/config.yaml` (what `init` writes), `internal/keystore/` (API-key storage) |
| MCP server | `cmd/llama-launcher-mcp/` (its tools `list_profiles`, `server_status`, `tail_log`, `load_profile`, …, flags, allowlist and config) |
| Go library | `launcher/` (the public package other programs import) |
| Agent integration | `skills/manage-llm-server/` (the shipped agent skill) |
| Docs | `README.md`, `llama-launcher.TDD.md`, `docs/releasing.md`, `.capcom/knowledge/glossary/` |
| Install & packaging | `.github/workflows/`, `cmd/release/templates/`, `Makefile` |
| Emitted strings | user messages and usage text in `internal/launcher/cli.go`, `internal/launcher/menu.go` and `cmd/llama-launcher-mcp/` |

`VERSION` moves with every release and is not a surface.
