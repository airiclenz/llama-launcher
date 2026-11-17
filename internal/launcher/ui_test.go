package launcher

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
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

// TestSwitchModel_ConfirmPopup pins the switch confirmation input: enter and
// a left click inside the popup box confirm; q, esc, an unreadable key and a
// raw-mode failure cancel; navigation, the wheel, a click outside the box and
// ignored mouse reports change nothing. Every path clears the popup, turns
// mouse reporting off and restores the cursor.
func TestSwitchModel_ConfirmPopup(t *testing.T) {
	t.Parallel()

	box := drawPopup(io.Discard, "Big", []string{"row"}, "enter switch · q cancel")
	click := func(x, y int) inputEvent { return inputEvent{key: keyClick, x: x, y: y} }
	key := func(code keyCode) inputEvent { return inputEvent{key: code} }
	right := box.col + box.width - 1
	bottom := box.row + box.height - 1

	tests := []struct {
		name   string
		events []inputEvent
		err    error
		want   bool
	}{
		{name: "enter confirms", events: []inputEvent{key(keyEnter)}, want: true},
		{name: "q cancels", events: []inputEvent{key(keyQ)}},
		{name: "esc cancels", events: []inputEvent{key(keyEscape)}},
		{name: "unreadable key cancels", events: []inputEvent{key(keyNone)}},
		{name: "navigation is ignored", events: []inputEvent{key(keyUp), key(keyDown), key(keyEnter)}, want: true},
		{name: "ignored mouse report does not cancel", events: []inputEvent{key(keyMouseIgnored), key(keyEnter)}, want: true},
		{name: "click on the top-left corner confirms", events: []inputEvent{click(box.col, box.row)}, want: true},
		{name: "click on the bottom-right corner confirms", events: []inputEvent{click(right, bottom)}, want: true},
		{name: "click left of the box is ignored", events: []inputEvent{click(box.col-1, box.row), key(keyQ)}},
		{name: "click right of the box is ignored", events: []inputEvent{click(right+1, box.row), key(keyQ)}},
		{name: "click above the box is ignored", events: []inputEvent{click(box.col, box.row-1), key(keyQ)}},
		{name: "click below the box is ignored", events: []inputEvent{click(box.col, bottom+1), key(keyQ)}},
		{name: "click outside then enter confirms", events: []inputEvent{click(right+1, bottom+1), key(keyEnter)}, want: true},
		{name: "wheel is ignored", events: []inputEvent{key(keyWheelUp), key(keyWheelDown), key(keyQ)}},
		{name: "raw mode failure cancels", err: errors.New("raw mode unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			var atWait string
			next := 0
			got := drawConfirmPopup(&out, "Big", []string{"row"}, func() (inputEvent, error) {
				if atWait == "" {
					atWait = out.String()
				}
				if tt.err != nil {
					return key(keyNone), tt.err
				}
				if next >= len(tt.events) {
					t.Fatalf("popup still open after every scripted event")
				}
				event := tt.events[next]
				next++
				return event, nil
			})

			if got != tt.want {
				t.Errorf("drawConfirmPopup = %v, want %v", got, tt.want)
			}
			if next != len(tt.events) {
				t.Errorf("popup decided after %d of %d events", next, len(tt.events))
			}
			if !strings.Contains(atWait, "enter switch · q cancel") {
				t.Errorf("popup drawn without the confirm footer: %q", atWait)
			}
			if !strings.Contains(atWait, escMouseOn) {
				t.Errorf("mouse reporting not on while waiting: %q", atWait)
			}
			tail := strings.TrimPrefix(out.String(), atWait)
			if !strings.Contains(tail, escCursorShow) || !strings.Contains(tail, escClear) {
				t.Errorf("popup not cleared or cursor not restored: %q", tail)
			}
			assertMouseOffBeforeCursorShow(t, tail)
		})
	}
}

// TestAwaitDismiss pins what closes an any-key popup: a key, a click or a
// wheel notch closes it at once; ignored mouse reports — a button release
// above all — are read past without closing it.
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
		{name: "motion", input: "\x1b[<35;5;7M", wantEvent: inputEvent{key: keyMouseIgnored}, wantConsumed: 10, wantComplete: true},
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
