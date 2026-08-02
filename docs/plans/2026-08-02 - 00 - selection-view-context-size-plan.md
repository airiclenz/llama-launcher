# Plan: context-size column in the Profile selection list

- **Goal:** Show each Profile's configured context size (e.g. `65K`, `131K`) as a
  right-aligned column between the Profile title and the `[server]` tag, on every
  surface that lists Profiles.
- **Date:** 2026-08-02
- **Status:** not started
- **Authoritative sources (in precedence order):**
  1. The **Design decisions** section below — the user's ratified answers of 2026-08-02;
     they qualify the mock and win over any conflicting item text.
  2. `docs/context-design.txt` — the visual mock (spacing in it is illustrative except
     where a Decision pins it).
  3. `llama-launcher.TDD.md` §4.7 (the every-surface list-rendering contract) and the
     ParamSpecs display rule in `internal/launcher/backend.go` (interface doc on
     `ParamSpecs()`: a parameter the server never receives is never displayed).
- **Standing requirements:**
  - Execute this plan with the `coding-standards` skill forwarded
    (invoke as: implement-plan "<this file>" with skills: coding-standards).
  - Commit messages carry **no** AI-attribution trailers or footers of any kind.
  - Doc amendments (TDD / README / CHANGELOG) are owned **exclusively by item 5** —
    code items 1–4 must not touch those files, the repo's "After Changing Code"
    checklist notwithstanding.
  - Any authorized deviation from item text must land as a dated `NOTES:` line under
    the item.
- **Out of scope:**
  - Parsing `-c` / `--ctx-size` out of a Profile's `extra_args` (item 5 documents this
    as a known limitation instead).
  - Querying live or default context sizes from running servers (`QueryLiveParams`,
    server-side defaults); the column shows configured values only.
  - Sending `num_ctx` / context length to Ollama, or any backend behaviour change.
  - Regenerating the README screenshots `media/screen_1.png` / `media/screen_2.png`
    (manual follow-up for the user).
  - The MCP adapter (`cmd/llama-launcher-mcp/`) — it shells out to `list --json`,
    which already emits `context_size`.
  - Changes to `internal/launcher/defaults/config.yaml` or the README's embedded copy
    of it.
  - The "Show model config" pop-up (`formatProfileParams`) — it already displays
    context size where applicable.

## Design decisions (ratified by the user, 2026-08-02)

- **D1 — Ollama rows show a blank cell.** The column only shows values the server
  actually receives. Displayability is derived from the Profile's resolved LLM
  Server's `ParamSpecs()` containing the context-size spec (`specContextSize`,
  `internal/launcher/backend.go`) — never from a hardcoded backend-name check. Ollama's
  `ParamSpecs()` is empty, so its rows render an empty cell; a future Ollama
  context-size feature would light the column up automatically. This preserves the
  v1.4.6 rule pinned by `TestFormatProfileParams_OllamaShowsNoParams`.
- **D2 — All list surfaces get the column:** `buildProfileItems` (TUI menu),
  `buildSimpleProfileLines` (non-TTY numbered fallback), and `cmdList`
  (`llama-launcher list` text output), per the TDD §4.7 single-contract rule.
  `list --json` already emits `context_size` and is unchanged.
- **D3 — Version bump to 1.6.2 is explicitly user-authorized** (AskUserQuestion answer
  "Full 1.6.2 bump", 2026-08-02). Item 6 performs it; item 5 writes the CHANGELOG
  entry under a new `## 1.6.2` heading.
- **D4 — Number format** (derived from the mock: 65536→`65K`, 131072→`131K`):
  integer floor-division. `n < 1000` → the number verbatim; `n < 1_000_000` →
  `n/1000` + `K`; otherwise `n/1_000_000` + `M`. Examples: 512→`512`, 4096→`4K`,
  16384→`16K`, 32768→`32K`, 65536→`65K`, 98304→`98K`, 131072→`131K`, 1048576→`1M`.
- **D5 — The value shown is the effective (merged) one:**
  `mergeParams(cfg.Defaults, p.ProfileParams).ContextSize` — same semantics as
  `list --json`. Never call `ResolveProfile` in a row builder: it stats model files,
  and the menu rebuilds on every 1-second repaint tick.
- **D6 — Column layout:** the column appears only when at least one rendered Profile
  row is displayable (has a non-nil merged context size AND passes the D1 gate) —
  analogous to the `anyFav` gating of the ★ marker. Cells are right-aligned to the
  widest cell; non-displayable rows get a same-width run of spaces so the `[server]`
  tags stay aligned. Two spaces separate the context cell from the `[` of the tag
  (matching `cmdList`'s two-space column separator). With a single enabled server
  (no tag column), the context cell is the whole description. The ★ favourite marker
  stays rightmost and aligned (the existing `favouriteSuffix` math keeps working
  because the cell joins the description before `maxDescWidth` is computed). Action
  rows ("Edit config", "Stop server", "Show log") and separators are untouched.
- **D7 — `extra_args` is ignored.** A `-c` value smuggled into `extra_args` is not
  parsed; item 5 records this as a known limitation.

## 1. `formatContextSize` helper — ✅ DONE (2026-08-02)

**What:** In `internal/launcher/menu.go` (near the other list-rendering helpers such
as `favouriteSuffix`), add `formatContextSize(n int) string` implementing Decision D4
exactly. Pure function, no I/O. Callers are responsible for the nil-check on
`*int` fields — the helper takes a plain `int`.

**Tests:** In `internal/launcher/menu_test.go`, add table-driven
`TestFormatContextSize` covering at minimum: 512→`512`, 4096→`4K`, 16384→`16K`,
32768→`32K`, 65536→`65K`, 98304→`98K`, 131072→`131K`, 1048576→`1M`, 0→`0`.

**Acceptance:**
```
go test ./internal/launcher/ -run TestFormatContextSize -v
make check
```

**Commit:** `feat(menu): add formatContextSize helper for compact token counts`

## 2. Context column in the TUI selection menu (`buildProfileItems`)

Depends on item 1.

**What:** In `internal/launcher/menu.go`, extend `buildProfileItems` (currently
~L556–595) to build each row's description as
`<context cell><two spaces + [server] tag when present><favourite suffix>` per
Decisions D1, D5, D6:

- Per Profile: merged value via `mergeParams(cfg.Defaults, p.ProfileParams)` (D5).
- Displayability gate (D1): the Profile's resolved server (`resolveProfileServer`)
  must expose the context-size spec in its `ParamSpecs()`. Implement this as a small
  helper (e.g. in `internal/launcher/backend.go` next to `specContextSize`) that
  looks the server up via `GetLLMServer` and scans its specs; it must be nil-safe for
  unknown/unregistered server names and must derive identity from `specContextSize`
  itself (its label constant), not from a backend-name switch — Go forbids `==` on
  structs with func fields, so label comparison against `specContextSize`'s label is
  the expected mechanism.
- Column-wide layout per D6: presence gate, right-alignment via `visibleWidth`
  (never `len`), blank same-width cells, tag alignment preserved, ★ rightmost.
- The single-enabled-server case (today: empty descriptions) now renders the context
  cell alone when the column is present.
- Do not touch `selectMenu`, `menuItem`, or the action-row construction — the column
  lives entirely inside the description string, like the ★ marker does.

**Tests:** In `internal/launcher/menu_test.go` (first direct coverage of
`buildProfileItems` — construct `*Config` values inline with real registered server
names `llamacpp` / `lmstudio` / `ollama`, per existing test convention):

- `TestBuildProfileItems_ContextColumn`: two enabled servers; llamacpp Profiles with
  `context_size` 65536 and 131072, an Ollama Profile **with** `context_size` set.
  Assert the llamacpp cells render `65K` / `131K` right-aligned (equal
  `visibleWidth` up to the `[`), the Ollama row's cell is blank spaces of the same
  width, and every closing `]` aligns.
- `TestBuildProfileItems_ContextFromDefaults`: Profile-level nil,
  `Defaults.ContextSize` set → the merged value is shown (D5).
- `TestBuildProfileItems_NoContextColumn`: no Profile displayable → descriptions are
  byte-identical to the pre-feature shape (tag only, or empty).
- `TestBuildProfileItems_SingleServerContextOnly`: one enabled server → description
  is the context cell alone, no tag.
- ★ interplay: at least one of the above includes a favourite Profile and asserts ★
  is still the rightmost, aligned column.

**Acceptance:**
```
go test ./internal/launcher/ -run 'TestBuildProfileItems' -v
go test ./internal/launcher/ -run TestFormatProfileParams_OllamaShowsNoParams -v
make check
```

**Commit:** `feat(menu): show configured context size in the selection menu`

## 3. Context column in the non-TTY fallback (`buildSimpleProfileLines`)

Depends on item 2.

**What:** In `internal/launcher/menu.go`, mirror the identical column rules
(D1/D5/D6) in `buildSimpleProfileLines` (currently ~L513–554), which renders the
numbered plain-text selection list when stdin is not a TTY. Factor the per-Profile
cell computation shared with item 2 into a common helper rather than duplicating the
gate/format/width logic.

**Tests:** In `internal/launcher/menu_test.go`,
`TestBuildSimpleProfileLines_ContextColumn` (first direct coverage of this function):
mixed-backend config as in item 2; assert cell values, right-alignment, blank Ollama
cell, tag alignment, and ★ suffix placement.

**Acceptance:**
```
go test ./internal/launcher/ -run TestBuildSimpleProfileLines -v
make check
```

**Commit:** `feat(menu): mirror the context column in the non-TTY profile list`

## 4. Context column in `llama-launcher list` (`cmdList`)

Depends on item 2.

**What:** In `internal/launcher/cli.go`, extend `cmdList` (currently ~L468–526) to
insert the same right-aligned context column between the Profile title column and the
`[server]` tag, reusing the shared cell helper from items 2–3. Keep `cmdList`'s
existing conventions: `visibleWidth`-based padding and the `-` placeholder for an
empty description column. `cmdListJSON` is out of scope (already emits
`context_size`).

**Tests:** In `internal/launcher/cli_test.go`,
`TestCmdList_ContextColumnAlignment`, patterned on
`TestCmdList_StarColumnAlignmentMultibyte` (~L463) with `captureStdout`: a
multibyte-titled Profile plus ASCII ones across two backends; assert the context
cells and `[` columns align by visible width and the Ollama row's cell is blank.

**Acceptance:**
```
go test ./internal/launcher/ -run 'TestCmdList_(ContextColumnAlignment|StarColumnAlignmentMultibyte)' -v
make check
```

**Commit:** `feat(cli): show configured context size in list output`

## 5. Documentation: TDD, README, CHANGELOG

Depends on items 1–4. This item owns every doc amendment of the plan.

**What:**

- `llama-launcher.TDD.md`:
  - §3.1: add the context column to the interactive-menu ASCII mock-ups, and extend
    the paragraph that today defines the `[server]` tag column (~L68) with the
    column's contract: shown when at least one Profile has an effective (merged)
    `context_size` that its LLM Server actually receives (ParamSpecs-gated — Ollama
    rows stay blank); right-aligned; blank cell otherwise; formatted per D4.
  - §4.7 (~L383–387): add the context column to the sentence naming
    `buildProfileItems`/`buildSimpleProfileLines`/`cmdList` as one every-surface
    contract.
  - §5.2: update the responsibility prose for the `menu.go` and `cli.go` rows and the
    test enumeration in the `menu_test.go` / `cli_test.go` rows.
  - §12.3 menu-helper-test table (~L848–856): add the tests introduced by items 1–4.
- `README.md`:
  - The interactive-mode section (~L438–454) and the merge/favourites paragraph
    (~L342–344): one or two sentences describing the column and its
    only-what-the-server-receives rule.
  - The `list` command line in the CLI section (~L456–473) if its wording enumerates
    columns.
  - Do **not** touch the embedded default-config block (the config file is
    unchanged).
- `CHANGELOG.md`: new `## 1.6.2` heading directly under `# Changelog`, with a
  `### Added` bullet in the house style (bold one-sentence summary, then narrative):
  the column, all three surfaces, the merged-value semantics, the
  ParamSpecs/Ollama-blank rule and its v1.4.6 lineage, the D4 format, and the D7
  known limitation (`extra_args` `-c` values are not parsed). No date, no brackets,
  no link refs — match the existing headings exactly.

**Tests:** none (prose only).

**Acceptance:**
```
grep -n '## 1.6.2' CHANGELOG.md
grep -n 'context' llama-launcher.TDD.md | grep -in 'column'
grep -in 'context size' README.md
make check
```

**Commit:** `docs: document the context-size column in TDD, README, and CHANGELOG`

## 6. Version bump to 1.6.2

Depends on item 5. Authorized by Decision D3 (explicit user request, 2026-08-02).

**What:** Change the `VERSION` file content from `1.6.1` to `1.6.2` (single line).
Nothing else — the Makefile injects it via ldflags, and the CHANGELOG heading was
written in item 5.

**Tests:** none.

**Acceptance:**
```
grep -qx '1.6.2' VERSION && echo VERSION-OK
make build
make check
```

**Commit:** `chore: bump version to 1.6.2`

## Closing notes

- Release (tag, GitHub release, Homebrew tap) is **not** part of this plan — the user
  runs the brew-release flow separately after execution.
- Manual follow-up for the user: the README screenshots `media/screen_1.png` /
  `media/screen_2.png` predate the column and will be stale.
