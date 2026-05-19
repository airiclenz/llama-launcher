package launcher

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	escClear      = "\033[2J\033[H"
	escCursorHide = "\033[?25l"
	escCursorShow = "\033[?25h"

	cReset    = "\033[0m"
	cBold     = "\033[1m"
	cDim      = "\033[2m"
	cCyan     = "\033[36m"
	cGreen    = "\033[32m"
	cRed      = "\033[31m"
	cYellow   = "\033[33m"
	cBoldCyan = "\033[1;36m"
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
)

type menuItem struct {
	Label       string
	Description string
	Separator   bool
}

func readKey() keyCode {
	buf := make([]byte, 4)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return keyNone
	}

	if n == 1 {
		switch buf[0] {
		case 13, 10:
			return keyEnter
		case 27:
			return keyEscape
		case 3:
			return keyCtrlC
		case 'q', 'Q':
			return keyQ
		}
		if buf[0] >= '1' && buf[0] <= '9' {
			return keyCode(buf[0])
		}
		return keyNone
	}

	if n >= 3 && buf[0] == 27 && buf[1] == '[' {
		switch buf[2] {
		case 'A':
			return keyUp
		case 'B':
			return keyDown
		}
	}

	return keyNone
}

func selectMenu(title string, headerLines []string, items []menuItem, hints string) int {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return -1
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return -1
	}
	defer func() {
		term.Restore(fd, oldState)
		fmt.Print(escCursorShow)
	}()

	selected := firstSelectable(items)
	if selected < 0 {
		return -1
	}

	frame := Frame{Title: title, RawMode: true}

	var buf strings.Builder
	for {
		var lines []string
		lines = append(lines, headerLines...)

		for i, item := range items {
			if item.Separator {
				lines = append(lines, "")
				continue
			}
			if i == selected {
				lines = append(lines, fmt.Sprintf("%s▸ %-22s%s %s", cBoldCyan, item.Label, cReset, item.Description))
			} else {
				lines = append(lines, fmt.Sprintf("  %-22s %s%s%s", item.Label, cDim, item.Description, cReset))
			}
		}

		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("%s%s%s", cDim, hints, cReset))

		buf.Reset()
		buf.WriteString(escClear)
		buf.WriteString(escCursorHide)
		buf.WriteString("\r\n")
		buf.WriteString(frame.Render(lines))

		os.Stdout.WriteString(buf.String())

		key := readKey()
		switch key {
		case keyUp:
			for next := selected - 1; next >= 0; next-- {
				if !items[next].Separator {
					selected = next
					break
				}
			}
		case keyDown:
			for next := selected + 1; next < len(items); next++ {
				if !items[next].Separator {
					selected = next
					break
				}
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
