---
title: The Switch model confirmation has no visible click target
status: done
type: feature
priority: 3
labels: [menu]
deps: [menu-hover-not-shown]
plan: 2026-11-17 - 02 - menu-hover-highlight-plan
created: 2026-11-17T10:25:41Z
closed: 2026-11-17T11:11:14Z
reason: Resolved by plan '2026-11-17 - 02 - menu-hover-highlight-plan' (archived in 016882b)
---

The Switch model confirmation popup (`drawConfirmPopup` in `internal/launcher/ui.go`, TDD §5 "Switch model") confirms on a left click anywhere inside its box. Nothing in the popup looks clickable, nothing reacts to the pointer, and there is no way to cancel with the mouse.

Wanted: the popup shows two buttons, `[ Switch ]` and `[ Cancel ]`. The button under the pointer is highlighted; a left click on `[ Switch ]` confirms, a left click on `[ Cancel ]` cancels, and a click anywhere else does nothing. This replaces click-anywhere-inside-confirms. Enter still switches and `q` / Esc still cancel.

Information popups ("Show model config", errors) keep closing on any key, click or wheel notch: the whole screen is their click target, so they get no hover highlight.

## Why

A click target that cannot be seen is a guess, and a confirmation that fires on a click anywhere in a large box is easy to trigger by accident.

## Acceptance

- The confirmation popup draws `[ Switch ]` and `[ Cancel ]`; the one under the pointer is highlighted and neither is when the pointer is elsewhere.
- A left click on `[ Switch ]` confirms; on `[ Cancel ]` cancels; anywhere else, inside or outside the box, does nothing.
- Enter confirms and `q`, Esc, Ctrl+C cancel as before; the numbered fallback is unchanged.
- Mouse reporting, motion included, is off before the terminal is restored on every return path.
- Unit tests cover the button hit-test against the drawn popup and the click and hover outcomes; TDD, README and CHANGELOG describe the buttons and no longer say a click inside the popup confirms.
