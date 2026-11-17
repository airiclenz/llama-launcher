# Menu hover highlight

**Goal:** Show the click target before the click. In the interactive menu and its pickers the selection highlight follows the pointer; the Switch model confirmation gets `[ Switch ]` / `[ Cancel ]` buttons that highlight under the pointer and replace click-anywhere-inside-confirms.
**Date:** 2026-11-17
**Status:** unexecuted
**sized for:** ~200k-context host
**base:** 4f20e0b
**Closes:** menu-hover-not-shown; confirm-popup-click-target

**Sources:**
- `.capcom/issues/menu-hover-not-shown.md`, `.capcom/issues/confirm-popup-click-target.md`
- `llama-launcher.TDD.md` §3.1, the "Switch model" paragraph, the `ui.go` / `ui_input.go` rows of the file table
- `docs/plans/archived/2026-11-17 - 01 - vllm-mouse-autostart-plan.md` items 6-8 (the mouse support this builds on)
- capcom Notes `adr-0012`, `glossary`

**Ratified design calls** (user, 2026-11-17):
- **Menu hover:** hover moves the selection; one highlight (`▶`, bold cyan), no separate hover style.
- **Confirm popup:** real `[ Switch ]` / `[ Cancel ]` buttons; only a click on a button acts; click-anywhere-inside-confirms is removed.
- **Mechanism** (carried from the archived mouse plan): hand-rolled SGR reporting on `x/term` raw mode; hover adds any-motion tracking (mode 1003); no bubbletea.

**Writer decisions under those calls** (2026-11-17):
- **Button look:** one centred row `[ Switch ]  [ Cancel ]` between the popup lines and the footer; the hovered button in `cBoldCyan`, the other plain; footer text stays `enter switch · q cancel`.
- **Buttons have no keyboard focus:** Enter switches, `q` / Esc / Ctrl+C cancel, as today.
- **Information popups** keep click-and-wheel reporting only (no motion tracking, no highlight): the whole screen is their click target.

**Regression check (2026-11-17, 4f20e0b):**
- 1: guard folded — the existing `TestDecodeInput` "motion" row is replaced, not kept; the stale-comment list becomes a grep rule.
- 2: guard folded — the on-switch goes behind a `menuRawEnter` writer seam with a test that bites; writer decision: the no-repaint and refresh-deadline clauses stay in the Goal, in a pure function (`menuStep`, `remainingWait`) with table tests the Acceptance command runs.

**Standing requirements:**
- skills: coding-standards
- Use glossary terms (LLM Server, Profile, Model) in code, docs and messages.
- Each item's user-visible change gets a CHANGELOG entry via its sidecar; item 3's entry also replaces the unreleased wording "a left click inside the Switch model confirmation confirms it".
- An authorized deviation from item text lands as a dated NOTES line under the item.
- `make cross` must stay green: no new platform primitive outside `ui_poll_*.go`.

**Out of scope:**
- Hover in information popups, the still-loading popup and the numbered fallback.
- Changing the terminal's pointer shape; drag selection; scrolling long lists; keyboard focus on popup buttons.
- Windows (the menu is refused there).

## 1. Decode pointer motion

**What:**
**Goal:** `decodeInput` decodes an SGR mouse report whose button value has the motion bit (32) set and whose final byte is `M` to `inputEvent{key: keyHover, x, y}` with the report's 1-based column and row, whether or not a button is held; every report without the motion bit decodes as it did. `ui.go` carries `escMouseTrackOn` (modes 1000, 1003, 1006 on) and `escMouseOff` switches 1003 off as well as 1006 and 1000. `awaitDismiss` reads past `keyHover` exactly as it reads past `keyMouseIgnored`.
**Approach (assumed at the header base):** Extend `decodeSGRMouse` in `ui_input.go`: test the motion bit before the button `switch`, with a named constant (`sgrMotionBit = 32`) beside `sgrButtonLeft`. Add `keyHover keyCode = -11` to the mouse block in `ui.go` and update the doc comments of `inputEvent`, `decodeInput`, `decodeSGRMouse` and the key block (x,y are set for `keyClick` and `keyHover`). `escMouseTrackOn = "\033[?1000h\033[?1003h\033[?1006h"`; `escMouseOff = "\033[?1006l\033[?1003l\033[?1000l"` — one off-switch for both on-switches, since switching off a mode that is not on is harmless. `escMouseOn` stays as it is and nothing enables `escMouseTrackOn` yet. `awaitDismiss` must skip `keyHover` because a motion report queued in `pendingInput` while the menu tracked motion would otherwise close the popup the click opened. No new file, no new module.
**Regression guard.** `TestDecodeInput`'s existing "motion" row pins `\x1b[<35;5;7M` → `keyMouseIgnored` (`ui_test.go:331`): replace that row (→ `keyHover` x:5 y:7, consumed 10), do not only add cases. The comment list in the Approach is not closed: update every comment that lists the mouse events decoded or says motion is ignored — `awaitDismiss`'s doc and the `TestAwaitDismiss` / `TestDecodeInput` headers included — found with `grep -n -i 'motion\|keyMouseIgnored\|mouse report' internal/launcher/ui.go internal/launcher/ui_input.go internal/launcher/ui_test.go`.
**Files:** internal/launcher/ui_input.go, internal/launcher/ui.go, internal/launcher/ui_test.go
**Read first:** internal/launcher/ui_input.go — decodeSGRMouse, decodeInput, inputEvent, sgrButtonLeft; internal/launcher/ui.go — keyMouseIgnored key block, escMouseOff, awaitDismiss; internal/launcher/ui_test.go — TestDecodeInput
**Tests:** `TestDecodeInput`'s existing "motion" row is replaced: `\x1b[<35;5;7M` → `keyHover` at 5,7, consumed 10. It gains cases: `\x1b[<35;12;7M` → `keyHover` at 12,7; `\x1b[<32;3;4M` (left held) → `keyHover` at 3,4; `\x1b[<35;12;7m` → `keyMouseIgnored`; `\x1b[<0;5;6M` still `keyClick`; `\x1b[<64;1;1M` / `\x1b[<65;1;1M` still the wheel. `TestAwaitDismiss` gains "hover does not close": `keyHover, keyHover, keyClick` → 3 reads. `TestMenuRawRestore` and `assertMouseOffBeforeCursorShow` hold with the longer `escMouseOff`.
**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher/ -run 'DecodeInput|AwaitDismiss|MenuRawRestore|DrawPopup|ConfirmPopup'`
**Commit:** `feat(menu): decode pointer motion reports`

## 2. Selection follows the pointer in the menu

Depends on item 1.

**What:**
**Goal:** While `selectMenu` holds raw mode the terminal reports pointer motion (`escMouseTrackOn`), and a `keyHover` over a selectable row makes that row the selection; over a separator, the title, the header, the footer or outside the frame the selection is unchanged. A pure function `hoverSelection(geometry, items, selected, x, y) int` returns the new selection and is the only place that rule lives. An event that changes nothing visible — `keyHover` leaving the selection unchanged, `keyMouseIgnored` — causes no repaint, and the header callback still runs at least once per `refresh` interval under continuous motion. Click, wheel, arrows, digits, Enter and quit behave as before; every return path still writes `escMouseOff`.
**Approach (assumed at the header base):** In `ui.go`, `selectMenu` writes `escMouseTrackOn` in place of `escMouseOn`; `menuRawRestore` is unchanged (item 1 widened `escMouseOff`). `hoverSelection` wraps `menuItemAt` — same hit-test as a click, so the highlighted row is always the row a click returns. In the event loop, split "draw" from "wait": after an event that changes nothing visible, skip `headerFn` and the repaint and wait again, but keep a deadline (`time.Now().Add(refresh)`, reset on each repaint) and pass the remaining time to `readInputTimeout`, so a stream of motion reports cannot postpone the refresh tick or the stale check. Geometry used for a hover is the one from the last repaint. Update `selectMenu`'s doc comment. Extends `ui.go`; no new file.
**Regression guard.** `selectMenu` returns -1 before writing anything without a terminal (`ui.go:90-92`), so no test reaches its on-switch: put the on-switch behind a writer seam beside `menuRawRestore` (e.g. `menuRawEnter(w io.Writer)`, called by `selectMenu`) and assert its output equals `escMouseTrackOn` — red before the item, green after. The Goal clauses "no repaint for an event that changes nothing visible" and "header callback still runs at least once per `refresh` interval under continuous motion" stay in the Goal and are tested (writer decision, 2026-11-17): the per-event decision lives in a pure function `menuStep` (event, geometry, items, selected → selected, repaint, result) and the deadline in a remaining-time helper `remainingWait`, each with a table test run by the Acceptance command.
**Files:** internal/launcher/ui.go, internal/launcher/ui_test.go
**Read first:** internal/launcher/ui.go — selectMenu, menuItemAt, newMenuGeometry, moveSelection, menuRawRestore, readInputTimeout call; internal/launcher/ui_test.go — TestMenuItemAt, TestMenuRawRestore
**Tests:** `TestHoverSelection` (table, built like `TestMenuItemAt` on the same geometry/items fixture): pointer on a selectable row → that index; on the already selected row → unchanged; on a separator, above the body (title/header), below it (footer), left and right of the frame → `selected` unchanged; first and last frame column of a row count as on the row. `TestMenuRawEnter`: the on-switch seam's output (`menuRawEnter` to a buffer) equals `escMouseTrackOn`, red on the pre-item tree; `TestMenuRawRestore` keeps pinning that `menuRawRestore`'s output switches 1003 off before `escCursorShow`. `TestMenuStep` (table): unchanged-selection `keyHover` and `keyMouseIgnored` → no repaint; hover onto another row, wheel, arrows → repaint; click, Enter, quit → result. `TestRemainingWait` (table): time left before the deadline, zero or negative at and past it.
**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher/ -run 'HoverSelection|MenuItemAt|SelectMenu|MenuRawRestore|MenuRawEnter|MenuStep|RemainingWait'`
**Commit:** `feat(menu): move the selection to the row under the pointer`

## 3. Switch model confirmation buttons

Depends on item 1.

**What:**
**Goal:** The Switch model confirmation popup draws one centred row `[ Switch ]  [ Cancel ]` between its lines and its footer `enter switch · q cancel`. `drawConfirmPopup` enables `escMouseTrackOn`; the button under the pointer is drawn in `cBoldCyan` and neither is when the pointer is elsewhere; a left click on `[ Switch ]` returns true, on `[ Cancel ]` returns false, and anywhere else — inside or outside the box — changes nothing. Enter returns true; `q`, Esc, Ctrl+C, an unreadable key and a wait failure return false; wheel and ignored reports change nothing. Every return path clears the popup and writes `escMouseOff` before `escCursorShow`. The hit rectangles are derived from the same draw that put the buttons on screen.
**Approach (assumed at the header base):** In `ui.go`, give the confirm popup its own draw that builds the body (`lines`, blank, button row, blank, dimmed footer), renders through `Frame` as `drawPopup` does, and returns the box plus one `screenRect` per button; share the placement code with `drawPopup` (one helper computing start row/col from `lastMenuRect` or the terminal) rather than copying it. A button's rect is its label including the brackets, one row high. `drawConfirmPopup` keeps a `hovered` state (none / switch / cancel), repaints the popup in place only when it changes, and decides clicks with `screenRect.contains`. `drawPopup`, `drawPopupAndWait`, `showPopup` and `confirmPopup`'s signature are unchanged; `doSwitch` in `menu.go` needs no edit. Update the doc comments of `drawConfirmPopup` and `confirmPopup`.
**Files:** internal/launcher/ui.go, internal/launcher/ui_test.go
**Read first:** internal/launcher/ui.go — drawConfirmPopup, drawPopup, screenRect, contains, confirmPopup, lastMenuRect; internal/launcher/ui_test.go — TestSwitchModel_ConfirmPopup, TestDrawPopupReturnsDrawnRect
**Tests:** Rewrite `TestSwitchModel_ConfirmPopup`: keyboard cases kept; click on the first and last cell of `[ Switch ]` → true; click on the first and last cell of `[ Cancel ]` → false; click one cell left/right of each button, on the box corner, on a text row inside the box, and outside the box → ignored (then `q`); hover over `[ Switch ]` then Enter → true and the output written after the hover contains `cBoldCyan` immediately before `[ Switch ]`; hover that changes nothing writes nothing; `escMouseTrackOn` is on at the first wait; `assertMouseOffBeforeCursorShow` on the tail. New `TestConfirmPopupButtonRects`: render to a buffer, strip escapes, and assert each returned rect's row/col/width matches where `[ Switch ]` / `[ Cancel ]` appear in the drawn output. `TestDrawPopupReturnsDrawnRect` stays green.
**Acceptance:**
- `go build ./...`
- `go test ./internal/launcher/ -run 'ConfirmPopup|DrawPopup|AwaitDismiss|SwitchModel'`
**Commit:** `feat(menu): confirm a model switch with hover-highlighted buttons`

## 4. Document hover and the confirmation buttons

Depends on items 2 and 3.

**What:**
**Goal:** `llama-launcher.TDD.md` and `README.md` describe the behaviour items 1-3 landed and no sentence in either still says a click inside the Switch model confirmation confirms it, or that the menu reports clicks and wheel only.
**Approach (assumed at the header base):** TDD: the feature bullet ("click selects, wheel moves"), §3.1's mouse sentences (hover moves the selection; motion reporting on only while the menu or the confirmation holds raw mode; information popups take click and wheel only), the "Switch model" paragraph (buttons, click on a button, click elsewhere does nothing), and the `ui.go` / `ui_input.go` file-table rows (`keyHover`, `escMouseTrackOn`, `hoverSelection`, the confirm popup's button rects, `awaitDismiss` skipping hover). README: the Switch model sentence and the "menu takes the mouse" paragraph. Rule for stale prose: every sentence naming a click inside the confirmation popup or listing the mouse events the menu takes — find with `grep -n -i 'click\|wheel\|mouse' README.md llama-launcher.TDD.md`. Docs only; no code.
**Files:** llama-launcher.TDD.md, README.md
**Read first:** llama-launcher.TDD.md — feature bullet "click selects, wheel moves", §3.1 mouse sentences, "Switch model" paragraph, `ui.go` and `ui_input.go` file-table rows; README.md — "Switch model asks before loading" sentence, "The menu takes the mouse" paragraph
**Tests:** none (docs only).
**Acceptance:**
- `! grep -n -i 'click inside' README.md llama-launcher.TDD.md`
- `grep -n -i 'hover\|pointer' README.md llama-launcher.TDD.md`
- `grep -n 'Switch \]' README.md llama-launcher.TDD.md`
**Commit:** `docs(menu): describe hover selection and the confirmation buttons`
