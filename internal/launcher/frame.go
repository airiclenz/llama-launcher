package launcher

import (
	"os"
	"strings"

	"golang.org/x/term"
)

type Frame struct {
	Title   string
	Padding int
	RawMode bool
}

func (f Frame) Render(lines []string) string {
	pad := f.Padding
	if pad == 0 {
		pad = 1
	}

	maxContent := 0
	for _, line := range lines {
		if w := visibleWidth(line); w > maxContent {
			maxContent = w
		}
	}

	titleVis := visibleWidth(f.Title)
	if f.Title != "" {
		titleNeeded := titleVis + 4
		if titleNeeded > maxContent {
			maxContent = titleNeeded
		}
	}

	innerWidth := maxContent + pad*2
	outerWidth := innerWidth + 2

	if tw := terminalWidth(); outerWidth > tw {
		outerWidth = tw
		innerWidth = outerWidth - 2
	}

	nl := "\n"
	if f.RawMode {
		nl = "\r\n"
	}

	var buf strings.Builder
	buf.Grow(outerWidth * (len(lines) + 2))

	// top border
	buf.WriteString("╭")
	if f.Title != "" {
		buf.WriteString("━ ")
		buf.WriteString(f.Title)
		buf.WriteString(" ")
		remaining := innerWidth - titleVis - 3
		if remaining > 0 {
			buf.WriteString(strings.Repeat("━", remaining))
		}
	} else {
		buf.WriteString(strings.Repeat("━", innerWidth))
	}
	buf.WriteString("╮")
	buf.WriteString(nl)

	// content lines
	padding := strings.Repeat(" ", pad)
	for _, line := range lines {
		buf.WriteString("│")
		buf.WriteString(padding)
		vis := visibleWidth(line)
		fill := innerWidth - pad*2 - vis
		buf.WriteString(line)
		if fill > 0 {
			buf.WriteString(strings.Repeat(" ", fill))
		}
		buf.WriteString(padding)
		buf.WriteString("│")
		buf.WriteString(nl)
	}

	// bottom border
	buf.WriteString("╰")
	buf.WriteString(strings.Repeat("━", innerWidth))
	buf.WriteString("╯")
	buf.WriteString(nl)

	return buf.String()
}

func visibleWidth(s string) int {
	w := 0
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
		w++
	}
	return w
}

func terminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 60
	}
	return w
}
