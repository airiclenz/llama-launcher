# AGENTS.md

Consult `llama-launcher.TDD.md` for all project details: architecture, build commands, coding standards, and contribution workflow.

Supported LLM Servers: `llamacpp`, `ollama`, `lmstudio` and `splash` — see the `glossary` Note (`capcom know show glossary`) for the domain terms and `llama-launcher.TDD.md` for how each one is driven.

Project knowledge lives in capcom's Knowledge tree (listed at the end of this file). Before
starting work, run `capcom know tree --depth 2`, then `capcom know tree <id>` to open a branch,
and `capcom know show <id>` on every Note whose title matches the task. `--depth 2` prints the
top level and its direct children only.

<!-- BEGIN CAPCOM -->
<!-- Managed by capcom: run "capcom agents" to refresh. Edits inside these markers are overwritten. -->
## Issues and knowledge: capcom

capcom is the issue register of record for this repository. Issues and Knowledge Notes are
markdown files under `.capcom/`, versioned with the code; track work there and nowhere else.

Without a `capcom` binary, install it from https://github.com/airiclenz/capcom#install.

- `capcom next`: the most urgent ready issue, in full
- `capcom ready`: the issues ready to work on, most urgent first
- `capcom show <id>`: one issue in full, with what blocks it
- `capcom context <id>`: the issue with its deps, plan and the Notes that apply; run it before you start
- `capcom start <id>`: claim it and take the Tree lock before you start
- `capcom log <id> <text>`: note progress as work lands
- `capcom close <id> --reason <text>`: close an issue; it names the issues this made ready
- `capcom finish <id> --reason <text>`: close it and free the Tree lock; it names the issue to work on next
- `capcom reopen <id> --reason <text>`: undo a close
- `capcom create --id <slug> --title <title> [--dep <id>]`: file a new issue
- `capcom dep add <id> <dep-id>`: make an issue wait for another; `capcom dep list <id>` shows both sides
- `capcom list --tree --depth 2`: the outline of the issues
- `capcom know tree`: the outline of the Knowledge tree; `capcom know show <id>` reads a Note

Each command's flags are in `capcom <cmd> --help`.

An issue's body says what is wrong or wanted, why, and how to tell it is done (`## Acceptance`),
readable on its own; a plan says how and in what order.

Where things are: an issue is `.capcom/issues/<id>.md`, and its children sit in the directory
`.capcom/issues/<id>/` beside it; Notes follow the same shape under `.capcom/knowledge/`.
Prefer capcom commands; after a hand edit run `capcom check`.

A standing issue (`standing: true` in its file) only groups its children: never claim or close it.

Tree lock: before a change that spans several files or commits, it is recommended to claim this
worktree with `capcom lock acquire --purpose <text>`. capcom keeps the Lock token for the session:
running the same acquire again resumes the lock after a restart or compaction and prints the token
again (or pass `--token <token>`).
`capcom start <id>` takes the lock for an issue, and running the same start again resumes it too.
`capcom lock touch --token <token>` renews it during long work,
`capcom lock release --token <token>` frees it when done. When `capcom lock check --token <token>` exits 3, or
`capcom lock check` does while you hold no token, another session holds this worktree: ask the user
before editing it. The lock is signalled, not enforced: a capcom write is never refused, but it
warns, naming the holder, while the lock is held for a purpose other than the issue it writes; if
the lock is yours, carry on.

Knowledge tree, top level:

- `decisions`: Decision records: why llama-launcher works the way it does, one Note per record with its sections as children
- `docs-layout`: Before reading or changing a design: where the glossary, decisions, plans, handoffs and changelog live
- `glossary`: Domain glossary: the terms to use in code, docs and messages, one Note per term
- `issue-conventions`: Before filing or changing an issue: how issues are named, labelled, planned and closed
<!-- END CAPCOM -->
