---
title: The menu does not show which row a click would hit
status: open
type: feature
priority: 3
labels: [menu]
plan: 2026-11-17 - 02 - menu-hover-highlight-plan
created: 2026-11-17T10:25:41Z
---

The interactive menu and its pickers (`selectMenu` in `internal/launcher/ui.go`, TDD §3.1) take a left click on a row, but nothing on screen follows the pointer. Terminal.app keeps its I-beam cursor over the menu, and an I-beam does not make the targeted row obvious, so the user has to guess which row a click will land on. The menu enables click and wheel reporting only (`\x1b[?1000h` + `\x1b[?1006h`); pointer motion is never reported, and `decodeSGRMouse` in `ui_input.go` drops motion reports as `keyMouseIgnored`.

Wanted: the selection highlight (`▶`, bold cyan) follows the pointer. Hovering a selectable row selects it, exactly as moving there with the arrow keys would, so there is one highlight and Enter and a click always act on the same row. This needs any-motion reporting (`\x1b[?1003h`) while the menu holds raw mode, and decoding the SGR motion reports.

## Why

Clicking is guesswork under an I-beam cursor: the row the click hits is not shown before the click.

## Acceptance

- In every menu variant and picker, moving the pointer onto a selectable row moves the selection highlight to it; the pointer over a separator, the title, the header, the footer or outside the frame leaves the selection where it was.
- Arrow keys, wheel, digits, Enter and click behave as before; after hovering, Enter runs the hovered row.
- Pointer motion that does not change the selection does not repaint the menu, and continuous motion does not hold back the header refresh.
- Motion reporting is switched off wherever mouse reporting is switched off today; a motion report still queued when an information popup opens does not close it.
- A terminal that sends no motion reports behaves as it does today.
- Unit tests cover decoding SGR motion reports and mapping a pointer position to the selection; TDD §3.1, README and CHANGELOG describe hover.
