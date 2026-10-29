# Splash MLX install check — plan

**Goal:** A Splash Model that Splash installed and pinned as an MLX snapshot counts as installed, so `load` and `config validate` accept it. Old Splash-package snapshots keep resolving.
**Date:** 2026-10-29
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** d002e2a
**Sources:** `.capcom/issues/splash-mlx-install-check.md`; Note `adr-0014` (and children `adr-0014-decision`, `adr-0014-why`, `adr-0014-consequences`); `llama-launcher.TDD.md` Splash model resolution paragraph and test table.

**Ratified design calls** (issue `splash-mlx-install-check` acceptance, 2026-10-29):
- **Installed snapshot:** a `refs/splash/<installation>/<rev>` pin whose `snapshots/<rev>/` holds a regular `config.json` OR a regular `manifest.json`; the pin stays mandatory.

**Standing requirements:**
- skills: coding-standards
- Behaviour change updates TDD and ADR-0014 notes (item 2); CHANGELOG entry travels in the sidecar.

**Regression check (2026-10-29, d002e2a):**
- 1: guard folded (test helper cannot build the missing-snapshot / marker-as-directory cases; install-check closures pin a const ref)
- 2: recast (prose rule widened to every sentence presenting the old Splash package format as a current Splash Model; README.md and glossary Note `model.md` added)
- 2: guard folded (round 2: acceptance note — `capcom check` already exits 1 at base on AGENTS.md:12; passes when no problem is under `.capcom/knowledge`, AGENTS.md not refreshed)
- 2: guard folded (round 2, G: lines: Approach rule and grep widened to the guard's, grep covers `internal/launcher/defaults/config.yaml`; config.yaml:344 splash-qwen id replaced, README.md:7 project link kept; `adr-0014.md` Status "Accepted; amended 2026-10-29" with dated amendment marker; Acceptance grep line added)

**Out of scope:**
- Checking safetensors shards or weight completeness beyond the pin.
- Any change to the hub lookup (`splashHubDir`) or repo-id validation.
- Any version bump or release.

## 1. Accept MLX snapshots in the Splash install check — ✅ DONE (2026-10-29)

NOTES (2026-10-29): marker list lives in a package var `splashSnapshotMarkers` and the per-snapshot stat in a new helper `splashSnapshotHasMarker` (keeps `splashModelInstalled` within the nesting limit); `writeSplashSnapshot` now returns the snapshot dir so the missing-snapshot and marker-as-directory cases can break it; the existing "manifest.json without a refs/splash pin" subtest is kept alongside the new config.json one.

**Closes:** splash-mlx-install-check

**What:** Regression from Splash commit 539eae95 ("Stop loading Splash packages"): current Splash models are MLX snapshots (`config.json` + safetensors, no `manifest.json`), so every one is refused as not installed.

**Regression guard.** `writeSplashSnapshot` always `MkdirAll`s `snapshots/<rev>/` and writes the marker as a file, so say how the edge cases are built: the missing-snapshot case writes the pin, then `os.RemoveAll`s `snapshots/<rev>`; the directory case `os.Mkdir`s `snapshots/<rev>/config.json` (or give the helper a no-snapshot / marker-as-dir option). `resolves` and `refusesWithInstallHint` close over the const `ref`: make both take the ref as a parameter (or switch the const to the issue's id) so the MLX case can use `mlx-community/Qwen3.8-27B-4bit`.

**Goal:** `ResolveModel` on a Splash Profile resolves a Model whose pinned snapshot holds `config.json` or `manifest.json` (each a regular file), and refuses with the `splash serve --model <id>` hint when there is no `refs/splash` pin, or the pinned snapshot is missing or holds neither file.

**Approach (assumed at the header base):** in `internal/launcher/backend_splash.go`, `splashModelInstalled` stats `snapshots/<rev>/manifest.json`; make it accept either marker file (loop over `config.json`, `manifest.json`). Update its doc comment: pin written only after a verified download; marker is `config.json` for MLX snapshots, `manifest.json` for the old Splash package format. In `backend_splash_test.go`, generalise `writeSplashSnapshot` to take the marker filename (`""` = none) instead of `withManifest`; `installSplashModel` writes an MLX snapshot (`config.json`). Error wording unchanged.

**Files:** `internal/launcher/backend_splash.go`; `internal/launcher/backend_splash_test.go`
**Read first:** internal/launcher/backend_splash.go — splashModelInstalled, Splash.ResolveModel; internal/launcher/backend_splash_test.go — TestSplashResolveModelInstallCheck, writeSplashSnapshot, installSplashModel, TestSplashResolveModel, setSplashHubEnv, splashTestRev

**Tests:** in `TestSplashResolveModelInstallCheck`: "MLX snapshot with config.json" resolves; "legacy snapshot with manifest.json" resolves; "pinned snapshot with neither file" refused (rename the existing "snapshot without manifest.json" subtest to this); "pin whose snapshot directory is missing" refused; "config.json without a refs/splash pin" refused; existing "model absent" case stays. A `config.json` that is a directory is refused. Use the issue's id `mlx-community/Qwen3.8-27B-4bit` in the MLX case.

**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher -run 'TestSplash' -count=1`
- `go vet ./internal/launcher`

**Commit:** `fix(splash): accept MLX snapshots in the install check`

## 2. Document the MLX marker in TDD and ADR-0014 — ✅ DONE (2026-10-29)

NOTES (2026-10-29): the dated amendment marker sits on the changed decision and why bullets; the new consequences line carries the history itself, so it has no marker. adr-0014.md also says those two Notes carry the amendment, the way adr-0015.md does. The TDD splash-qwen example keeps two spaces before its comment, and the next line's comment column is left as it is.
NOTES (2026-10-29): `capcom check` exits 1 only on AGENTS.md:12 (the stale capcom block, already failing at base) and reports nothing under .capcom/knowledge.

**What:** Recast at the regression check (2026-10-29).

**Regression guard.** widen the prose rule to every doc sentence that presents the old Splash package format as a current Splash Model (not only those naming manifest.json); fold in README.md's backend table example `incoai/Qwen3.8-27B-Splash` and the commented `splash-qwen` profile in `internal/launcher/defaults/config.yaml` (:344), replacing each id with `mlx-community/Qwen3.8-27B-4bit` (keep README.md:7's `github.com/incoai/splash` project link), the glossary Note `.capcom/knowledge/glossary/glossary-language/model.md` "installed Splash package" wording, and the same phrase in `adr-0014-why`; set `.capcom/knowledge/decisions/adr-0014.md` Status to "Accepted; amended 2026-10-29" and mark the changed decision/why bullets or the new consequences line with "*(Amended 2026-10-29: installed originally required manifest.json.)*" (house convention: adr-0015.md:5, adr-0015-consequences.md:7, adr-0012.md:5); add README.md, model.md, defaults/config.yaml and adr-0014.md to item 2's Files line; grep: `grep -rn "manifest\|Splash package\|incoai" README.md llama-launcher.TDD.md .capcom/knowledge internal/launcher/defaults/config.yaml`. Acceptance note — `capcom check` already exits 1 at base on AGENTS.md:12 (stale capcom block); the item passes when no reported problem is under .capcom/knowledge, and the item does not refresh AGENTS.md.

**Goal:** Every doc that states what an installed Splash Model is names the pinned snapshot holding `config.json` (MLX) or `manifest.json` (old Splash package); no doc says `manifest.json` alone is the marker.

**Approach (assumed at the header base):** rule: every sentence outside `CHANGELOG.md`, `docs/plans/` and `.capcom/issues/` that presents the old Splash package format as a current Splash Model. Find with `grep -rn "manifest\|Splash package\|incoai" README.md llama-launcher.TDD.md .capcom/knowledge internal/launcher/defaults/config.yaml` (README.md:7's `github.com/incoai/splash` project link is a hit to keep). Known sites at base: TDD Splash model-resolution paragraph, the `TestSplashResolveModelInstallCheck` table row and the splash-qwen config example (TDD:376); README.md:110 backend table splash row; `internal/launcher/defaults/config.yaml:344` commented splash-qwen profile; glossary Note `model.md` Model line; `adr-0014.md` Status line; `adr-0014-decision` "Installed means" bullet; `adr-0014-why` "A manifest alone is not enough" bullet (reword to "a snapshot file alone": Splash pins only after a verified download, so a snapshot without a pin can be an interrupted download). Add to `adr-0014-consequences` a line that Splash dropped its package format (Splash commit 539eae95), so the check accepts the MLX `config.json` too. Edit Notes by hand, then `capcom check`.

**Files:** `llama-launcher.TDD.md`; `.capcom/knowledge/decisions/adr-0014/adr-0014-decision.md`; `.capcom/knowledge/decisions/adr-0014/adr-0014-why.md`; `.capcom/knowledge/decisions/adr-0014/adr-0014-consequences.md`; `README.md`; `.capcom/knowledge/glossary/glossary-language/model.md`; `internal/launcher/defaults/config.yaml`; `.capcom/knowledge/decisions/adr-0014.md`
**Read first:** llama-launcher.TDD.md — Splash model-resolution paragraph, TestSplashResolveModelInstallCheck table row, splash-qwen config example; .capcom/knowledge/decisions/adr-0014/adr-0014-decision.md — "Installed means" bullet; .capcom/knowledge/decisions/adr-0014/adr-0014-why.md — "A manifest alone is not enough", "The Hugging Face cache" bullets;
.capcom/knowledge/decisions/adr-0014.md — Status line; .capcom/knowledge/decisions/adr-0015/adr-0015-consequences.md — dated amendment marker; README.md — backend table splash row; internal/launcher/defaults/config.yaml — splash-qwen comment block;
.capcom/knowledge/glossary/glossary-language/model.md — Model

**Tests:** none (docs only).

**Acceptance:**
- `grep -rn "config.json" llama-launcher.TDD.md .capcom/knowledge/decisions/adr-0014` lists the TDD paragraph, the TDD test row and `adr-0014-decision`
- `grep -rn "manifest\|Splash package\|incoai" README.md llama-launcher.TDD.md .capcom/knowledge internal/launcher/defaults/config.yaml` shows no `incoai/Qwen` id and no "Splash package" outside a sentence naming it the old format, and every `manifest.json` line also names `config.json`
- `capcom check` reports no problem in `.capcom/knowledge`

**Depends on item 1.**

**Commit:** `docs(splash): name the MLX config.json install marker`
