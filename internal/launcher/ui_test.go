package launcher

import (
	"bytes"
	"errors"
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
	})
}

// TestSwitchModel_ConfirmPopup pins the switch confirmation keys: enter
// confirms; q, esc, an unreadable key and a raw-mode failure cancel;
// navigation keys and ignored mouse reports are ignored. Every path clears the popup and restores the
// cursor.
func TestSwitchModel_ConfirmPopup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []keyCode
		err  error
		want bool
	}{
		{name: "enter confirms", keys: []keyCode{keyEnter}, want: true},
		{name: "q cancels", keys: []keyCode{keyQ}},
		{name: "esc cancels", keys: []keyCode{keyEscape}},
		{name: "unreadable key cancels", keys: []keyCode{keyNone}},
		{name: "navigation is ignored", keys: []keyCode{keyUp, keyDown, keyEnter}, want: true},
		{name: "ignored mouse report does not cancel", keys: []keyCode{keyMouseIgnored, keyEnter}, want: true},
		{name: "raw mode failure cancels", err: errors.New("raw mode unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			var atWait string
			next := 0
			got := drawConfirmPopup(&out, "Big", []string{"row"}, func() (keyCode, error) {
				atWait = out.String()
				if tt.err != nil {
					return keyNone, tt.err
				}
				key := tt.keys[next]
				next++
				return key, nil
			})

			if got != tt.want {
				t.Errorf("drawConfirmPopup = %v, want %v", got, tt.want)
			}
			if !strings.Contains(atWait, "enter switch · q cancel") {
				t.Errorf("popup drawn without the confirm footer: %q", atWait)
			}
			tail := strings.TrimPrefix(out.String(), atWait)
			if !strings.Contains(tail, escCursorShow) || !strings.Contains(tail, escClear) {
				t.Errorf("popup not cleared or cursor not restored: %q", tail)
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
