package launcher

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

const (
	escClear      = "\033[H\033[J"
	escCursorHide = "\033[?25l"
	escCursorShow = "\033[?25h"

	cReset = "\033[0m"
	cDim   = "\033[2m"
	cGreen = "\033[32m"
	cRed   = "\033[31m"
	cBoldLightGray = "\033[1;37m"
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

var lastMenuRect struct {
	row, col, width, height int
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

func readKeyTimeout(timeout time.Duration) keyCode {
	tv := syscall.NsecToTimeval(int64(timeout))
	var fds syscall.FdSet
	fds.Bits[0] = 1
	if err := syscall.Select(1, &fds, nil, nil, &tv); err != nil {
		return keyNone
	}
	if fds.Bits[0]&1 == 0 {
		return keyNone
	}
	return readKey()
}

func selectMenu(title string, headerFn func() []string, items []menuItem, hints string, centered bool) int {
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

	labelWidth := 0
	for _, item := range items {
		if !item.Separator && len(item.Label) > labelWidth {
			labelWidth = len(item.Label)
		}
	}
	labelWidth += 2

	var buf strings.Builder
	for {
		if headerFn != nil {
			frame.Header = headerFn()
		}

		var body []string

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

		rendered := frame.Render(body)
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

		buf.Reset()
		buf.WriteString(escClear)
		buf.WriteString(escCursorHide)

		for i, line := range renderedLines {
			fmt.Fprintf(&buf, "\033[%d;%dH%s", startRow+i, startCol, line)
		}

		os.Stdout.WriteString(buf.String())

		key := readKeyTimeout(10 * time.Second)
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

	body := append([]string{}, lines...)
	body = append(body, "", fmt.Sprintf("%spress any key to close%s", cDim, cReset))

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
	os.Stdout.WriteString(buf.String())

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return
	}
	readKey()
	term.Restore(fd, oldState)
}
