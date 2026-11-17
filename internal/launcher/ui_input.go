package launcher

import (
	"bytes"
	"os"
	"strconv"
	"time"
)

// inputEvent is one decoded unit of raw-mode stdin: a key, or a mouse report.
// x and y are the 1-based terminal column and row of a keyClick or a
// keyHover; every other event leaves them zero.
type inputEvent struct {
	key  keyCode
	x, y int
}

const (
	byteEscape    = 0x1b
	byteCtrlC     = 0x03
	byteLineFeed  = '\n'
	byteReturn    = '\r'
	csiIntroducer = '['
	ss3Introducer = 'O'
	sgrMouseMark  = '<'

	// csiFinalMin and csiFinalMax bound the byte that ends a CSI sequence;
	// csiBodyMin is the lowest byte a sequence body (parameters and
	// intermediates) may hold. A byte below it ends the sequence early, so a
	// truncated sequence never swallows a following Enter or Ctrl+C.
	csiFinalMin = 0x40
	csiFinalMax = 0x7e
	csiBodyMin  = 0x20

	// maxEscapeSequenceLength caps how far a CSI sequence is scanned for its
	// final byte. A longer run is consumed whole as keyNone, so a stray
	// prefix cannot hold input back indefinitely; the longest SGR mouse
	// report a terminal sends is well under it.
	maxEscapeSequenceLength = 32

	sgrButtonLeft      = 0
	sgrMotionBit       = 32 // set when the report is for pointer motion, a button held or not
	sgrButtonWheelUp   = 64
	sgrButtonWheelDown = 65
	sgrPress           = 'M'
	sgrParameterCount  = 3

	// stdinReadSize is how many bytes one stdin read takes: enough for
	// several mouse reports arriving together.
	stdinReadSize = 64
)

// pendingInput holds stdin bytes read but not yet decoded: the rest of a read
// that carried several events, or the prefix of a sequence split across
// reads. Only the UI goroutine reads stdin, so it needs no lock.
var pendingInput []byte

// decodeInput decodes the first event in buf. consumed is how many bytes of
// buf the event spans; complete is false when buf ends inside an escape
// sequence (consumed is then 0), so the caller must read more bytes before
// decoding again. A lone ESC ending buf is a complete keyEscape. Every other
// escape sequence is consumed whole: an SGR mouse report decodes to keyClick,
// keyHover, keyWheelUp, keyWheelDown or keyMouseIgnored, the arrow keys to
// keyUp and keyDown, and anything else to keyNone. An empty buf is incomplete.
func decodeInput(buf []byte) (ev inputEvent, consumed int, complete bool) {
	if len(buf) == 0 {
		return inputEvent{}, 0, false
	}
	if buf[0] != byteEscape {
		return inputEvent{key: decodeSingleByte(buf[0])}, 1, true
	}
	if len(buf) == 1 {
		return inputEvent{key: keyEscape}, 1, true
	}

	switch buf[1] {
	case csiIntroducer:
		return decodeCSI(buf)
	case ss3Introducer:
		// SS3 (ESC O <byte>) carries one final byte, e.g. an application-mode arrow.
		if len(buf) < 3 {
			return inputEvent{}, 0, false
		}
		return inputEvent{key: keyNone}, 3, true
	default:
		return inputEvent{key: keyNone}, 2, true
	}
}

// decodeSingleByte maps one non-escape byte to its key: Enter, Ctrl+C, q, a
// digit 1-9 (as its own character code), or keyNone.
func decodeSingleByte(b byte) keyCode {
	switch b {
	case byteReturn, byteLineFeed:
		return keyEnter
	case byteCtrlC:
		return keyCtrlC
	case 'q', 'Q':
		return keyQ
	}
	if b >= '1' && b <= '9' {
		return keyCode(b)
	}
	return keyNone
}

// decodeCSI decodes the CSI sequence (ESC [ ... final) at the start of buf.
// An SGR mouse report (ESC [ < ...) goes to decodeSGRMouse; ESC [ A and
// ESC [ B are the arrows; every other sequence is consumed through its final
// byte and decodes to keyNone.
func decodeCSI(buf []byte) (inputEvent, int, bool) {
	const bodyStart = 2
	end := bodyStart
	for ; end < len(buf) && end < maxEscapeSequenceLength; end++ {
		b := buf[end]
		if b >= csiFinalMin && b <= csiFinalMax {
			break
		}
		if b < csiBodyMin {
			// Not a sequence byte: the sequence was cut short, so drop
			// what came before and leave this byte to decode as a key.
			return inputEvent{key: keyNone}, end, true
		}
	}
	if end == maxEscapeSequenceLength {
		return inputEvent{key: keyNone}, end, true
	}
	if end == len(buf) {
		return inputEvent{}, 0, false
	}

	consumed := end + 1
	body := buf[bodyStart:end]
	if len(body) > 0 && body[0] == sgrMouseMark {
		return decodeSGRMouse(body[1:], buf[end]), consumed, true
	}
	if len(body) == 0 {
		switch buf[end] {
		case 'A':
			return inputEvent{key: keyUp}, consumed, true
		case 'B':
			return inputEvent{key: keyDown}, consumed, true
		}
	}
	return inputEvent{key: keyNone}, consumed, true
}

// decodeSGRMouse decodes the parameters ("button;x;y") and final byte of an
// SGR mouse report. A report with the motion bit set is keyHover at x,y,
// whether or not a button is held; a left-button press is keyClick at x,y;
// button 64 and 65 presses are the wheel; a release, any other button or a
// malformed report is keyMouseIgnored.
func decodeSGRMouse(params []byte, final byte) inputEvent {
	ignored := inputEvent{key: keyMouseIgnored}
	if final != sgrPress {
		return ignored
	}

	fields := bytes.Split(params, []byte{';'})
	if len(fields) != sgrParameterCount {
		return ignored
	}
	values := make([]int, 0, sgrParameterCount)
	for _, field := range fields {
		value, err := strconv.Atoi(string(field))
		if err != nil {
			return ignored
		}
		values = append(values, value)
	}

	if values[0]&sgrMotionBit != 0 {
		return inputEvent{key: keyHover, x: values[1], y: values[2]}
	}

	switch values[0] {
	case sgrButtonLeft:
		return inputEvent{key: keyClick, x: values[1], y: values[2]}
	case sgrButtonWheelUp:
		return inputEvent{key: keyWheelUp}
	case sgrButtonWheelDown:
		return inputEvent{key: keyWheelDown}
	}
	return ignored
}

// takePendingEvent decodes and removes the first complete event in
// pendingInput. It reports false when pendingInput is empty or holds only the
// prefix of a split sequence.
func takePendingEvent() (inputEvent, bool) {
	ev, consumed, complete := decodeInput(pendingInput)
	if !complete {
		return inputEvent{}, false
	}
	pendingInput = pendingInput[consumed:]
	return ev, true
}

// readStdinOnce blocks for one stdin read and appends what arrived to
// pendingInput. It reports false when the read fails or returns nothing.
func readStdinOnce() bool {
	buf := make([]byte, stdinReadSize)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return false
	}
	pendingInput = append(pendingInput, buf[:n]...)
	return true
}

// readInput returns the next input event, blocking until one is complete. A
// pending event from an earlier read is returned without touching stdin; a
// sequence split across reads is reassembled. A failed read returns keyNone.
// Stdin must already be in raw mode.
func readInput() inputEvent {
	for {
		if ev, ok := takePendingEvent(); ok {
			return ev
		}
		if !readStdinOnce() {
			return inputEvent{key: keyNone}
		}
	}
}

// readInputTimeout returns the next input event, or keyNone when none is
// complete within timeout. A complete pending event returns at once without a
// poll; an incomplete prefix is kept and stdin polled for the rest, and one
// still incomplete after the read returns keyNone, to be finished on a later
// call. The poll is per-OS (ui_poll_darwin.go and its siblings) — the one
// place the menu touches a platform primitive.
func readInputTimeout(timeout time.Duration) inputEvent {
	if ev, ok := takePendingEvent(); ok {
		return ev
	}
	if !pollStdin(timeout) || !readStdinOnce() {
		return inputEvent{key: keyNone}
	}
	if ev, ok := takePendingEvent(); ok {
		return ev
	}
	return inputEvent{key: keyNone}
}
