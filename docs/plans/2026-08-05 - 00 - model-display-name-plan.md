# Plan — Stop reporting the running Model as a full filesystem path

- **Goal:** every terminal surface that names the running Model shows a readable file name (`qwen3.6-35B-A3B-Q4_K_M.gguf`) instead of the absolute path llama-server reports (`/Users/airic/LL-Models/Qwen/qwen3.6-35B-A3B-Q4_K_M.gguf`). Machine-readable output and every matching path keep the raw server-reported id.
- **Date:** 2026-08-05
- **Status:** ready to execute
- **Skills:** `coding-standards`

## Authoritative sources

- `llama-launcher.TDD.md` §7.1–§7.2 (`RunningInstance`, `DiscoverRunningInstances`, `matchProfileName`), §3.1 (interactive mode), §3.2 (subcommand table, `status --json` field list).
- `docs/adr/0007-profile-activation-idempotency.md` — the idempotency check that shares `modelNamesMatch` with discovery.
- Observed ground truth, llama.cpp b10176 on 2026-08-05 (`curl http://127.0.0.1:1111/v1/models`): the `id` **and** every entry of `aliases` is the absolute `--model` path the server was launched with. Both the TDD §7.2 prose and the code comments at `internal/launcher/discovery.go` (`modelNamesMatch`) and `internal/launcher/backend_llamacpp.go` (`ListRunningModels`) currently claim llama-server "defaults the id to the model file's basename". That claim is stale and item 3 corrects it.

## Diagnosis (why the path reaches the screen)

Two independent causes stack:

1. **`RunningInstance.ActiveModel` is the raw server id, and every display site prints it verbatim.** `probeInstance` (`discovery.go:141-150`) stores `models[0].Name` after `sanitizeServerString`, which strips control characters only. For llamacpp that value is an absolute path.
2. **The profile-title path is unavailable on this config, so the raw-model fallback is what actually renders.** `matchProfileName` returns `""` on a tie, and the live config has two profiles — `Qwen3.6-35B-A3B-Q4_K_M.gguf` and `Qwen3.6-35B-A3B-Q4_K_M-MTP` — resolving to the same model file at the same `0.0.0.0:1111`. Both are exact matches, so no profile is named and every surface falls through to `ActiveModel`.

**This plan addresses cause 1 only** (ratified below). Cause 2 is left as-is: with cause 1 fixed, the tie renders as `qwen3.6-35B-A3B-Q4_K_M.gguf`, which is readable and honest about what the server reports.

## Ratified design calls

- **Shorten the label; do not attempt to disambiguate tied profiles.** Decided by the user (Airic Lenz), 2026-08-05, choosing "Shorten the label only" over "also break the profile-match tie via `/props`" and "also pass `--alias` to llama-server". Consequences: discovery adds no HTTP probe (TDD §7.2's "`/props` is not probed during discovery" stands), and the OpenAI model id clients see in `/v1/models` is unchanged.
- **The rendered label keeps the `.gguf` extension.** The user-approved target output is `qwen3.6-35B-A3B-Q4_K_M.gguf`, not `qwen3.6-35B-A3B-Q4_K_M`. This also matches how profiles are keyed in the user's own config.
- **Only path-shaped ids are shortened.** LM Studio reports ids like `qwen/qwen3-8b` and Ollama like `llama3:8b`; those are names, not paths, and `filepath.Base` would silently drop the publisher prefix. The helper therefore shortens only when the id is absolute or its base name ends in `.gguf`.
- **`ActiveModel` stays raw.** Shortening happens at render time, never at ingest, so `modelNamesMatch`, `matchProfileName`, `instancesSignature` and the ADR-0007 idempotency check keep comparing the value the server actually reported.

## Out of scope

- `cmdStatusJSON` (`internal/launcher/cli.go`) — `active_model` is a documented machine contract (TDD §3.2, and the MCP tool description in `cmd/llama-launcher-mcp/main.go:99`). It keeps emitting the raw id.
- `discovery.go` ingest: `sanitizeServerString(models[0].Name)` is unchanged, as are `modelNamesMatch`, `matchProfileName` and `instancesSignature`.
- Backend argument assembly — no `--alias`, no change to `BuildServerArgs`.
- The `matchProfileName` tie-break (cause 2 above).
- `VERSION` and any release tag.

## Standing requirements

- One conventional commit per item; the item's own commit message is given below.
- Any authorized deviation from an item's text lands as a dated `NOTES:` line under that item in this file.
- `make check` (`go test ./...` + the three-GOOS cross gate) must pass before an item is considered done. No item may start or stop a server: a llama-server instance is running on `0.0.0.0:1111` and must be left alone.

---

## 1. Add `modelDisplayName` and route every `menu.go` Model surface through it — ✅ DONE (2026-08-05)

**What**

Add to `internal/launcher/menu.go`, directly below `profileDisplayName` (currently line 485), a render-time helper:

```go
// modelDisplayName renders a server-reported model id for the terminal.
// llama-server reports the id it was launched with, and the launcher launches
// it with an absolute --model path, so the raw id is a filesystem path that
// nobody wants in a status row. Path-shaped ids collapse to their base name;
// every other id is left verbatim, because for the other backends the id is a
// name rather than a path — LM Studio's "qwen/qwen3-8b" and Ollama's
// "llama3:8b" would lose meaning if their separators were cut. The stored
// RunningInstance.ActiveModel keeps the raw value: matching (modelNamesMatch,
// the ADR-0007 idempotency check) and `status --json` compare and report what
// the server actually said.
func modelDisplayName(id string) string
```

Binding behaviour:

- `""` → `""`.
- `filepath.IsAbs(id)` is true, **or** `strings.HasSuffix(strings.ToLower(filepath.Base(id)), ".gguf")` → return `filepath.Base(id)`.
- otherwise → return `id` unchanged.

Use `path/filepath` (not `path`) so the Windows build of ADR-0012 splits on `\` as well.

Then replace the raw `inst.ActiveModel` / `target.ActiveModel` reads at these five `menu.go` display sites with `modelDisplayName(...)` of the same value (line numbers are current-tree references; match on the surrounding code, not the number):

| Line | Function | Current expression |
|---|---|---|
| 393 | `doUnloadModel` — picker item label | `label = inst.ActiveModel` |
| 410 | `doUnloadModel` — progress title | `displayName = target.ActiveModel` |
| 435 | `doShowConfig` — "Active model" pop-up | `fmt.Sprintf("Model:   %s", inst.ActiveModel)` |
| 787 | `serverStatusLines` — instance detail | `detail += " · " + inst.ActiveModel` |
| 836 | `runLoadedMenuSimple` — `Model:` line | `profileLabel = inst.ActiveModel` |

Do not touch the `inst.ActiveModel != ""` presence checks at lines 43, 87 and 374, or the `case inst.ActiveModel != ""` guard at 786 — emptiness is preserved by the helper and those are control flow, not display.

**Tests** (`internal/launcher/menu_test.go`)

- `TestModelDisplayName` — table-driven over the helper: absolute `.gguf` path → base name; relative `.gguf` path (`Qwen/x.gguf`) → base name; LM Studio-style `qwen/qwen3-8b` → verbatim; Ollama-style `llama3:8b` → verbatim; `""` → `""`; an upper-case `.GGUF` suffix → base name.
- `TestServerStatusLines_ShortensModelPath` — build a `[]*RunningInstance` with `ActiveModel` set to an absolute `.gguf` path and no `ActiveProfile`, call `serverStatusLines` (the existing `TestServerStatusLines_StartingInstance` at line 649 is the shape to copy), and assert the rendered line contains the base name and does **not** contain the directory portion.

**Acceptance**

```
go test ./internal/launcher/ -run 'TestModelDisplayName|TestServerStatusLines' -v
make check
```

**Commit:** `fix(ui): show the running model as a file name, not a path`

---

## 2. Route the `cli.go` Model surfaces through `modelDisplayName` — ✅ DONE (2026-08-05)

Depends on item 1.

**What**

In `internal/launcher/cli.go`, wrap the three human-facing reads (line numbers are current-tree references):

| Line | Function | Current expression |
|---|---|---|
| 212 | `unloadTargetLabel` — ambiguous-unload listing | `fmt.Sprintf("%s (%s)", inst.ActiveModel, profileLabel)` |
| 408 | `cmdStatus` — model column of a status row | `modelStr := inst.ActiveModel` |
| 463 | `statusDetailsLead` — `Active:` fallback when no profile matched | `profileLabel = inst.ActiveModel` |

Leave `cmdStatusJSON` (line ~588) emitting `inst.ActiveModel` raw — see "Out of scope". Leave the `inst.ActiveModel == ""` / `!= ""` guards at lines 160, 168 and 419 alone.

**Tests** (`internal/launcher/cli_test.go`)

- `TestCmdStatus_ShortensModelPath` — using the existing `captureStdout` helper (line 24) and the httptest stand-in pattern from `TestCmdStatus_RendersStartingInstance` (line 288), serve a `/v1/models` response whose id is an absolute `.gguf` path, run `cmdStatus`, and assert the captured output contains the base name and not the directory portion — covering both the row and the `Active:` details line.
- `TestCmdStatusJSON_KeepsRawModelPath` — same stand-in, `cmdStatusJSON`, assert `active_model` still carries the full absolute path. This is the regression guard on the machine contract.
- `TestUnloadTargetLabel_ShortensModelPath` — direct call on the pure function.

**Acceptance**

```
go test ./internal/launcher/ -run 'TestCmdStatus|TestUnloadTargetLabel' -v
make check
```

**Commit:** `fix(cli): show the running model as a file name in status and unload output`

---

## 3. Correct the stale llama-server claim and document the display rule

Depends on items 1 and 2. This item owns **all** documentation and comment changes for this plan; no earlier item edits docs.

**What**

1. **`llama-launcher.TDD.md` §7.2** (the `matchProfileName` paragraph, currently line 714) — the sentence "llama-server defaults the id to the model file's basename" is contradicted by the observation recorded under "Authoritative sources". Rewrite the clause to say that a server reports the model as whatever path or alias it was launched with, and that current llama.cpp builds report the absolute `--model` path, which is precisely why the basename fallback exists. Keep the rest of the paragraph (exact-match-wins, ambiguity yields no match) intact.
2. **`llama-launcher.TDD.md` §3.1** — add the rendering rule next to the existing paragraph on Profile titles (currently line 696, "Each Profile row shows the Profile's optional `title`…"): where a Model name is shown rather than a Profile title — the status header, the "Active model" pop-up, the unload picker — a path-shaped id renders as its base name; ids that are names rather than paths (LM Studio's `qwen/qwen3-8b`, Ollama's `llama3:8b`) render verbatim.
3. **`llama-launcher.TDD.md` §3.2** — in the `status` row of the subcommand table (currently line 148), note that the human output renders the Model as a file name while `--json` keeps `active_model` as the raw server-reported id.
4. **`llama-launcher.TDD.md` §14 file table** — in the `menu.go` row (currently line 444), name `modelDisplayName` alongside the other row-composition helpers described there.
5. **Code comments** — apply the same correction to `internal/launcher/discovery.go` (the `modelNamesMatch` doc comment, currently lines 171-176) and `internal/launcher/backend_llamacpp.go` (the `ListRunningModels` doc comment, currently lines 92-94).
6. **`CHANGELOG.md`** — insert a new `## Unreleased` section above `## 1.6.2` with a `### Fixed` entry in this file's established prose style: what the user saw (an absolute path in the status row, the `Active:` line, the menu header, the unload picker and the "Active model" pop-up), why (llama-server reports the launch `--model` path as its `/v1/models` id, and every display site printed it verbatim), what changed (one render-time helper, applied at every human surface), and what deliberately did not (`status --json`, profile matching and the ADR-0007 idempotency check all keep the raw id). Mention that ids which are names rather than paths are untouched. Do **not** add or edit any version heading.

`README.md` needs no change: it lists `status` in the command table but carries no status-output sample.

**Tests**

Documentation only — no new tests. The build/test gate below is the regression check.

**Acceptance**

```
grep -n "defaults the id to the model file's basename" llama-launcher.TDD.md internal/launcher/discovery.go internal/launcher/backend_llamacpp.go   # must return nothing
grep -n "^## Unreleased" CHANGELOG.md                                                                                                              # must return one line
grep -n "modelDisplayName" llama-launcher.TDD.md                                                                                                   # must return at least one line
make check
```

**Commit:** `docs: record the model-name display rule and correct the llama-server id claim`

---

## Manual check after the run

Not an item — a one-liner for the user, safe against the live server:

```
./llama-launcher status
```

Expected, with the currently running instance:

```
  ● LLaMA.cpp  running    0.0.0.0:1111           qwen3.6-35B-A3B-Q4_K_M.gguf

Active: qwen3.6-35B-A3B-Q4_K_M.gguf · PID 15481 · Uptime … · Log …
```

## Suggested version bump

`VERSION` is `1.6.2` and `v1.6.2` is tagged, so this work lands unreleased. It is a user-visible output fix with no API or config change — **patch, 1.6.3** — but no item in this plan touches `VERSION` or adds a version heading to `CHANGELOG.md`. Whether and when to cut the release is yours.

## Known limitation this plan does not fix

`Qwen3.6-35B-A3B-Q4_K_M.gguf` and `Qwen3.6-35B-A3B-Q4_K_M-MTP` resolve to the same model file at the same address, so `matchProfileName` sees a tie and names no Profile — which is why the Model name renders at all instead of the Profile title "Qwen 3.6 35B-A3B Q4-K-M". After this plan those two profiles are still indistinguishable from a live probe alone. Breaking that tie needs either a `/props` probe during discovery (which TDD §7.2 currently rules out) or `--alias` on the llama.cpp launch (which changes the model id API clients see); both were declined for this plan on 2026-08-05.
