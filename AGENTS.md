# AGENTS.md

Consult `llama-launcher.TDD.md` for all project details: architecture, build commands, coding standards, and contribution workflow.

Supported LLM Servers: `llamacpp`, `ollama`, `lmstudio` and `splash` — see `CONTEXT.md` for the domain terms and `llama-launcher.TDD.md` for how each one is driven.

## Issue register — beads (`bd`), not a file

Open defects, loose ends and parked feature ideas live in the beads tracker. `TODO.md` was migrated into beads on 2026-09-24 and deleted; the mentions of it that survive in `CHANGELOG.md`, `docs/adr/`, `docs/handoffs/` and archived plans are historical and stay.

- **Storage:** a local embedded Dolt DB in `.beads/embeddeddolt/` (ignored by `.beads/.gitignore`). It syncs through its own remote, `git+https://github.com/airiclenz/llama-launcher-beads.git` (`refs/dolt/data`), never through this repository's `origin`.
- **Fresh clone:** run `bd bootstrap` to hydrate the DB from that remote. Never `bd init` in a clone — it mints a new identity and points the Dolt remote at this repo's `origin`.
- **Export:** `.beads/issues.jsonl` is a passive export (`export.auto: true`), committed so a clone without `bd` can still read the register — never the source of truth. After a batch write, compare it with `bd list` and re-export with `bd export -o .beads/issues.jsonl` if they disagree.
- **Sync:** `bd dolt push` / `bd dolt pull`. Push only when the owner asks, the same as `git push`.
- **CHANGELOG is still the closed trail.** Closing a bead writes no changelog entry — do both.

Everyday commands:

```bash
bd ready                         # open, unblocked work
bd ready --label planned         # designed by a plan — hand to /implement-plan
bd ready --exclude-label planned # still needs design
bd show <id>                     # details, notes, dependencies
bd update <id> --claim           # take it
bd close <id> --reason "..."     # done
```

### Naming and labelling rules

- **Every new bead gets a spoken id:** `bd create --id llama-launcher-<slug> "<title>" ...`. The slug is two to four kebab-case words naming the defect or the work, never the fix (`llama-launcher-api-key-cmd-reruns`, not `llama-launcher-cache-api-key-cmd`). Without `--id`, bd mints a meaningless hash suffix. `--id` exists on `bd create` only — `bd q`, `--file` and `--graph` cannot set it, so create such beads singly.
- **An id is permanent.** bd has no rename and `bd update` has no `--id`; changing one means delete-and-recreate, which drops comments, history and dependency edges. Pick the slug deliberately and `bd show llama-launcher-<slug>` first if it might collide.
- **A bead a plan has designed is labelled, never re-statused:** `bd update <id> --add-label planned --spec-id "<plan path>"`. A custom status would drop it out of `bd ready`, which matches `status == open` literally. A plan's own *Out of scope* list disowns beads it merely mentions — read ownership from the plan's header and item sections.
- **Source labels:** beads migrated from `TODO.md` carry `from-todo`; area labels (`docs`, `tests`, `release`, `security`, `menu`, `cli`, `config`, `keystore`, `windows`, `performance`) are free-form but reuse existing ones first (`bd label list-all`).

### What beads owns — and what it does not

beads replaces `TODO.md` only. `docs/plans/` keeps the numbered What/Tests/Acceptance/commit plan format (a plan is a design artefact, not a TODO list), `docs/adr/` keeps settled decisions, `docs/handoffs/` keeps work-in-flight handoffs, and `CHANGELOG.md` keeps the closed trail.

### bd quirks in this repo

- **bd makes its own git commits.** `bd init` and every `bd config set` that touches `.beads/config.yaml` commit it straight away (`bd init: …`, `bd: update sync.remote`), with `--no-verify`. Never run them with a dirty index, and squash or reword those commits before pushing.
- bd's git hooks are **not** installed (`bd init --skip-hooks`); `.git/hooks/commit-msg` (the attribution stripper) is the only hook. Run `bd hooks install` only as a deliberate decision — it rewires `core.hooksPath`.
