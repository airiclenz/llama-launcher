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
	// (1006); escMouseTrackOn adds any-motion tracking (1003) to them, so the
	// pointer is reported as it moves. escMouseOff is the one off-switch for
	// both: it turns all three modes off in reverse order, and switching off
	// a mode that is not on is harmless.
	escMouseOn      = "\033[?1000h\033[?1006h"
	escMouseTrackOn = "\033[?1000h\033[?1003h\033[?1006h"
	escMouseOff     = "\033[?1006l\033[?1003l\033[?1000l"

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

	// The mouse events decodeInput reports: a left-button press and pointer
	// motion (each with its position on the inputEvent), the two wheel
	// directions, and every other mouse report — release, other buttons —
	// which callers ignore.
	keyClick        keyCode = -7
	keyWheelUp      keyCode = -8
	keyWheelDown    keyCode = -9
	keyMouseIgnored keyCode = -10
	keyHover        keyCode = -11
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
// refresh tick and before every repaint; it returns the header lines plus a
// flag marking the menu stale. Mouse reporting with pointer tracking is on
// while the menu holds raw mode (menuRawEnter): the selection follows the
// pointer onto the selectable row under it, a left click on a selectable row
// chooses it as Enter would, a wheel notch moves the selection as Up/Down do,
// and every other mouse report is ignored. An event that changes nothing
// visible (menuStep) causes no repaint and does not postpone the refresh tick.
// Both modes are undone on every return path, mouse first.
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
	menuRawEnter(os.Stdout)

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

		// Motion reports arrive far faster than the refresh tick: events that
		// change nothing visible are absorbed here, against a deadline fixed
		// at this repaint, so they can neither flicker the screen nor
		// postpone the next headerFn run.
		deadline := time.Now().Add(refresh)
		repaint := false
		for wait := refresh; !repaint; {
			event := readInputTimeout(wait)
			next, changed, result, done := menuStep(event, geometry, items, selected)
			if done {
				return result
			}
			selected = next
			wait = remainingWait(deadline, time.Now())
			repaint = changed || wait <= 0
		}
	}
}

// menuStep decides what one input event does to the menu drawn at geometry:
// the selection after it, whether the screen must be repainted, and — when
// done is true — the value selectMenu returns (an item index, or -1 for
// quit). A hover that leaves the selection where it is, an ignored mouse
// report and an elapsed wait (keyNone) change nothing visible and ask for no
// repaint; every other event does, as before pointer tracking.
func menuStep(event inputEvent, geometry menuGeometry, items []menuItem, selected int) (next int, repaint bool, result int, done bool) {
	switch event.key {
	case keyNone, keyMouseIgnored:
		return selected, false, 0, false
	case keyHover:
		next = hoverSelection(geometry, items, selected, event.x, event.y)
		return next, next != selected, 0, false
	case keyUp, keyWheelUp:
		return moveSelection(items, selected, -1), true, 0, false
	case keyDown, keyWheelDown:
		return moveSelection(items, selected, 1), true, 0, false
	case keyClick:
		if idx, ok := menuItemAt(geometry, items, event.x, event.y); ok {
			return selected, false, idx, true
		}
	case keyEnter:
		return selected, false, selected, true
	case keyQ, keyEscape, keyCtrlC:
		return selected, false, -1, true
	default:
		if idx, ok := digitItem(items, event.key); ok {
			return selected, false, idx, true
		}
	}
	return selected, true, 0, false
}

// digitItem returns the index of the selectable item a digit key '1'..'9'
// names — the nth one, separators not counted — and true; false for any other
// key or a digit past the last item.
func digitItem(items []menuItem, key keyCode) (int, bool) {
	if key < keyCode('1') || key > keyCode('9') {
		return -1, false
	}
	remaining := int(key - keyCode('1'))
	for i, item := range items {
		if item.Separator {
			continue
		}
		if remaining == 0 {
			return i, true
		}
		remaining--
	}
	return -1, false
}

// remainingWait returns how long selectMenu may still wait for input before
// its next refresh is due at deadline: zero or negative once now has reached
// it.
func remainingWait(deadline, now time.Time) time.Duration {
	return deadline.Sub(now)
}

// menuRawEnter writes the escape that turns on selectMenu's mouse reporting to
// w: clicks, the wheel and any pointer motion, so the selection can follow the
// pointer. menuRawRestore undoes it.
func menuRawEnter(w io.Writer) {
	io.WriteString(w, escMouseTrackOn)
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

// hoverSelection returns the selection after the pointer moved to column x,
// row y: the selectable item under it, or selected unchanged over a
// separator, the title, the header, the footer or outside the frame. It
// shares menuItemAt with the click, so the highlighted row is always the row
// a click would choose.
func hoverSelection(geometry menuGeometry, items []menuItem, selected, x, y int) int {
	if idx, ok := menuItemAt(geometry, items, x, y); ok {
		return idx
	}
	return selected
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
// every click — never does, and neither does pointer motion: a keyHover
// still queued from the menu's motion tracking must not close the popup the
// click opened.
func awaitDismiss(read func() inputEvent) {
	for {
		switch read().key {
		case keyMouseIgnored, keyHover:
		default:
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

// confirmPopup shows a popup asking to confirm a model switch, with a
// [ Switch ] and a [ Cancel ] button, and reports whether it was confirmed;
// see drawConfirmPopup for what confirms and what cancels. It holds stdin in
// raw mode for the whole popup so mouse reporting is off again before the
// terminal is restored. Outside a terminal, or when raw mode cannot be
// entered, it confirms nothing.
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

const (
	confirmSwitchLabel = "[ Switch ]"
	confirmCancelLabel = "[ Cancel ]"
	confirmButtonGap   = "  "
	confirmFooter      = "enter switch · q cancel"

	// popupPadding is the blank cells a popup frame keeps left of its
	// content; popupBorderWidth is the width of the border glyph before them.
	popupPadding     = 2
	popupBorderWidth = 1
)

// confirmButton names a button of the Switch model confirmation popup, or
// none of them.
type confirmButton int

const (
	confirmButtonNone confirmButton = iota
	confirmButtonSwitch
	confirmButtonCancel
)

// confirmLayout is where one draw of the Switch model confirmation popup put
// its box (border included) and its two buttons. A button's rect is its label
// including the brackets, one row high.
type confirmLayout struct {
	box          screenRect
	switchButton screenRect
	cancelButton screenRect
}

// buttonAt returns the button drawn on the 1-based cell at column x, row y,
// or confirmButtonNone when the cell belongs to neither.
func (l confirmLayout) buttonAt(x, y int) confirmButton {
	switch {
	case l.switchButton.contains(x, y):
		return confirmButtonSwitch
	case l.cancelButton.contains(x, y):
		return confirmButtonCancel
	default:
		return confirmButtonNone
	}
}

// drawConfirmPopup renders the Switch model confirmation popup — lines, one
// centred row of [ Switch ] and [ Cancel ] buttons and an "enter switch · q
// cancel" footer — to w, turns on mouse reporting with motion tracking and
// reads input events with wait until one decides: enter, or a left click on
// [ Switch ], confirms; q, esc, ctrl-c, an unreadable key, a wait failure
// (raw mode unavailable) and a left click on [ Cancel ] all cancel;
// navigation and digit keys, a click anywhere else — inside or outside the
// box — the wheel and ignored mouse reports change nothing. The button under
// the pointer is drawn highlighted; the popup is repainted in place only when
// that button changes. The buttons take no keyboard focus. Like
// drawPopupAndWait it clears the popup, turns mouse reporting off and
// restores the cursor on every return path.
func drawConfirmPopup(w io.Writer, title string, lines []string, wait func() (inputEvent, error)) bool {
	defer fmt.Fprint(w, escClear+escMouseOff+escCursorShow)

	hovered := confirmButtonNone
	layout := drawConfirmFrame(w, title, lines, hovered)
	fmt.Fprint(w, escMouseTrackOn)

	for {
		ev, err := wait()
		if err != nil {
			return false
		}
		switch ev.key {
		case keyEnter:
			return true
		case keyQ, keyEscape, keyCtrlC, keyNone:
			return false
		case keyClick:
			if clicked := layout.buttonAt(ev.x, ev.y); clicked != confirmButtonNone {
				return clicked == confirmButtonSwitch
			}
		case keyHover:
			if pointed := layout.buttonAt(ev.x, ev.y); pointed != hovered {
				hovered = pointed
				layout = drawConfirmFrame(w, title, lines, hovered)
			}
		}
	}
}

// drawConfirmFrame draws the Switch model confirmation popup to w with the
// hovered button highlighted, and returns where the box and the buttons
// landed. The button rects are read back from the rendered frame lines, so
// they are the cells the labels were drawn on.
func drawConfirmFrame(w io.Writer, title string, lines []string, hovered confirmButton) confirmLayout {
	popupLines := renderConfirmFrame(title, lines, hovered)
	box := placePopup(w, popupLines)

	return confirmLayout{
		box:          box,
		switchButton: labelRect(popupLines, box, confirmSwitchLabel),
		cancelButton: labelRect(popupLines, box, confirmCancelLabel),
	}
}

// renderConfirmFrame renders the confirmation popup's frame lines: lines, a
// blank row, the button row centred in the box, a blank row and the dimmed
// footer.
func renderConfirmFrame(title string, lines []string, hovered confirmButton) []string {
	render := func(buttonRow string) []string {
		body := append([]string{}, lines...)
		body = append(body, "", buttonRow, "", cDim+confirmFooter+cReset)
		return renderPopupFrame(title, body)
	}

	// The frame sizes itself to its widest line, so the row is rendered once
	// unindented to learn the box width, then again indented to its centre.
	buttonRow := confirmButtonRow(hovered)
	boxWidth := visibleWidth(render(buttonRow)[0])
	indent := (boxWidth-visibleWidth(buttonRow))/2 - popupBorderWidth - popupPadding
	if indent < 0 {
		indent = 0
	}
	return render(strings.Repeat(" ", indent) + buttonRow)
}

// confirmButtonRow returns the "[ Switch ]  [ Cancel ]" row with the hovered
// button in the selection colour and the other plain.
func confirmButtonRow(hovered confirmButton) string {
	paint := func(button confirmButton, label string) string {
		if button != hovered {
			return label
		}
		return cBoldCyan + label + cReset
	}
	return paint(confirmButtonSwitch, confirmSwitchLabel) +
		confirmButtonGap +
		paint(confirmButtonCancel, confirmCancelLabel)
}

// labelRect returns the cells label occupies in the lowest of popupLines that
// holds it, given the box those lines were drawn in. It is the zero rect,
// which contains no cell, when no line holds label.
func labelRect(popupLines []string, box screenRect, label string) screenRect {
	for i := len(popupLines) - 1; i >= 0; i-- {
		at := strings.Index(popupLines[i], label)
		if at < 0 {
			continue
		}
		return screenRect{
			row:    box.row + i,
			col:    box.col + visibleWidth(popupLines[i][:at]),
			width:  visibleWidth(label),
			height: 1,
		}
	}
	return screenRect{}
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

	return placePopup(w, renderPopupFrame(title, body))
}

// renderPopupFrame renders body in a popup frame under title and returns the
// frame's lines, top border first.
func renderPopupFrame(title string, body []string) []string {
	f := Frame{Title: title, Padding: popupPadding, BorderColor: cLightGray}
	rendered := f.Render(body)
	return strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
}

// placePopup writes popupLines to w centred on the last menu (or the
// terminal), hides the cursor and returns the box they cover, border
// included. The same lines always land on the same cells while the menu and
// the terminal keep their size, so a popup is repainted in place by placing
// it again.
func placePopup(w io.Writer, popupLines []string) screenRect {
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
