package launcher

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	escClear      = "\033[H\033[J"
	escCursorHide = "\033[?25l"
	escCursorShow = "\033[?25h"

	// escMouseOn turns on click and wheel reporting (1000) in SGR encoding
	// (1006); escMouseOff turns both off again, in reverse order.
	escMouseOn  = "\033[?1000h\033[?1006h"
	escMouseOff = "\033[?1006l\033[?1000l"

	cReset         = "\033[0m"
	cDim           = "\033[2m"
	cGreen         = "\033[32m"
	cRed           = "\033[31m"
	cBoldLightGray = "\033[1;37m"
	cBoldCyan      = "\033[1;36m"
)

type keyCode int

const (
	keyNone   keyCode = 0
	keyUp     keyCode = -1
	keyDown   keyCode = -2
	keyEnter  keyCode = -3
	keyEscape keyCode = -4
	keyCtrlC  keyCode = -5
	keyQ      keyCode = -6

	// The mouse events decodeInput reports: a left-button press (with its
	// position on the inputEvent), the two wheel directions, and every other
	// mouse report — release, motion, other buttons — which callers ignore.
	keyClick        keyCode = -7
	keyWheelUp      keyCode = -8
	keyWheelDown    keyCode = -9
	keyMouseIgnored keyCode = -10
)

type menuItem struct {
	Label       string
	Description string
	Separator   bool
}

var lastMenuRect struct {
	row, col, width, height int
}

// readKey returns the key of the next input event, blocking until one is
// complete; see readInput. For callers that need no mouse coordinates.
func readKey() keyCode {
	return readInput().key
}

// readKeyTimeout returns the key of the input event completed within timeout,
// or keyNone when the wait elapses first; see readInputTimeout. For callers
// that need no mouse coordinates.
func readKeyTimeout(timeout time.Duration) keyCode {
	return readInputTimeout(timeout).key
}

// idxMenuStale is returned by selectMenu when headerFn reports that the
// state the menu was built from no longer holds (e.g. a model was loaded or
// unloaded in the background); callers rebuild the menu instead of acting on
// a stale item list.
const idxMenuStale = -2

// selectMenu renders an interactive item picker and blocks until the user
// chooses an entry (returns its index), quits (returns -1), or headerFn
// reports the menu stale (returns idxMenuStale). headerFn runs once per
// refresh tick and on every input event; it returns the header lines plus a
// flag marking the menu stale. Mouse reporting is on while the menu holds raw
// mode: a left click on a selectable row chooses it as Enter would, a wheel
// notch moves the selection as Up/Down do, and every other mouse report is
// ignored. Both modes are undone on every return path, mouse first.
func selectMenu(title string, headerFn func() ([]string, bool), items []menuItem, hints string, centered bool, refresh time.Duration) int {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return -1
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return -1
	}
	defer func() {
		menuRawRestore(os.Stdout)
		term.Restore(fd, oldState)
	}()
	os.Stdout.WriteString(escMouseOn)

	selected := firstSelectable(items)
	if selected < 0 {
		return -1
	}

	frame := Frame{
		Title:   title,
		Footer:  []string{fmt.Sprintf("%s%s%s", cDim, hints, cReset)},
		RawMode: true,
	}
	for _, item := range items {
		if !item.Separator && strings.Contains(item.Description, "★") {
			frame.RightPadding = 1
			break
		}
	}

	var buf strings.Builder
	for {
		if headerFn != nil {
			header, stale := headerFn()
			if stale {
				return idxMenuStale
			}
			frame.Header = header
		}

		rendered := frame.Render(renderMenuRows(items, selected))
		renderedLines := strings.Split(strings.TrimSuffix(rendered, "\r\n"), "\r\n")

		frameWidth := visibleWidth(renderedLines[0])
		frameHeight := len(renderedLines)

		startRow := 2
		startCol := 1
		if centered {
			tw := terminalWidth()
			th := terminalHeight()
			startCol = (tw-frameWidth)/2 + 1
			startRow = (th-frameHeight)/2 + 1
			if startCol < 1 {
				startCol = 1
			}
			if startRow < 1 {
				startRow = 1
			}
		}

		lastMenuRect.row = startRow
		lastMenuRect.col = startCol
		lastMenuRect.width = frameWidth
		lastMenuRect.height = frameHeight
		geometry := newMenuGeometry(frame, startRow, startCol, frameWidth)

		buf.Reset()
		buf.WriteString(escClear)
		buf.WriteString(escCursorHide)

		for i, line := range renderedLines {
			fmt.Fprintf(&buf, "\033[%d;%dH%s", startRow+i, startCol, line)
		}

		os.Stdout.WriteString(buf.String())

		event := readInputTimeout(refresh)
		key := event.key
		switch key {
		case keyUp, keyWheelUp:
			selected = moveSelection(items, selected, -1)
		case keyDown, keyWheelDown:
			selected = moveSelection(items, selected, 1)
		case keyClick:
			if idx, ok := menuItemAt(geometry, items, event.x, event.y); ok {
				return idx
			}
		case keyEnter:
			return selected
		case keyQ, keyEscape, keyCtrlC:
			return -1
		default:
			if key >= keyCode('1') && key <= keyCode('9') {
				idx := int(key-keyCode('0')) - 1
				count := 0
				for i, item := range items {
					if item.Separator {
						continue
					}
					if count == idx {
						return i
					}
					count++
				}
			}
		}
	}
}

// menuRawRestore writes the escapes that undo selectMenu's screen modes to w:
// mouse reporting off first, then the cursor shown again. selectMenu calls it
// before restoring the terminal, so a button released after the menu returned
// is no longer reported and never lands as text in cooked mode.
func menuRawRestore(w io.Writer) {
	io.WriteString(w, escMouseOff+escCursorShow)
}

// moveSelection returns the index of the next selectable item from selected in
// direction step (-1 up, +1 down), skipping separators; selected itself when
// none lies that way. The arrow keys and the mouse wheel share it.
func moveSelection(items []menuItem, selected, step int) int {
	for next := selected + step; next >= 0 && next < len(items); next += step {
		if !items[next].Separator {
			return next
		}
	}
	return selected
}

// menuGeometry is where one selectMenu draw put the menu body on screen, in
// the 1-based row and column cells an SGR mouse report carries.
type menuGeometry struct {
	firstBodyRow int // screen row of items[0]
	left, right  int // first and last screen column of the frame, borders included
}

// newMenuGeometry derives the body's screen position from the frame drawn at
// startRow/startCol, frameWidth columns wide. It mirrors Frame.Render's
// layout: the top border, a title rule when the frame has a title, and the
// header lines plus their divider when it has a header. The header is the one
// the draw used — headerFn may change its length between refresh ticks.
func newMenuGeometry(frame Frame, startRow, startCol, frameWidth int) menuGeometry {
	bodyRow := startRow + 1
	if frame.Title != "" {
		bodyRow++
	}
	if len(frame.Header) > 0 {
		bodyRow += len(frame.Header) + 1
	}
	return menuGeometry{
		firstBodyRow: bodyRow,
		left:         startCol,
		right:        startCol + frameWidth - 1,
	}
}

// menuItemAt hit-tests a mouse position against the menu body: the index of
// the selectable item drawn at column x, row y, and true; or false for a
// separator, the title, the header, the footer, or anywhere outside the frame.
func menuItemAt(geometry menuGeometry, items []menuItem, x, y int) (int, bool) {
	if x < geometry.left || x > geometry.right {
		return -1, false
	}
	idx := y - geometry.firstBodyRow
	if idx < 0 || idx >= len(items) || items[idx].Separator {
		return -1, false
	}
	return idx, true
}

// menuLabelWidth returns the width of the menu's label column: the widest
// non-separator label plus the two-space gap that follows it. Labels are
// measured with visibleWidth rather than len, so a multibyte title costs the
// column the one place it occupies on screen instead of one per byte.
func menuLabelWidth(items []menuItem) int {
	width := 0
	for _, item := range items {
		if item.Separator {
			continue
		}
		if w := visibleWidth(item.Label); w > width {
			width = w
		}
	}
	return width + 2
}

// renderMenuRows formats the menu body: one line per item, separators as blank
// lines, the item at selected highlighted. Every label is padded to the common
// label-column width, so the description — the context-size cell, the [server]
// tag and the ★ marker — starts at the same column on every row.
func renderMenuRows(items []menuItem, selected int) []string {
	labelWidth := menuLabelWidth(items)
	body := make([]string, 0, len(items))
	for i, item := range items {
		if item.Separator {
			body = append(body, "")
			continue
		}
		if i == selected {
			body = append(body, fmt.Sprintf("%s▶ %-*s%s %s", cBoldCyan, labelWidth, item.Label, cReset, item.Description))
		} else {
			body = append(body, fmt.Sprintf("· %-*s %s%s%s", labelWidth, item.Label, cDim, item.Description, cReset))
		}
	}
	return body
}

func firstSelectable(items []menuItem) int {
	for i, item := range items {
		if !item.Separator {
			return i
		}
	}
	return -1
}

func isTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func showErrorPopup(err error) {
	if !isTerminal() {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}

	lines := strings.Split(err.Error(), "\n")

	maxWidth := terminalWidth() - 12
	if maxWidth < 40 {
		maxWidth = 40
	}

	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, wrapLine(line, maxWidth)...)
	}

	title := fmt.Sprintf("%sError%s", cRed, cReset)
	showPopup(title, wrapped)
}

func wrapLine(line string, maxWidth int) []string {
	if visibleWidth(line) <= maxWidth {
		return []string{line}
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}
	var result []string
	current := words[0]
	for _, word := range words[1:] {
		if visibleWidth(current)+1+visibleWidth(word) > maxWidth {
			result = append(result, current)
			current = word
		} else {
			current += " " + word
		}
	}
	return append(result, current)
}

func showPopup(title string, lines []string) {
	if !isTerminal() {
		fmt.Printf("  %s\n", title)
		for _, line := range lines {
			fmt.Printf("  %s\n", line)
		}
		return
	}

	// Raw mode holds for the whole popup, so the mouse-off escape its deferred
	// clear writes reaches the terminal before cooked mode returns. When raw
	// mode cannot be entered there is nothing to wait on and the popup is
	// skipped.
	_ = withRawStdin(func() {
		drawPopupAndWait(os.Stdout, title, lines, waitForAnyKey)
	})
}

// waitForAnyKey blocks until awaitDismiss sees a dismissing event on stdin,
// which must already be in raw mode. It never fails.
func waitForAnyKey() error {
	awaitDismiss(readInput)
	return nil
}

// awaitDismiss reads input events with read until one closes an any-key
// popup: every key, a click anywhere and a wheel notch close it; a mouse
// report the decoder ignores — a button release above all, which follows
// every click — never does.
func awaitDismiss(read func() inputEvent) {
	for {
		if read().key != keyMouseIgnored {
			return
		}
	}
}

// withRawStdin runs fn with stdin in raw mode and restores the previous
// terminal state once fn returns, so the escapes fn writes on its way out —
// mouse reporting off — reach the terminal while raw mode still holds and a
// late mouse report is never echoed as text. It fails without running fn
// when raw mode cannot be entered. Nested raw sections inside fn are safe:
// each restores to the raw state it found.
func withRawStdin(fn func()) error {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer term.Restore(fd, oldState)
	fn()
	return nil
}

// drawPopupAndWait renders the popup frame to w, hides the cursor, turns on
// mouse reporting and calls wait for the dismissing input. On every return
// path — including a wait failure — it clears the popup, turns mouse
// reporting off and then restores the cursor, so the process never exits with
// the cursor hidden or the mouse reported.
func drawPopupAndWait(w io.Writer, title string, lines []string, wait func() error) {
	defer fmt.Fprint(w, escClear+escMouseOff+escCursorShow)

	drawPopup(w, title, lines, "press any key to close")
	fmt.Fprint(w, escMouseOn)

	if err := wait(); err != nil {
		return // the wait failed: close the popup without waiting
	}
}

// confirmPopup shows a popup asking to confirm a model switch and reports
// whether it was confirmed. It holds stdin in raw mode for the whole popup so
// mouse reporting is off again before the terminal is restored. Outside a
// terminal, or when raw mode cannot be entered, it confirms nothing.
func confirmPopup(title string, lines []string) bool {
	if !isTerminal() {
		return false
	}
	confirmed := false
	err := withRawStdin(func() {
		confirmed = drawConfirmPopup(os.Stdout, title, lines, readInputRaw)
	})
	return err == nil && confirmed
}

// readInputRaw reads one input event with stdin in raw mode, restoring the
// previous terminal state afterwards. It fails, returning keyNone, when raw
// mode cannot be entered.
func readInputRaw() (inputEvent, error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return inputEvent{key: keyNone}, err
	}
	defer term.Restore(fd, oldState)
	return readInput(), nil
}

// drawConfirmPopup renders a popup with an "enter switch · q cancel" footer
// to w, turns on mouse reporting and reads input events with wait until one
// decides: enter, or a left click inside the popup box, confirms; q, esc,
// ctrl-c, an unreadable key and a wait failure (raw mode unavailable) all
// cancel; navigation and digit keys, a click outside the box, the wheel and
// ignored mouse reports change nothing. Like drawPopupAndWait it clears the
// popup, turns mouse reporting off and restores the cursor on every return
// path.
func drawConfirmPopup(w io.Writer, title string, lines []string, wait func() (inputEvent, error)) bool {
	defer fmt.Fprint(w, escClear+escMouseOff+escCursorShow)

	box := drawPopup(w, title, lines, "enter switch · q cancel")
	fmt.Fprint(w, escMouseOn)

	for {
		ev, err := wait()
		if err != nil {
			return false
		}
		switch ev.key {
		case keyEnter:
			return true
		case keyClick:
			if box.contains(ev.x, ev.y) {
				return true
			}
		case keyQ, keyEscape, keyCtrlC, keyNone:
			return false
		}
	}
}

// screenRect is a box on the terminal: its top-left cell as a 1-based row and
// column, and its size in cells.
type screenRect struct {
	row, col, width, height int
}

// contains reports whether the 1-based cell at column x, row y lies inside r.
func (r screenRect) contains(x, y int) bool {
	return x >= r.col && x < r.col+r.width && y >= r.row && y < r.row+r.height
}

// drawPopup renders the popup frame — lines, a blank row and the dimmed
// footer — centred on the last menu (or the terminal), hides the cursor and
// returns the box it drew, border included.
func drawPopup(w io.Writer, title string, lines []string, footer string) screenRect {
	body := append([]string{}, lines...)
	body = append(body, "", fmt.Sprintf("%s%s%s", cDim, footer, cReset))

	f := Frame{Title: title, Padding: 2, BorderColor: cLightGray}
	rendered := f.Render(body)
	popupLines := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")

	popupWidth := visibleWidth(popupLines[0])

	var startCol, startRow int
	if lastMenuRect.width > 0 && lastMenuRect.height > 0 {
		startCol = lastMenuRect.col + (lastMenuRect.width-popupWidth)/2
		startRow = lastMenuRect.row + (lastMenuRect.height-len(popupLines))/2
	} else {
		tw := terminalWidth()
		th := terminalHeight()
		startCol = (tw-popupWidth)/2 + 1
		startRow = (th-len(popupLines))/2 + 1
	}
	if startCol < 1 {
		startCol = 1
	}
	if startRow < 1 {
		startRow = 1
	}

	var buf strings.Builder
	buf.WriteString(escCursorHide)
	for i, line := range popupLines {
		fmt.Fprintf(&buf, "\033[%d;%dH%s", startRow+i, startCol, line)
	}
	fmt.Fprint(w, buf.String())

	return screenRect{row: startRow, col: startCol, width: popupWidth, height: len(popupLines)}
}
