---
title: The interactive menu ignores the mouse
status: open
type: feature
priority: 3
labels: [menu]
plan: 2026-11-17 - 01 - vllm-mouse-autostart-plan
created: 2026-11-06T18:09:37Z
---

The interactive menu (TDD §3.1) is keyboard-only: `selectMenu`, the pickers and the popups in `internal/launcher/ui.go` read keys through `readKey` and never enable terminal mouse reporting, so a click or a wheel notch does nothing. apogee (`internal/tui/panes.go`, `reportpane.go`) and capcom (`internal/ui/mouse.go`, `listcursor.go`) both take the mouse: a left click selects a row, a wheel notch moves the cursor or scrolls.

The menu is hand-rolled on `golang.org/x/term` raw mode, not bubbletea, so this means enabling SGR mouse reporting (`\x1b[?1000h` + `\x1b[?1006h`) while raw mode is on, decoding the `\x1b[<b;x;yM/m` sequences in `readKey`, and mapping a click to the row it lands on. Moving to bubbletea v2 like apogee and capcom is the alternative; that choice belongs in the plan.

## Why

The sibling TUIs (apogee, capcom) take the mouse, so reaching for it in llama-launcher and getting nothing feels broken.

## Acceptance

- In every menu variant and instance/Profile picker, a left click on a selectable row runs it as Enter would; a click on a separator, the header or outside the list does nothing.
- A wheel notch moves the selection one selectable row, as Up/Down do.
- In a confirm popup a click inside the box confirms as Enter does; in an any-key popup a click dismisses it.
- Mouse reporting is switched off whenever raw mode ends — on exit, Ctrl+C, before "Edit config" hands the terminal to an editor, and on the numbered fallback — so no escape sequences leak into the shell.
- Keyboard behaviour is unchanged; unit tests cover decoding the SGR mouse sequences and mapping a click row to a menu item.
- TDD §3.1, README and CHANGELOG describe mouse use.
