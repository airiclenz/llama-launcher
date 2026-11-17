package launcher

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRenderMenuRowsMultibyteLabelColumn pins the label-column measurement of
// the interactive menu. The column pads to the widest label by visible width;
// measuring it in bytes counts the two-byte "é" twice, so a multibyte profile
// title pushes the whole description column — the context-size cell, the
// [server] tag and the ★ marker — right of where an equally wide ASCII title
// puts it.
func TestRenderMenuRowsMultibyteLabelColumn(t *testing.T) {
	t.Parallel()

	menuRows := func(title string) []string {
		host := "127.0.0.1"
		port := 8080
		cfg := &Config{
			Servers: map[string]ServerConfig{
				"llamacpp": {Enabled: true},
				"ollama":   {Enabled: true},
			},
			Profiles: map[string]Profile{
				"alpha": {Title: title, ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(65536)}},
				"bravo": {IsFavourite: true, ProfileParams: ProfileParams{Server: strPtrLocal("llamacpp"), ContextSize: ptrInt(131072)}},
				"olla":  {ProfileParams: ProfileParams{Server: strPtrLocal("ollama"), ContextSize: ptrInt(32768)}},
			},
		}
		cfg.Defaults = ProfileParams{Host: &host, Port: &port}
		return renderMenuRows(buildProfileItems(cfg, []string{"alpha", "bravo", "olla"}), 0)
	}

	t.Run("label column is measured in runes, not bytes", func(t *testing.T) {
		t.Parallel()

		if got, want := menuLabelWidth([]menuItem{{Label: "café"}, {Separator: true}}), len([]rune("café"))+2; got != want {
			t.Errorf("menuLabelWidth = %d, want %d", got, want)
		}
	})

	t.Run("a multibyte title leaves every following column in place", func(t *testing.T) {
		t.Parallel()

		multibyte := menuRows("café latte")
		ascii := menuRows("cafe latte")
		if len(multibyte) != 3 || len(ascii) != 3 {
			t.Fatalf("expected 3 rows each, got %d and %d", len(multibyte), len(ascii))
		}

		for i := range multibyte {
			want := strings.Replace(stripEscapes(ascii[i]), "cafe latte", "café latte", 1)
			if got := stripEscapes(multibyte[i]); got != want {
				t.Errorf("row %d shifted by the multibyte title:\n got %q\nwant %q", i, got, want)
			}
		}
	})
}

// stripEscapes removes ANSI escape sequences from a rendered row, leaving the
// text a terminal actually shows. It mirrors the escape handling of
// visibleWidth, so column positions in its result match that measurement.
func stripEscapes(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestDrawPopupAndWaitRestoresCursor(t *testing.T) {
	t.Parallel()

	t.Run("restores cursor and clears popup after the key wait", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		var atWait string
		drawPopupAndWait(&out, "Title", []string{"line one"}, func() error {
			atWait = out.String()
			return nil
		})

		if !strings.Contains(atWait, escCursorHide) {
			t.Errorf("popup drawn without hiding the cursor: %q", atWait)
		}
		if !strings.Contains(atWait, escMouseOn) {
			t.Errorf("mouse reporting not on during the key wait: %q", atWait)
		}
		if strings.Contains(atWait, escCursorShow) {
			t.Errorf("cursor restored before the key wait: %q", atWait)
		}
		tail := strings.TrimPrefix(out.String(), atWait)
		if !strings.Contains(tail, escCursorShow) {
			t.Errorf("cursor not restored after the key wait: %q", tail)
		}
		if !strings.Contains(tail, escClear) {
			t.Errorf("popup not cleared after the key wait: %q", tail)
		}
		assertMouseOffBeforeCursorShow(t, tail)
	})

	t.Run("restores cursor and clears popup when raw mode fails", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		drawPopupAndWait(&out, "Title", []string{"line one"}, func() error {
			return errors.New("raw mode unavailable")
		})

		if !strings.Contains(out.String(), escCursorShow) {
			t.Errorf("cursor not restored after wait failure: %q", out.String())
		}
		if !strings.Contains(out.String(), escClear) {
			t.Errorf("popup not cleared after wait failure: %q", out.String())
		}
		assertMouseOffBeforeCursorShow(t, out.String())
	})
}

// assertMouseOffBeforeCursorShow fails t unless output turns mouse reporting
// off before it shows the cursor again.
func assertMouseOffBeforeCursorShow(t *testing.T, output string) {
	t.Helper()

	off := strings.LastIndex(output, escMouseOff)
	show := strings.LastIndex(output, escCursorShow)
	if off < 0 || off > show {
		t.Errorf("mouse reporting not turned off before the cursor is shown: %q", output)
	}
}

// TestDrawPopupReturnsDrawnRect pins the box drawPopup reports against what
// it wrote: the first cursor position is the box's top-left cell, one line is
// drawn per row of the box, and every line is the box's width.
func TestDrawPopupReturnsDrawnRect(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	box := drawPopup(&out, "Title", []string{"line one", "a somewhat longer line two"}, "footer")

	positions := regexp.MustCompile(`\x1b\[(\d+);(\d+)H([^\x1b]*(?:\x1b\[[0-9;]*m[^\x1b]*)*)`).FindAllStringSubmatch(out.String(), -1)
	if len(positions) != box.height {
		t.Fatalf("drew %d lines, box height %d", len(positions), box.height)
	}
	for i, position := range positions {
		row, rowErr := strconv.Atoi(position[1])
		col, colErr := strconv.Atoi(position[2])
		if rowErr != nil || colErr != nil {
			t.Fatalf("line %d: unreadable cursor position %q", i, position[0])
		}
		if row != box.row+i || col != box.col {
			t.Errorf("line %d drawn at %d;%d, want %d;%d", i, row, col, box.row+i, box.col)
		}
		if width := visibleWidth(position[3]); width != box.width {
			t.Errorf("line %d is %d cells wide, box width %d", i, width, box.width)
		}
	}
}

// confirmPopupRun is one scripted run of drawConfirmPopup: what it returned,
// how many events it read, and the output it had written at each wait, the
// whole output last.
type confirmPopupRun struct {
	confirmed bool
	read      int
	atWait    []string
	output    string
}

// writtenAfterWait returns what the popup wrote between its wait number
// index and the next one (or its return, for the last wait).
func (r confirmPopupRun) writtenAfterWait(index int) string {
	until := r.output
	if index+1 < len(r.atWait) {
		until = r.atWait[index+1]
	}
	return strings.TrimPrefix(until, r.atWait[index])
}

// runConfirmPopup drives drawConfirmPopup over the scripted events, or over
// a wait that fails with waitErr when it is set.
func runConfirmPopup(t *testing.T, events []inputEvent, waitErr error) confirmPopupRun {
	t.Helper()

	var out bytes.Buffer
	run := confirmPopupRun{}
	run.confirmed = drawConfirmPopup(&out, "Big", []string{"row"}, func() (inputEvent, error) {
		run.atWait = append(run.atWait, out.String())
		if waitErr != nil {
			return inputEvent{key: keyNone}, waitErr
		}
		if run.read >= len(events) {
			t.Fatalf("popup still open after every scripted event")
		}
		event := events[run.read]
		run.read++
		return event, nil
	})
	run.output = out.String()
	return run
}

// TestSwitchModel_ConfirmPopup pins the switch confirmation input: enter and
// a left click on [ Switch ] confirm; q, esc, an unreadable key, a raw-mode
// failure and a left click on [ Cancel ] cancel; navigation, the wheel,
// ignored mouse reports and a click anywhere else — beside a button, on the
// box or outside it — change nothing. Every path waits with motion tracking
// on, then clears the popup, turns mouse reporting off and restores the
// cursor.
func TestSwitchModel_ConfirmPopup(t *testing.T) {
	t.Parallel()

	layout := drawConfirmFrame(io.Discard, "Big", []string{"row"}, confirmButtonNone)
	box, switchButton, cancelButton := layout.box, layout.switchButton, layout.cancelButton
	click := func(x, y int) inputEvent { return inputEvent{key: keyClick, x: x, y: y} }
	key := func(code keyCode) inputEvent { return inputEvent{key: code} }
	lastCol := func(r screenRect) int { return r.col + r.width - 1 }
	buttonRow := switchButton.row

	tests := []struct {
		name   string
		events []inputEvent
		err    error
		want   bool
	}{
		{name: "enter confirms", events: []inputEvent{key(keyEnter)}, want: true},
		{name: "q cancels", events: []inputEvent{key(keyQ)}},
		{name: "esc cancels", events: []inputEvent{key(keyEscape)}},
		{name: "ctrl-c cancels", events: []inputEvent{key(keyCtrlC)}},
		{name: "unreadable key cancels", events: []inputEvent{key(keyNone)}},
		{name: "navigation is ignored", events: []inputEvent{key(keyUp), key(keyDown), key(keyEnter)}, want: true},
		{name: "ignored mouse report does not cancel", events: []inputEvent{key(keyMouseIgnored), key(keyEnter)}, want: true},
		{name: "click on the first cell of switch confirms", events: []inputEvent{click(switchButton.col, buttonRow)}, want: true},
		{name: "click on the last cell of switch confirms", events: []inputEvent{click(lastCol(switchButton), buttonRow)}, want: true},
		{name: "click on the first cell of cancel cancels", events: []inputEvent{click(cancelButton.col, buttonRow)}},
		{name: "click on the last cell of cancel cancels", events: []inputEvent{click(lastCol(cancelButton), buttonRow)}},
		{name: "click left of switch is ignored", events: []inputEvent{click(switchButton.col-1, buttonRow), key(keyQ)}},
		{name: "click right of switch is ignored", events: []inputEvent{click(lastCol(switchButton)+1, buttonRow), key(keyQ)}},
		{name: "click left of cancel is ignored", events: []inputEvent{click(cancelButton.col-1, buttonRow), key(keyQ)}},
		{name: "click right of cancel is ignored", events: []inputEvent{click(lastCol(cancelButton)+1, buttonRow), key(keyQ)}},
		{name: "click above switch is ignored", events: []inputEvent{click(switchButton.col, buttonRow-1), key(keyQ)}},
		{name: "click below cancel is ignored", events: []inputEvent{click(cancelButton.col, buttonRow+1), key(keyQ)}},
		{name: "click on the box corner is ignored", events: []inputEvent{click(box.col, box.row), key(keyQ)}},
		{name: "click on a text row inside the box is ignored", events: []inputEvent{click(switchButton.col, box.row+2), key(keyQ)}},
		{name: "click outside the box is ignored", events: []inputEvent{click(box.col+box.width, box.row+box.height), key(keyQ)}},
		{name: "click elsewhere then enter confirms", events: []inputEvent{click(box.col, box.row), key(keyEnter)}, want: true},
		{name: "wheel is ignored", events: []inputEvent{key(keyWheelUp), key(keyWheelDown), key(keyQ)}},
		{name: "raw mode failure cancels", err: errors.New("raw mode unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := runConfirmPopup(t, tt.events, tt.err)

			if run.confirmed != tt.want {
				t.Errorf("drawConfirmPopup = %v, want %v", run.confirmed, tt.want)
			}
			if run.read != len(tt.events) {
				t.Errorf("popup decided after %d of %d events", run.read, len(tt.events))
			}
			drawn := run.atWait[0]
			for _, text := range []string{confirmSwitchLabel + confirmButtonGap + confirmCancelLabel, "enter switch · q cancel"} {
				if !strings.Contains(drawn, text) {
					t.Errorf("popup drawn without %q: %q", text, drawn)
				}
			}
			if strings.Contains(drawn, cBoldCyan) {
				t.Errorf("a button is highlighted before the pointer moved: %q", drawn)
			}
			if !strings.Contains(drawn, escMouseTrackOn) {
				t.Errorf("motion tracking not on at the first wait: %q", drawn)
			}
			tail := run.writtenAfterWait(len(run.atWait) - 1)
			if !strings.Contains(tail, escCursorShow) || !strings.Contains(tail, escClear) {
				t.Errorf("popup not cleared or cursor not restored: %q", tail)
			}
			assertMouseOffBeforeCursorShow(t, tail)
		})
	}
}

// TestSwitchModel_ConfirmPopupHover pins the button highlight: the button
// under the pointer is repainted in the selection colour, neither is once the
// pointer leaves, a hover that changes nothing writes nothing, and hovering
// decides nothing — enter still confirms.
func TestSwitchModel_ConfirmPopupHover(t *testing.T) {
	t.Parallel()

	layout := drawConfirmFrame(io.Discard, "Big", []string{"row"}, confirmButtonNone)
	hover := func(x, y int) inputEvent { return inputEvent{key: keyHover, x: x, y: y} }
	overSwitch := hover(layout.switchButton.col, layout.switchButton.row)
	overCancel := hover(layout.cancelButton.col, layout.cancelButton.row)
	overBox := hover(layout.box.col, layout.box.row)
	plainRow := confirmSwitchLabel + confirmButtonGap + confirmCancelLabel
	switchLit := cBoldCyan + confirmSwitchLabel + cReset + confirmButtonGap + confirmCancelLabel
	cancelLit := confirmSwitchLabel + confirmButtonGap + cBoldCyan + confirmCancelLabel + cReset

	tests := []struct {
		name string
		// events are followed by an enter; wantWritten[i] is the button row
		// the popup must repaint after events[i], or "" for no output at all.
		events      []inputEvent
		wantWritten []string
	}{
		{name: "hover over switch highlights it", events: []inputEvent{overSwitch}, wantWritten: []string{switchLit}},
		{name: "hover over cancel highlights it", events: []inputEvent{overCancel}, wantWritten: []string{cancelLit}},
		{name: "hover elsewhere writes nothing", events: []inputEvent{overBox, hover(1, 1)}, wantWritten: []string{"", ""}},
		{name: "hover within one button writes nothing", events: []inputEvent{overSwitch, hover(overSwitch.x+1, overSwitch.y)}, wantWritten: []string{switchLit, ""}},
		{name: "highlight moves between the buttons", events: []inputEvent{overSwitch, overCancel}, wantWritten: []string{switchLit, cancelLit}},
		{name: "highlight clears when the pointer leaves", events: []inputEvent{overCancel, overBox}, wantWritten: []string{cancelLit, plainRow}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			events := append(append([]inputEvent{}, tt.events...), inputEvent{key: keyEnter})

			run := runConfirmPopup(t, events, nil)

			if !run.confirmed {
				t.Errorf("enter after hovering did not confirm")
			}
			for i, want := range tt.wantWritten {
				written := run.writtenAfterWait(i)
				if want == "" && written != "" {
					t.Errorf("event %d changed nothing but wrote %q", i, written)
				}
				if want != "" && !strings.Contains(written, want) {
					t.Errorf("event %d: button row %q not repainted: %q", i, want, written)
				}
			}
		})
	}
}

// TestConfirmPopupButtonRects pins the button hit rectangles against the
// drawn popup: each rect is one row high, as wide as its label, and sits on
// the cells where the label appears once the output's escapes are stripped;
// the row is centred in the box, whichever line sets the box width and
// whichever button is highlighted.
func TestConfirmPopupButtonRects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		title   string
		lines   []string
		hovered confirmButton
	}{
		{name: "buttons set the width", title: "Big", lines: []string{"row"}},
		{name: "a long line sets the width", title: "Big", lines: []string{"a line much wider than the two buttons side by side"}},
		{name: "an even-width line sets the width", title: "Big", lines: []string{"a line much wider than the two buttons side by side!"}},
		{name: "the title sets the width", title: "A title much wider than the two buttons side by side", lines: []string{"row"}},
		{name: "a line names a button", title: "Big", lines: []string{"press [ Switch ] or [ Cancel ]"}},
		{name: "switch highlighted", title: "Big", lines: []string{"row"}, hovered: confirmButtonSwitch},
		{name: "cancel highlighted", title: "Big", lines: []string{"row"}, hovered: confirmButtonCancel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			layout := drawConfirmFrame(&out, tt.title, tt.lines, tt.hovered)

			row, col := lastDrawnCell(t, out.String(), confirmSwitchLabel+confirmButtonGap+confirmCancelLabel)
			labelWidth := visibleWidth(confirmSwitchLabel)
			wantSwitch := screenRect{row: row, col: col, width: labelWidth, height: 1}
			wantCancel := screenRect{row: row, col: col + labelWidth + visibleWidth(confirmButtonGap), width: visibleWidth(confirmCancelLabel), height: 1}
			if layout.switchButton != wantSwitch {
				t.Errorf("switch button rect %+v, drawn at %+v", layout.switchButton, wantSwitch)
			}
			if layout.cancelButton != wantCancel {
				t.Errorf("cancel button rect %+v, drawn at %+v", layout.cancelButton, wantCancel)
			}
			left := wantSwitch.col - layout.box.col
			right := layout.box.col + layout.box.width - (wantCancel.col + wantCancel.width)
			if left-right > 1 || right-left > 1 {
				t.Errorf("button row not centred: %d cells left of it, %d right", left, right)
			}
			footerRow, _ := lastDrawnCell(t, out.String(), "enter switch · q cancel")
			if row <= layout.box.row+len(tt.lines) || row >= footerRow {
				t.Errorf("button row %d not between the popup lines and the footer row %d", row, footerRow)
			}
		})
	}
}

// sgrSequence matches one colour or attribute escape.
var sgrSequence = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// lastDrawnCell returns the 1-based row and column of the first cell of text
// on the lowest line of output that shows it, reading output as drawPopup
// writes it: one cursor position per line, colour escapes stripped. It fails
// t when no line shows text.
func lastDrawnCell(t *testing.T, output, text string) (row, col int) {
	t.Helper()

	positions := regexp.MustCompile(`\x1b\[(\d+);(\d+)H([^\x1b]*(?:\x1b\[[0-9;]*m[^\x1b]*)*)`).FindAllStringSubmatch(output, -1)
	for i := len(positions) - 1; i >= 0; i-- {
		plain := sgrSequence.ReplaceAllString(positions[i][3], "")
		at := strings.Index(plain, text)
		if at < 0 {
			continue
		}
		lineRow, rowErr := strconv.Atoi(positions[i][1])
		lineCol, colErr := strconv.Atoi(positions[i][2])
		if rowErr != nil || colErr != nil {
			t.Fatalf("unreadable cursor position %q", positions[i][0])
		}
		return lineRow, lineCol + len([]rune(plain[:at]))
	}
	t.Fatalf("%q not drawn: %q", text, output)
	return 0, 0
}

// TestAwaitDismiss pins what closes an any-key popup: a key, a click or a
// wheel notch closes it at once; ignored mouse reports — a button release
// above all — and pointer motion are read past without closing it.
func TestAwaitDismiss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		events   []keyCode
		wantRead int
	}{
		{name: "key closes", events: []keyCode{keyQ}, wantRead: 1},
		{name: "enter closes", events: []keyCode{keyEnter}, wantRead: 1},
		{name: "click closes", events: []keyCode{keyClick}, wantRead: 1},
		{name: "wheel up closes", events: []keyCode{keyWheelUp}, wantRead: 1},
		{name: "wheel down closes", events: []keyCode{keyWheelDown}, wantRead: 1},
		{name: "release does not close", events: []keyCode{keyMouseIgnored, keyMouseIgnored, keyClick}, wantRead: 3},
		{name: "hover does not close", events: []keyCode{keyHover, keyHover, keyClick}, wantRead: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			read := 0

			awaitDismiss(func() inputEvent {
				if read >= len(tt.events) {
					t.Fatalf("still waiting after every scripted event")
				}
				event := inputEvent{key: tt.events[read], x: 1, y: 1}
				read++
				return event
			})

			if read != tt.wantRead {
				t.Errorf("closed after %d events, want %d", read, tt.wantRead)
			}
		})
	}
}

// TestDecodeInput pins the raw-mode input decoder: every key the menu reads,
// the SGR mouse reports it acts on or ignores, other escape sequences
// consumed whole as keyNone, a sequence split across reads reported
// incomplete, and only the first of several buffered events decoded.
func TestDecodeInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		wantEvent    inputEvent
		wantConsumed int
		wantComplete bool
	}{
		{name: "carriage return", input: "\r", wantEvent: inputEvent{key: keyEnter}, wantConsumed: 1, wantComplete: true},
		{name: "line feed", input: "\n", wantEvent: inputEvent{key: keyEnter}, wantConsumed: 1, wantComplete: true},
		{name: "ctrl-c", input: "\x03", wantEvent: inputEvent{key: keyCtrlC}, wantConsumed: 1, wantComplete: true},
		{name: "q", input: "q", wantEvent: inputEvent{key: keyQ}, wantConsumed: 1, wantComplete: true},
		{name: "capital Q", input: "Q", wantEvent: inputEvent{key: keyQ}, wantConsumed: 1, wantComplete: true},
		{name: "digit 1", input: "1", wantEvent: inputEvent{key: keyCode('1')}, wantConsumed: 1, wantComplete: true},
		{name: "digit 9", input: "9", wantEvent: inputEvent{key: keyCode('9')}, wantConsumed: 1, wantComplete: true},
		{name: "digit 0 is no key", input: "0", wantEvent: inputEvent{key: keyNone}, wantConsumed: 1, wantComplete: true},
		{name: "other letter is no key", input: "x", wantEvent: inputEvent{key: keyNone}, wantConsumed: 1, wantComplete: true},
		{name: "arrow up", input: "\x1b[A", wantEvent: inputEvent{key: keyUp}, wantConsumed: 3, wantComplete: true},
		{name: "arrow down", input: "\x1b[B", wantEvent: inputEvent{key: keyDown}, wantConsumed: 3, wantComplete: true},
		{name: "lone esc", input: "\x1b", wantEvent: inputEvent{key: keyEscape}, wantConsumed: 1, wantComplete: true},
		{name: "left click", input: "\x1b[<0;5;7M", wantEvent: inputEvent{key: keyClick, x: 5, y: 7}, wantConsumed: 9, wantComplete: true},
		{name: "click with multi-digit coordinates", input: "\x1b[<0;123;45M", wantEvent: inputEvent{key: keyClick, x: 123, y: 45}, wantConsumed: 12, wantComplete: true},
		{name: "wheel up", input: "\x1b[<64;10;3M", wantEvent: inputEvent{key: keyWheelUp}, wantConsumed: 11, wantComplete: true},
		{name: "wheel down", input: "\x1b[<65;10;3M", wantEvent: inputEvent{key: keyWheelDown}, wantConsumed: 11, wantComplete: true},
		{name: "left release", input: "\x1b[<0;5;7m", wantEvent: inputEvent{key: keyMouseIgnored}, wantConsumed: 9, wantComplete: true},
		{name: "motion", input: "\x1b[<35;5;7M", wantEvent: inputEvent{key: keyHover, x: 5, y: 7}, wantConsumed: 10, wantComplete: true},
		{name: "motion with multi-digit column", input: "\x1b[<35;12;7M", wantEvent: inputEvent{key: keyHover, x: 12, y: 7}, wantConsumed: 11, wantComplete: true},
		{name: "motion with left held", input: "\x1b[<32;3;4M", wantEvent: inputEvent{key: keyHover, x: 3, y: 4}, wantConsumed: 10, wantComplete: true},
		{name: "motion with release final byte", input: "\x1b[<35;12;7m", wantEvent: inputEvent{key: keyMouseIgnored}, wantConsumed: 11, wantComplete: true},
		{name: "left click without motion bit", input: "\x1b[<0;5;6M", wantEvent: inputEvent{key: keyClick, x: 5, y: 6}, wantConsumed: 9, wantComplete: true},
		{name: "wheel up at origin", input: "\x1b[<64;1;1M", wantEvent: inputEvent{key: keyWheelUp}, wantConsumed: 10, wantComplete: true},
		{name: "wheel down at origin", input: "\x1b[<65;1;1M", wantEvent: inputEvent{key: keyWheelDown}, wantConsumed: 10, wantComplete: true},
		{name: "right click", input: "\x1b[<2;5;7M", wantEvent: inputEvent{key: keyMouseIgnored}, wantConsumed: 9, wantComplete: true},
		{name: "malformed mouse report", input: "\x1b[<0;5M", wantEvent: inputEvent{key: keyMouseIgnored}, wantConsumed: 7, wantComplete: true},
		{name: "split mouse report", input: "\x1b[<0;12", wantComplete: false},
		{name: "split csi introducer", input: "\x1b[", wantComplete: false},
		{name: "split ss3", input: "\x1bO", wantComplete: false},
		{name: "empty buffer", input: "", wantComplete: false},
		{name: "two events decode the first", input: "\x1b[<64;1;1M\x1b[<0;2;3M", wantEvent: inputEvent{key: keyWheelUp}, wantConsumed: 10, wantComplete: true},
		{name: "key then mouse decodes the key", input: "q\x1b[<0;2;3M", wantEvent: inputEvent{key: keyQ}, wantConsumed: 1, wantComplete: true},
		{name: "page up", input: "\x1b[5~", wantEvent: inputEvent{key: keyNone}, wantConsumed: 4, wantComplete: true},
		{name: "delete", input: "\x1b[3~", wantEvent: inputEvent{key: keyNone}, wantConsumed: 4, wantComplete: true},
		{name: "f5", input: "\x1b[15~", wantEvent: inputEvent{key: keyNone}, wantConsumed: 5, wantComplete: true},
		{name: "arrow right", input: "\x1b[C", wantEvent: inputEvent{key: keyNone}, wantConsumed: 3, wantComplete: true},
		{name: "ss3 arrow up", input: "\x1bOA", wantEvent: inputEvent{key: keyNone}, wantConsumed: 3, wantComplete: true},
		{name: "esc then other byte", input: "\x1bx", wantEvent: inputEvent{key: keyNone}, wantConsumed: 2, wantComplete: true},
		{name: "truncated csi leaves enter", input: "\x1b[\r", wantEvent: inputEvent{key: keyNone}, wantConsumed: 2, wantComplete: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, consumed, complete := decodeInput([]byte(tt.input))

			if complete != tt.wantComplete {
				t.Fatalf("decodeInput(%q) complete = %v, want %v", tt.input, complete, tt.wantComplete)
			}
			if !tt.wantComplete {
				return
			}
			if ev != tt.wantEvent || consumed != tt.wantConsumed {
				t.Errorf(
					"decodeInput(%q) = (%+v, %d), want (%+v, %d)",
					tt.input, ev, consumed, tt.wantEvent, tt.wantConsumed,
				)
			}
		})
	}
}

// TestDecodeInput_SplitSequenceReassembles feeds a mouse report split across
// two reads: the first part is incomplete, and once the rest arrives the
// joined bytes decode to the click.
func TestDecodeInput_SplitSequenceReassembles(t *testing.T) {
	t.Parallel()
	first, rest := []byte("\x1b[<0;1"), []byte("2;34M")

	_, _, firstComplete := decodeInput(first)
	ev, consumed, complete := decodeInput(append(first, rest...))

	if firstComplete {
		t.Fatalf("decodeInput(%q) complete before the rest arrived", first)
	}
	want := inputEvent{key: keyClick, x: 12, y: 34}
	if !complete || ev != want || consumed != len(first)+len(rest) {
		t.Errorf("joined decode = (%+v, %d, %v), want (%+v, %d, true)", ev, consumed, complete, want, len(first)+len(rest))
	}
}

// TestDecodeInput_SeveralEventsOneAtATime walks a buffer holding three events
// and checks each comes out in order and the buffer is used up exactly.
func TestDecodeInput_SeveralEventsOneAtATime(t *testing.T) {
	t.Parallel()
	buf := []byte("\x1b[<65;1;1M\x1b[B\r")
	want := []keyCode{keyWheelDown, keyDown, keyEnter}

	var got []keyCode
	for len(buf) > 0 {
		ev, consumed, complete := decodeInput(buf)
		if !complete {
			t.Fatalf("decodeInput(%q) incomplete mid-buffer", buf)
		}
		got = append(got, ev.key)
		buf = buf[consumed:]
	}

	if len(got) != len(want) {
		t.Fatalf("decoded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestMenuItemAt pins the mouse hit-test against the frame selectMenu actually
// draws: for every rendered line, a click inside the frame selects exactly the
// item whose label that line shows, and nothing on the title, header, footer,
// border or separator lines; a click left or right of the frame selects
// nothing.
func TestMenuItemAt(t *testing.T) {
	t.Parallel()

	items := []menuItem{
		{Label: "alpha", Description: "first"},
		{Separator: true},
		{Label: "bravo", Description: "second"},
		{Label: "charlie", Description: "last"},
	}
	tests := []struct {
		name               string
		title              string
		header             []string
		startRow, startCol int
	}{
		{name: "no title, no header", startRow: 2, startCol: 1},
		{name: "title only", title: "Profiles", startRow: 2, startCol: 1},
		{name: "title and one header line", title: "Profiles", header: []string{"status"}, startRow: 2, startCol: 1},
		{name: "title and a longer header", title: "Profiles", header: []string{"status", "model", "uptime", "port"}, startRow: 2, startCol: 1},
		{name: "centered origin", title: "Profiles", header: []string{"status", "model"}, startRow: 9, startCol: 31},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			frame := Frame{Title: tc.title, Header: tc.header, Footer: []string{"hints"}, RawMode: true}
			lines := strings.Split(strings.TrimSuffix(frame.Render(renderMenuRows(items, 0)), "\r\n"), "\r\n")
			frameWidth := visibleWidth(lines[0])
			geometry := newMenuGeometry(frame, tc.startRow, tc.startCol, frameWidth)

			for row, line := range lines {
				want, wantOK := labelledItem(items, stripEscapes(line))
				y := tc.startRow + row
				for _, x := range []int{tc.startCol, tc.startCol + frameWidth/2, tc.startCol + frameWidth - 1} {
					got, ok := menuItemAt(geometry, items, x, y)
					if got != want || ok != wantOK {
						t.Errorf("menuItemAt(x=%d, y=%d) on %q = (%d, %v), want (%d, %v)", x, y, stripEscapes(line), got, ok, want, wantOK)
					}
				}
				for _, x := range []int{tc.startCol - 1, tc.startCol + frameWidth} {
					if got, ok := menuItemAt(geometry, items, x, y); ok {
						t.Errorf("menuItemAt(x=%d, y=%d) outside the frame = (%d, true), want no item", x, y, got)
					}
				}
			}
			for _, y := range []int{tc.startRow - 1, tc.startRow + len(lines)} {
				if got, ok := menuItemAt(geometry, items, tc.startCol+1, y); ok {
					t.Errorf("menuItemAt(y=%d) above or below the frame = (%d, true), want no item", y, got)
				}
			}
		})
	}
}

// labelledItem returns the index of the selectable item whose label a rendered
// menu line shows after its row marker, and true; false for any other line.
func labelledItem(items []menuItem, line string) (int, bool) {
	for i, item := range items {
		if item.Separator {
			continue
		}
		if strings.Contains(line, "▶ "+item.Label+" ") || strings.Contains(line, "· "+item.Label+" ") {
			return i, true
		}
	}
	return -1, false
}

// TestSelectMenuMoveSelection pins the separator-skipping step the arrow keys
// and the mouse wheel share.
func TestSelectMenuMoveSelection(t *testing.T) {
	t.Parallel()

	items := []menuItem{
		{Label: "alpha"},
		{Separator: true},
		{Label: "bravo"},
		{Label: "charlie"},
	}
	tests := []struct {
		name     string
		selected int
		step     int
		want     int
	}{
		{name: "down skips a separator", selected: 0, step: 1, want: 2},
		{name: "up skips a separator", selected: 2, step: -1, want: 0},
		{name: "down to the next row", selected: 2, step: 1, want: 3},
		{name: "down stays on the last row", selected: 3, step: 1, want: 3},
		{name: "up stays on the first row", selected: 0, step: -1, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := moveSelection(items, tc.selected, tc.step); got != tc.want {
				t.Errorf("moveSelection(%d, %d) = %d, want %d", tc.selected, tc.step, got, tc.want)
			}
		})
	}
}

// TestMenuRawRestore pins the order selectMenu undoes its screen modes in:
// mouse reporting off before the cursor is shown again.
func TestMenuRawRestore(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	menuRawRestore(&out)

	if got, want := out.String(), escMouseOff+escCursorShow; got != want {
		t.Errorf("menuRawRestore wrote %q, want %q", got, want)
	}
}

// TestHoverSelection pins where the selection goes when the pointer moves: onto
// the selectable row under it, and nowhere for a separator, the title, the
// header, the footer or a position outside the frame.
func TestHoverSelection(t *testing.T) {
	t.Parallel()

	items, geometry := hoverFixture()
	const selected = 3
	tests := []struct {
		name string
		x, y int
		want int
	}{
		{name: "selectable row", x: 40, y: hoverFirstBodyRow, want: 0},
		{name: "another selectable row", x: 40, y: hoverFirstBodyRow + 2, want: 2},
		{name: "already selected row", x: 40, y: hoverFirstBodyRow + 3, want: selected},
		{name: "first frame column of a row", x: geometry.left, y: hoverFirstBodyRow, want: 0},
		{name: "last frame column of a row", x: geometry.right, y: hoverFirstBodyRow, want: 0},
		{name: "separator", x: 40, y: hoverFirstBodyRow + 1, want: selected},
		{name: "header above the body", x: 40, y: hoverFirstBodyRow - 1, want: selected},
		{name: "title above the body", x: 40, y: hoverStartRow + 1, want: selected},
		{name: "footer below the body", x: 40, y: hoverFirstBodyRow + len(items), want: selected},
		{name: "left of the frame", x: geometry.left - 1, y: hoverFirstBodyRow, want: selected},
		{name: "right of the frame", x: geometry.right + 1, y: hoverFirstBodyRow, want: selected},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := hoverSelection(geometry, items, selected, tc.x, tc.y); got != tc.want {
				t.Errorf("hoverSelection(x=%d, y=%d) = %d, want %d", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

// hoverStartRow and hoverFirstBodyRow are where hoverFixture's frame starts and
// where its first item is drawn: below the top border, the title rule, two
// header lines and their divider.
const (
	hoverStartRow     = 9
	hoverFirstBodyRow = hoverStartRow + 5
)

// hoverFixture returns the items and geometry the hover and step tests share:
// the TestMenuItemAt items in a titled, two-line-header frame drawn at a
// centered origin.
func hoverFixture() ([]menuItem, menuGeometry) {
	items := []menuItem{
		{Label: "alpha", Description: "first"},
		{Separator: true},
		{Label: "bravo", Description: "second"},
		{Label: "charlie", Description: "last"},
	}
	frame := Frame{Title: "Profiles", Header: []string{"status", "model"}, Footer: []string{"hints"}, RawMode: true}
	lines := strings.Split(strings.TrimSuffix(frame.Render(renderMenuRows(items, 0)), "\r\n"), "\r\n")
	return items, newMenuGeometry(frame, hoverStartRow, 31, visibleWidth(lines[0]))
}

// TestMenuRawEnter pins the mouse mode selectMenu turns on: click and wheel
// reporting plus any-motion tracking, without which no hover is ever reported.
func TestMenuRawEnter(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	menuRawEnter(&out)

	if got, want := out.String(), escMouseTrackOn; got != want {
		t.Errorf("menuRawEnter wrote %q, want %q", got, want)
	}
}

// TestMenuStep pins what each input event does to the open menu: which events
// leave the screen alone, which move the selection and repaint, and which end
// the menu with a result.
func TestMenuStep(t *testing.T) {
	t.Parallel()

	items, geometry := hoverFixture()
	const selected = 2
	onRow := func(key keyCode, row int) inputEvent {
		return inputEvent{key: key, x: geometry.left + 1, y: hoverFirstBodyRow + row}
	}
	tests := []struct {
		name         string
		event        inputEvent
		wantSelected int
		wantRepaint  bool
		wantResult   int
		wantDone     bool
	}{
		{name: "hover on the selected row", event: onRow(keyHover, 2), wantSelected: selected},
		{name: "hover on a separator", event: onRow(keyHover, 1), wantSelected: selected},
		{name: "hover outside the frame", event: inputEvent{key: keyHover, x: geometry.right + 1, y: hoverFirstBodyRow}, wantSelected: selected},
		{name: "ignored mouse report", event: inputEvent{key: keyMouseIgnored}, wantSelected: selected},
		{name: "elapsed wait", event: inputEvent{key: keyNone}, wantSelected: selected},
		{name: "hover onto another row", event: onRow(keyHover, 0), wantSelected: 0, wantRepaint: true},
		{name: "wheel up", event: inputEvent{key: keyWheelUp}, wantSelected: 0, wantRepaint: true},
		{name: "wheel down", event: inputEvent{key: keyWheelDown}, wantSelected: 3, wantRepaint: true},
		{name: "arrow up", event: inputEvent{key: keyUp}, wantSelected: 0, wantRepaint: true},
		{name: "arrow down", event: inputEvent{key: keyDown}, wantSelected: 3, wantRepaint: true},
		{name: "click on a row", event: onRow(keyClick, 3), wantSelected: selected, wantResult: 3, wantDone: true},
		{name: "click on a separator", event: onRow(keyClick, 1), wantSelected: selected, wantRepaint: true},
		{name: "enter", event: inputEvent{key: keyEnter}, wantSelected: selected, wantResult: selected, wantDone: true},
		{name: "q", event: inputEvent{key: keyQ}, wantSelected: selected, wantResult: -1, wantDone: true},
		{name: "escape", event: inputEvent{key: keyEscape}, wantSelected: selected, wantResult: -1, wantDone: true},
		{name: "ctrl+c", event: inputEvent{key: keyCtrlC}, wantSelected: selected, wantResult: -1, wantDone: true},
		{name: "digit skips the separator", event: inputEvent{key: keyCode('2')}, wantSelected: selected, wantResult: 2, wantDone: true},
		{name: "digit past the last item", event: inputEvent{key: keyCode('9')}, wantSelected: selected, wantRepaint: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			next, repaint, result, done := menuStep(tc.event, geometry, items, selected)

			if next != tc.wantSelected || repaint != tc.wantRepaint || done != tc.wantDone {
				t.Errorf("menuStep = (selected %d, repaint %v, done %v), want (%d, %v, %v)", next, repaint, done, tc.wantSelected, tc.wantRepaint, tc.wantDone)
			}
			if done && result != tc.wantResult {
				t.Errorf("menuStep result = %d, want %d", result, tc.wantResult)
			}
		})
	}
}

// TestRemainingWait pins the refresh deadline arithmetic: time is left before
// the deadline and none at or past it, so a stream of pointer motion cannot
// postpone the next refresh.
func TestRemainingWait(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{name: "before the deadline", now: deadline.Add(-300 * time.Millisecond), want: 300 * time.Millisecond},
		{name: "at the deadline", now: deadline, want: 0},
		{name: "past the deadline", now: deadline.Add(2 * time.Second), want: -2 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := remainingWait(deadline, tc.now); got != tc.want {
				t.Errorf("remainingWait = %v, want %v", got, tc.want)
			}
		})
	}
}
