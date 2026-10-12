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

- `capcom ready`: the issues ready to work on, most urgent first
- `capcom show <id>`: one issue in full
- `capcom create --id <slug> --title <title>`: file a new issue
- `capcom close <id> --reason <text>`: close an issue
- `capcom know tree`: the outline of the Knowledge tree; `capcom know show <id>` reads a Note

Knowledge tree, top level:

- `decisions`: Decision records: why llama-launcher works the way it does, one Note per record with its sections as children
- `docs-layout`: Before reading or changing a design: where the glossary, decisions, plans, handoffs and changelog live
- `glossary`: Domain glossary: the terms to use in code, docs and messages, one Note per term
- `issue-conventions`: Before filing or changing an issue: how issues are named, labelled, planned and closed
<!-- END CAPCOM -->
