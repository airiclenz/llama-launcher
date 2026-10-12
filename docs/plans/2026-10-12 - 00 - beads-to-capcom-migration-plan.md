# beads to capcom migration plan

**Goal:** replace beads (`bd`) with capcom as the issue register of record, move the glossary (`CONTEXT.md`) and the ADRs (`docs/adr/`) into capcom Knowledge as Note trees the way capcom's own repository holds them, and leave no live trace of beads in the tree.
**Date:** 2026-10-12
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** be173e4

**Sources:**
- capcom `origin/main` (reference repo, `~/Repos/capcom`, fetch first): `docs/migrating-from-beads.md` (steps, field mapping), `docs/format.md` (issue and Note files), `.capcom/knowledge/decisions/adr-0010/adr-0010-decision.md` (Note-tree shape), `.capcom/knowledge/{glossary.md,decisions.md,docs-layout.md}`, `CONTEXT.md`, `docs/adr/README.md`, `AGENTS.md`, `.claude/settings.json`
- llama-launcher at the base: `bd list --all --json` (22 issues: 13 open, 9 closed), `CONTEXT.md`, `docs/adr/*.md` (17 files), `AGENTS.md`

**Ratified design calls (owner, 2026-10-12):**
- **Binary:** the installed `capcom` (Homebrew, 0.4.0 or newer) runs every command; no tree-built binary.
- **Issue ids:** the bead id minus its `llama-launcher-` prefix (`api-key-cmd-reruns`); the full bead id becomes the issue's only alias.
- **Beads archive:** a final `bd export` to `~/Backups/llama-launcher-beads/issues-final.jsonl`, outside the repo; no `bd dolt push`; the GitHub repo `airiclenz/llama-launcher-beads` is left for the owner to archive by hand.
- **Claude hooks:** `capcom init --claude-hooks`; `.claude/settings.json` is un-ignored and committed.
- **0013 brief:** `docs/adr/0013-gateway-decision-brief.md` becomes child Note `adr-0013-brief` of `adr-0013`, verbatim.
- **AGENTS rules:** the beads section's still-valid rules become Notes `issue-conventions` and `docs-layout`; AGENTS.md keeps its intro, a know-tree instruction and the capcom managed block.
- **Old files:** `CONTEXT.md` becomes a pointer stub and a new `docs/adr/README.md` stub is created (each ≤10 lines, capcom's wording adapted); every other file under `docs/adr/` is deleted.
- **ADR cut points:** an ADR splits at `##` headings and at blockquote paragraphs opening `> **Amended YYYY-MM-DD` or `> **Superseded in part YYYY-MM-DD`; text before the first cut is the `-decision` child.
- **Child ids:** `adr-NNNN-` plus one word: `decision`, `why`, `consequences`, `revisit` (Trigger to revisit), `state` / `conflicts` (0006), `router` / `shellout` / `trust` (0008), `exception` / `standalone` / `behavior` / `library` (0013), `amend-MMDD`; a second amendment of the same date is `amend-<one word from its parenthetical>`; a superseded-in-part paragraph is `partial-MMDD`.
- **Supersession:** only `adr-0010` gets `superseded-by: [adr-0015]` (it stays current); no Note is `retired`.
- **Citations:** inside Notes, `[ADR-NNNN](….md)` links become plain `ADR-NNNN` and other relative links become plain path text; human docs outside `.capcom/` link to the Note file (`.capcom/knowledge/decisions/adr-NNNN.md`, `.capcom/knowledge/decisions.md`, `.capcom/knowledge/glossary.md`); code comments citing `ADR-NNNN` stay (capcom resolves them as ids).
- **Split script:** a throwaway `python3` script under `docs/skill-runs/migrate-knowledge/` (gitignored), never committed.

**Standing requirements:**
- skills: coding-standards
- After every item `capcom check` exits 0 with no output and `capcom agents --check` exits 0.
- Text moved from `CONTEXT.md` or an ADR is verbatim except the citation rewrite; only titles, Status lines and summaries are newly written. A term Note's title is `<Term>: <gist>`; every other new title is one self-contained sentence.

**Out of scope:**
- `CHANGELOG.md` (closeout writes the entry), `docs/plans/archived/`, `docs/handoffs/`, `docs/reviews/`, `docs/skill-runs/`, `backend-tests-plan.md`: historical mentions of beads, `TODO.md`, `CONTEXT.md` or `docs/adr/` stay.
- Code comments citing `ADR-NNNN` or `CONTEXT.md`; uninstalling the `bd` binary; the GitHub beads remote; any Go behaviour change.

**Regression check (2026-10-12, be173e4):**
- 1: guard folded (full `.beads/` and `.beads.gate.lock` removal; `git grep -P`)
- 3: guard folded (double-quoted `title:`)
- 4: guard folded (double-quoted `title:`; links-only citation grep)
- 5: guard folded (blockquote cut end; brief Source line; links-only grep; record-line count)
- 6: recast (TDD contribution-workflow beads sentence) and guard folded (anchored glossary link)
- 7: decision folded (`docs/adr/README.md` is a new file)
- 8: guard folded (base AGENTS.md source; `git grep -P`; tracked-only refs)
- 6 (round 2): decision folded (TDD link count ≈85, none to the 0013 brief) and guard folded (link text and prose naming CONTEXT.md / docs/adr reworded)

## 1. Issue register moves from beads to capcom — ✅ DONE (2026-10-12)

NOTES (2026-10-12): bead `llama-launcher-readme-api-key-cmd-row` became `readme-api-key-cmd`, not `readme-api-key-cmd-row`: capcom refuses an id of more than four words (`capcom check`: invalid id); the full bead id is its alias as for every other issue.
NOTES (2026-10-12): the untracked `.beads/` runtime (embedded Dolt DB, backup/) and the root `.beads.gate.lock` were moved to `~/Backups/llama-launcher-beads/` (`dot-beads/`, `.beads.gate.lock`) rather than `rm -rf`ed — the auto-mode classifier refused the delete; the tree result (`test ! -e`) is the same and the Dolt history is kept beside the final export. `.beads.gate.lock` is untracked, so it is not in FILES.
NOTES (2026-10-12): `.beads/` tracked files are already staged as deletions (`git rm`); until the commit `git status --short | grep -c beads` prints 5, after it 0. No notes field, comment or non-blocks dependency other than the two dated notes/three blocks deps existed; no `bd remember` memories; `core.hooksPath` was unset. Also exported `bd list --all --json` to `~/Backups/llama-launcher-beads/list-all.json`.
NOTES (2026-10-12): `internal/launcher/backend_http_test.go:287` comment "(bead llama-launcher-ipv6-host-address-format)" still resolves through the alias but its word "bead" will match item 8’s final `git grep -P` beads check (code comments are out of scope per the plan header).

**What:**
**Goal:** `.capcom/` holds one issue file per bead (22, closed included) that `capcom check` accepts; `capcom list --all` shows the same issues and the same 13 open; `.beads/`, the beads block of `.gitignore` and the beads section of `AGENTS.md` are gone; the AGENTS.md capcom managed block exists; `git config --local beads.role` is unset.
**Approach (assumed at the header base):** confirm `capcom version` ≥ 0.4.0, else stop. Freeze: `mkdir -p ~/Backups/llama-launcher-beads && bd export -o ~/Backups/llama-launcher-beads/issues-final.jsonl` (also `bd list --all --json` into the same dir). `capcom init` (default config; its `plans:` list already holds `docs/plans` and `docs/plans/archived`). Write each issue file per the reference guide's Field mapping: id per the header call, `aliases: [<bead id>]`; statuses open→`open`, closed→`done` (`dropped` only if the reason says abandoned/duplicate); `planned` label dropped and the two `spec_id` plans become `plan:` basenames (`2026-09-24 - 00 - splash-backend-plan`, `2026-09-24 - 01 - splash-wildcard-host-probe-plan`); every other label kept; the three `blocks` dependencies become `deps` by new id; `notes` become dated `## Log` entries by author `beads`. Loop `capcom check` until clean, then `capcom fmt` and `capcom reindex`. Delete `.beads/` (`git rm -r -q .beads && rm -rf .beads`) and `.beads.gate.lock`, the `# Beads / Dolt files` block of `.gitignore`, and AGENTS.md's whole `## Issue register — beads` section (through `### bd quirks in this repo`); run `capcom agents`. `git config --local --unset beads.role`. One commit holds the new `.capcom/` and the removal of `.beads/`.
**Regression guard.** `git rm -r .beads` removes only the 5 tracked files; after the export is verified non-empty run `git rm -r -q .beads && rm -rf .beads`, and delete the root `.beads.gate.lock` with it (the `.gitignore` block that hides it goes). Grep with `git grep -P`: git grep's default ERE does not honour `\b` on macOS.
**Files:** .capcom/config.yaml, .capcom/issues/, .beads/ (removed), .beads.gate.lock (removed, untracked), .gitignore, AGENTS.md
**Read first:** AGENTS.md — "## Issue register — beads" section, "What beads owns", "bd quirks"; .gitignore — "# Beads / Dolt files" block; .beads/.gitignore — ignored runtime set;
capcom origin/main docs/migrating-from-beads.md — Steps 1/7/8, Field mapping, Log entries, Plans; capcom origin/main docs/format.md — Issue files Fields, Aliases, Naming a node;
llama-launcher.TDD.md — contribution workflow step 1 (beads line); internal/launcher/backend_http_test.go — bead-id comment (resolves as alias)
**Tests:** no Go change; the Acceptance commands are the check.
**Acceptance:**
- `capcom check` exits 0 with no output; `capcom fmt --check` and `capcom agents --check` exit 0
- `capcom list --all --json` holds 22 issues, 13 with status `open`; `capcom show llama-launcher-api-key-cmd-reruns` resolves through the alias
- `capcom show splash-wildcard-host-probe` prints the `plan:` basename; `test ! -e .beads`; `test ! -e .beads.gate.lock`; `git config --local --get beads.role` exits 1
- `test -s ~/Backups/llama-launcher-beads/issues-final.jsonl`; `git status --short | grep -c beads` prints 0
- `git grep -P -n -i '\bbeads?\b|\bbd [a-z]|\.beads' -- AGENTS.md .gitignore` prints nothing
**Commit:** `chore(issues): move the issue register from beads to capcom`

## 2. Session briefing through Claude Code hooks — ✅ DONE (2026-10-12)

Depends on item 1.

**What:**
**Goal:** `.claude/settings.json` is tracked and runs `capcom prime` on `SessionStart` (matcher `startup|resume|clear|compact`) and `PreCompact`, as capcom's own file does; no hook anywhere under `.claude/` runs a `bd` command.
**Approach (assumed at the header base):** `.claude/.gitignore` is `*`; add `!.gitignore` and `!settings.json` lines below it. Confirm `.claude/settings.local.json` holds no `bd` hook (delete any such hook group by hand). Run `capcom init --claude-hooks` and commit `.claude/settings.json` and `.claude/.gitignore` only — never `settings.local.json` or `.claude/skills/`.
**Files:** .claude/.gitignore, .claude/settings.json
**Read first:** .claude/.gitignore — `*` (untracked today); .claude/settings.local.json — permissions only, no hooks; capcom origin/main .claude/settings.json, .claude/.gitignore — reference shape;
.claude/skills — symlink to repo skills/, stays ignored; capcom init --claude-hooks — exits 0 on an already-initialized store and adds both groups (probed)
**Tests:** no Go change.
**Acceptance:**
- `git ls-files .claude` lists exactly `.claude/.gitignore` and `.claude/settings.json`
- `python3 -c 'import json;d=json.load(open(".claude/settings.json"));print(sorted(d["hooks"]))'` prints `['PreCompact', 'SessionStart']`, and `grep -c 'capcom prime' .claude/settings.json` prints 2
- `grep -rn '"bd ' .claude/settings.json .claude/settings.local.json` prints nothing
**Commit:** `chore(claude): brief every session with capcom prime`

## 3. Glossary as a Note tree — ✅ DONE (2026-10-12)

Depends on item 1.

**What:**
**Goal:** `.capcom/knowledge/glossary.md` exists with children `glossary-language`, `glossary-verbs` and `glossary-ambiguities`; each of the eight `**Term**:` entries of `CONTEXT.md` is a grandchild Note; the AGENTS.md managed block lists `glossary`; `CONTEXT.md` is unchanged.
**Approach (assumed at the header base):** a throwaway script under `docs/skill-runs/migrate-knowledge/` parses `CONTEXT.md` into its intro paragraph, the `## Language` and `## Verbs` term blocks (definition plus its `_Avoid_:` line) and `## Flagged ambiguities`. Term ids: `llm-server`, `model`, `profile`, `gateway` (Language); `activate`, `load` (`aliases: [unload]`), `start` (`aliases: [stop]`), `starting` (Verbs). `glossary.md` body: the intro paragraph, then one line per section citing its id; each section Note body one line saying what it covers; `glossary-ambiguities` is a leaf holding the bullets verbatim. Titles follow the standing requirement; gists are agent-written. Mirror the reference's `glossary/glossary-<section>/<id>.md` layout. `capcom fmt`, then `capcom agents`.
**Regression guard.** The script writes every `title:` double-quoted (YAML-escaping any `"` in it), as the reference does (capcom origin/main `.capcom/knowledge/glossary/glossary-work/issue.md`): an unquoted `<Term>: <gist>` title is invalid YAML and `capcom check` exits 1, which `capcom fmt` cannot fix.
**Files:** .capcom/knowledge/glossary.md, .capcom/knowledge/glossary/, AGENTS.md
**Read first:** CONTEXT.md — ## Language, ## Verbs, ## Flagged ambiguities, **Gateway** term (ADR-0013/ADR-0002 links); capcom origin/main .capcom/knowledge/glossary.md, glossary/glossary-work/issue.md, glossary/glossary-ambiguities.md;
capcom origin/main docs/format.md — Note files, Aliases (resolution: trim, lowercase, spaces to hyphens); AGENTS.md — capcom managed block
**Tests:** no Go change.
**Acceptance:**
- `capcom check` exits 0 with no output; `capcom agents --check` exits 0; `git diff --quiet HEAD -- CONTEXT.md`
- `capcom know tree glossary` lists the three sections and eight terms; `capcom know show "LLM Server"`, `capcom know show unload` and `capcom know show stop` each resolve
- `capcom know show gateway` prints `ADR-0013` and no `docs/adr`
**Commit:** `docs(knowledge): move the glossary into Knowledge as a Note tree`

## 4. Decision records 0001–0008 as Note trees — ✅ DONE (2026-10-12)

NOTES (2026-10-12): the split runs from a new throwaway `docs/skill-runs/migrate-knowledge/decisions.py` (gitignored, beside item 3's `glossary.py`) rather than inside `glossary.py`; item 5 extends its RECORDS table.
NOTES (2026-10-12): citation call applied to relative links only: the relative CHANGELOG link in ADR-0007 became the repo-root path `CHANGELOG.md`; the absolute llama.cpp #20851 issue link in ADR-0003 stays a markdown link (not a relative link, not matched by the acceptance grep).
NOTES (2026-10-12): child Notes drop the `##` heading line (the title names the section, as the reference does); the 0001 amendment child keeps the blockquote verbatim, including its `> **Amended 2026-09-24.**` opening.

Depends on item 1.

**What:**
**Goal:** `.capcom/knowledge/decisions.md` exists and lists `adr-0001`…`adr-0008`; each is a Note titled with its ADR's H1, whose body opens with a `Status:` line and a 3–5-line summary giving the reading order, and whose children are the ADR's cuts verbatim; `docs/adr/` is unchanged.
**Approach (assumed at the header base):** extend the item-3 script: cut each ADR per the header's cut-point and child-id calls (0001 gets `adr-0001-amend-0924`; 0003 gets `-revisit`; 0006 `-state`, `-conflicts`; 0008 `-router`, `-shellout`, `-trust`). Status line: `Status: Accepted.` or `Status: Accepted; amended YYYY-MM-DD.` Apply the citation call to each child's text. `decisions.md` body follows the reference's: what the records are, the reading-order convention, one line per record (`adr-NNNN`: H1 text) — written now for 0001–0008, extended by item 5. Child titles: `ADR-NNNN <section>: <gist>`. `capcom fmt`, then `capcom agents`.
**Regression guard.** The script double-quotes every `title:` (YAML-escaping any `"`), as the reference adr-0010 child Notes do: the ADR-0004 H1 opens with a backtick and `ADR-NNNN <section>: <gist>` holds a colon, and unquoted both fail `capcom check`. The citation grep looks at links only, so the 0007 CHANGELOG link turned plain text (`…CHANGELOG.md)`) does not trip it.
**Files:** .capcom/knowledge/decisions.md, .capcom/knowledge/decisions/ (adr-0001 … adr-0008 and children), AGENTS.md
**Read first:** docs/adr/0007-profile-activation-idempotency.md — Consequences (CHANGELOG link); docs/adr/0006-instances-are-keyed-by-address.md — How state is stored, Conflict resolution;
docs/adr/0008-mcp-control-plane-adapter.md — router/shellout/trust headings; docs/adr/0001-stop-is-unconditional.md — Amended 2026-09-24 blockquote;
capcom origin/main .capcom/knowledge/decisions.md, decisions/adr-0010.md, decisions/adr-0010/adr-0010-decision.md
**Tests:** no Go change.
**Acceptance:**
- `capcom check` exits 0 with no output; `capcom agents --check` exits 0; `git diff --quiet HEAD -- docs/adr`
- `capcom know show "ADR-0006"` resolves and lists the reading order; `capcom know tree adr-0006` lists `adr-0006-decision`, `-why`, `-state`, `-conflicts`, `-consequences`
- `grep -rn -E '\]\([^)]*(docs/adr|\.md)[^)]*\)' .capcom/knowledge/decisions` prints nothing
**Commit:** `docs(knowledge): move ADRs 0001-0008 into Knowledge`

## 5. Decision records 0009–0016 and the Gateway brief as Note trees — ✅ DONE (2026-10-12)

NOTES (2026-10-12): the decisions.py cut now routes non-quote text after a blockquote cut per record (back into `adr-0011-decision`, or into `adr-0012-rest`) instead of aborting; regenerating left adr-0001…adr-0008 byte-identical after `capcom fmt`.
NOTES (2026-10-12): Status lines beyond item 4's two forms: 0010 `Accepted; amended 2026-09-24 and superseded in part 2026-09-24.`, 0012 `Accepted; amended 2026-07-29 and 2026-09-24.`, 0015 `Accepted; amended 2026-09-24.` (its inline *(Amended 2026-09-24 …)* note in Consequences is not a blockquote cut and stays in `adr-0015-consequences`).
NOTES (2026-10-12): `capcom agents` reported the AGENTS.md block already current (it lists top-level Notes only), so AGENTS.md is unchanged.

Depends on item 4.

**What:**
**Goal:** `adr-0009`…`adr-0016` exist in item 4's shape and `decisions.md` lists all sixteen; `adr-0013` has child `adr-0013-brief` holding the decision brief verbatim below its H1; `adr-0010` carries `superseded-by: [adr-0015]` and its `-amend-0924` and `-partial-0924` children; `docs/adr/` is unchanged.
**Approach (assumed at the header base):** same script and calls as item 4. Amendment children: 0010 `-amend-0924`, `-partial-0924`; 0011 `-amend-0924` (its blockquote sits before `## Why`); 0012 `-amend-0729`, `-amend-0924`, `-amend-windows` (the second 2026-09-24 paragraph, "stop-auth-scope and windows-unsupported plan"). 0013 children `-exception`, `-standalone`, `-behavior`, `-library`, `-consequences`, `-brief`; the brief keeps its own `##` headings inside the one Note. `adr-0010`'s summary says what still holds and that ADR-0015 amends the Starting definition for Splash. `capcom fmt`, then `capcom agents`.
**Regression guard.** A blockquote cut ends at its last `>` line; non-quote text after it goes back into the preceding non-amendment child (0011's lines 53-54 into `adr-0011-decision`) or, in 0012 (no `##` headings), into its own child `adr-0012-rest` (rejected alternatives, `ErrStartupTimeout`, Consequences), which the summary's reading order names. The brief's `Source:` line becomes `Source: ADR-0013.`, its one non-verbatim edit. Titles are double-quoted as in item 4; the citation grep looks at links only (0012's amendment prose naming `docs/adr/` stays verbatim); the record count matches only `^- \`adr-` lines.
**Files:** .capcom/knowledge/decisions.md, .capcom/knowledge/decisions/ (adr-0009 … adr-0016 and children), AGENTS.md
**Read first:** docs/adr/0012-the-library-compiles-everywhere-and-actuates-where-it-can.md — three amendment blockquotes, trailing alternatives/Consequences prose; docs/adr/0011-public-library-facade.md — Amended 2026-09-24 blockquote, ## Why;
docs/adr/0013-gateway-decision-brief.md — Source line, ## Decision requested; docs/adr/0013-gateway-data-plane-sibling.md — exception/standalone/behavior/library headings;
docs/adr/0010-starting-instances-are-visible-and-stoppable.md — Amended / Superseded in part blockquotes; capcom origin/main docs/format.md — Supersession (fmt writes `superseded-by: [adr-0015]` flow style)
**Tests:** no Go change.
**Acceptance:**
- `capcom check` exits 0 with no output; `capcom agents --check` exits 0; `git diff --quiet HEAD -- docs/adr`
- `capcom know show decisions | grep -c '^- `adr-'` prints 16; `capcom know tree adr-0012` lists three `adr-0012-amend-` children
- `capcom know show adr-0013-brief` prints "Decision requested"; `grep -n 'superseded-by: \[adr-0015\]' .capcom/knowledge/decisions/adr-0010.md` matches
- `grep -rn -E '\]\([^)]*(docs/adr|\.md)[^)]*\)' .capcom/knowledge/decisions` prints nothing
**Commit:** `docs(knowledge): move ADRs 0009-0016 and the Gateway brief into Knowledge`

## 6. Human docs and code cite the Notes

Depends on items 3 and 5.

**What:**
Recast at the regression check (2026-10-12).
**Goal:** no live file outside `.capcom/`, `docs/adr/` and `CONTEXT.md` links into `docs/adr/` or to `CONTEXT.md`; every ADR link points at an existing `.capcom/knowledge/decisions/adr-NNNN.md`, every ADR-folder link at `.capcom/knowledge/decisions.md`, every glossary link at `.capcom/knowledge/glossary.md` or the glossary child Note it names.
**Approach (assumed at the header base):** rewrite by the citation call in `llama-launcher.TDD.md` (≈85 links, none targeting the 0013 brief, including its `docs/adr/` index lines that say "ADRs 0001–0016"), `README.md` (ADR links and the closing "ADRs / CONTEXT.md" sentence), `AGENTS.md` line "see `CONTEXT.md` for the domain terms" (→ the `glossary` Note), and the `docs/adr/0008-…md` path comment in `cmd/llama-launcher-mcp/main.go` (→ `ADR-0008`). Link text keeps its `ADR-NNNN` spelling.
**Regression guard.** The rule is every path into `docs/adr/` and every `CONTEXT.md` link in live files; find them with `git grep -n -E 'docs/adr|CONTEXT\.md' -- ':!CHANGELOG.md' ':!docs/plans' ':!docs/handoffs' ':!docs/reviews' ':!docs/skill-runs' ':!backend-tests-plan.md' ':!docs/adr' ':!CONTEXT.md' ':!.capcom'`. Code comments saying "See CONTEXT.md" stay (out of scope; the stub forwards).
**Regression guard.** Item 6 also owns llama-launcher.TDD.md's contribution-workflow sentence (around the "Update the documents" step) that says "Close, update or file the matching beads (`bd`, see `AGENTS.md`) — beads is the issue register; `TODO.md` …": rewrite it to name capcom as the issue register (`capcom close/update/create`, see the `issue-conventions` Note and AGENTS.md). An anchored glossary link targets the child Note file with no anchor: `[CONTEXT.md flagged ambiguities](CONTEXT.md#flagged-ambiguities)` (TDD multi-instance menu paragraph) → `.capcom/knowledge/glossary/glossary-ambiguities.md`.
**Regression guard.** Link text and prose naming `CONTEXT.md` or `docs/adr/` are reworded as well (e.g. `glossary`, `decisions`) — `[CONTEXT.md](…)` at TDD.md:5, :585 and README.md:367, `[CONTEXT.md flagged ambiguities](…)` at TDD.md:140, `[docs/adr/](…)` at TDD.md:6, :1070 — so the guard's grep stays clean; only `ADR-NNNN` link text keeps its spelling.
**Files:** llama-launcher.TDD.md, README.md, AGENTS.md, cmd/llama-launcher-mcp/main.go
**Read first:** llama-launcher.TDD.md — documentation map lines, multi-instance menu paragraph (CONTEXT.md#flagged-ambiguities), LLMServer interface paragraph, contribution workflow steps 1-2 (beads sentence, docs/adr link);
README.md — closing docs sentence, ADR links in stop/Starting/MCP/library/platform sections; AGENTS.md — LLM Servers line; cmd/llama-launcher-mcp/main.go — package doc comment;
internal/launcher/backend.go — LLMServer doc comment (stays); capcom origin/main AGENTS.md — managed block (lists Note titles only, no docs/adr)
**Tests:** `go build ./cmd/llama-launcher-mcp/ && go vet ./cmd/llama-launcher-mcp/`
**Acceptance:**
- the guard's grep prints only `// …CONTEXT.md…` Go comment lines
- every target exists: `grep -oh -E '\.capcom/knowledge/[a-z0-9/-]+\.md' llama-launcher.TDD.md README.md AGENTS.md | sort -u | xargs ls`
- `go build ./cmd/llama-launcher-mcp/` succeeds
- `grep -n -i -E 'beads|\x60bd\x60' llama-launcher.TDD.md` prints nothing
**Commit:** `docs: cite decision records and the glossary by their Notes`

## 7. Pointer stubs replace CONTEXT.md and docs/adr

Depends on item 6.

**What:**
**Goal:** `CONTEXT.md` and `docs/adr/README.md` are pointer stubs of at most 10 lines; no other file remains in `docs/adr/`.
**Approach (assumed at the header base):** adapt the reference's two stubs: `CONTEXT.md` names the `glossary` tree, `capcom know tree glossary`, `capcom know show "<Term>"` (example `"Profile"`) and that terms are edited in place under `.capcom/knowledge/glossary/`; `docs/adr/README.md` (a new file — none exists at the base) names the `decisions` Note, `capcom know show "ADR-0010"`, and that a new record takes the next number (`adr-0017`) in the shape of the existing records, edited under `.capcom/knowledge/decisions/`. `git rm` the 17 files under `docs/adr/`.
**Regression guard.** Item 7 creates `docs/adr/README.md`; it does not exist at the base (header "Old files" call reworded at the regression check).
**Files:** CONTEXT.md, docs/adr/README.md (new), docs/adr/ (17 files removed)
**Read first:** CONTEXT.md — whole file (Language, Verbs, Flagged ambiguities); docs/adr/ — the 17 tracked files (no README.md at base, so the stub is a new file);
capcom origin/main CONTEXT.md, docs/adr/README.md — stub wording; internal/launcher/backend.go — "See CONTEXT.md" comment the stub must forward
**Tests:** no Go change.
**Acceptance:**
- `ls docs/adr` prints only `README.md`; `wc -l < CONTEXT.md` and `wc -l < docs/adr/README.md` are each ≤ 10
- item 6's grep with `':!docs/adr'` and `':!CONTEXT.md'` dropped prints only the stubs' own lines and `// …CONTEXT.md…` Go comment lines
- `capcom check` exits 0 with no output
**Commit:** `docs: leave pointers where the glossary and ADRs were`

## 8. Working rules as Knowledge Notes

Depends on item 7.

**What:**
**Goal:** top-level Notes `issue-conventions` and `docs-layout` exist; AGENTS.md outside the managed block holds only its title, the TDD pointer, the LLM Servers line and a know-tree instruction; the managed block lists both new Notes.
**Approach (assumed at the header base):** `issue-conventions` (labels `[issues]`): a new issue gets `capcom create --id <slug>` with a two-to-four-word kebab slug naming the defect or the work, never the fix (`api-key-cmd-reruns`); `capcom show <slug>` first to avoid a collision; renames go through `capcom mv` (old id stays an alias); an issue a plan designs gets `capcom update <id> --plan "<plan basename>"` (it is then Planned), never a status change; reuse existing labels (`from-todo` marks issues migrated from `TODO.md`); closing an issue writes no `CHANGELOG.md` entry — do both. `docs-layout` (refs `[CONTEXT.md, docs/adr, docs/plans, docs/reviews, llama-launcher.TDD.md, CHANGELOG.md]`): adapt the reference's Note — `glossary`, `decisions`, `docs/plans/` (house plan format; finished ones to `archived/`), `docs/handoffs/`, `docs/reviews/`, the TDD, `CHANGELOG.md` as the closed trail. AGENTS.md: add capcom's "Before starting work, run `capcom know tree --depth 2`, then `capcom know tree <id>` …" paragraph above the managed block. `capcom agents`.
**Regression guard.** Item 8 reads the deleted beads rules from `git show be173e4:AGENTS.md` (item 1 removes them). Every `refs:` entry must be a tracked path, checked per ref with `git ls-files --error-unmatch <ref>` (or `git ls-tree HEAD <ref>`): `docs/handoffs` is ignored wholesale (`docs/handoffs/.gitignore` is `*`) and is dropped from `docs-layout`'s refs, kept in its body text. The final beads grep uses `git grep -P` (git grep's default ERE does not honour `\b` on macOS).
**Files:** .capcom/knowledge/issue-conventions.md, .capcom/knowledge/docs-layout.md, AGENTS.md
**Read first:** AGENTS.md — title, TDD pointer, LLM Servers line, managed block; capcom origin/main .capcom/knowledge/docs-layout.md — reference Note and refs; capcom origin/main AGENTS.md — know-tree paragraph;
capcom origin/main docs/format.md — Refs section, Managed block section; docs/handoffs/.gitignore — ignores the whole folder
**Tests:** no Go change.
**Acceptance:**
- `capcom check` exits 0 with no output; `capcom agents --check` exits 0; `capcom know verify` reports nothing
- `grep -n 'capcom know tree --depth 2' AGENTS.md` matches outside the managed block; the block lists `issue-conventions` and `docs-layout`
- every `refs:` entry of `docs-layout` passes `git ls-files --error-unmatch`
- `git grep -P -n -i '\bbeads?\b|\bbd [a-z]|\.beads|issues\.jsonl' -- ':!CHANGELOG.md' ':!docs/plans' ':!docs/handoffs' ':!docs/reviews' ':!docs/skill-runs' ':!.capcom'` prints nothing
**Commit:** `docs(knowledge): record the issue and docs conventions as Notes`
