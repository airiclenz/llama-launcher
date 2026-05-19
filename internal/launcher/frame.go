package launcher

import (
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	cDarkGray  = "\033[90m"
	cLightGray = "\033[37m"
)

type Frame struct {
	Title       string
	Header      []string
	Footer      []string
	Padding     int
	RawMode     bool
	BorderColor string
}

func (f Frame) Render(body []string) string {
	pad := f.Padding
	if pad == 0 {
		pad = 1
	}
	rightPad := pad + 1

	maxContent := 0
	for _, section := range [][]string{f.Header, body, f.Footer} {
		for _, line := range section {
			if w := visibleWidth(line); w > maxContent {
				maxContent = w
			}
		}
	}

	titleVis := visibleWidth(f.Title)
	if f.Title != "" {
		if needed := titleVis + 4; needed > maxContent {
			maxContent = needed
		}
	}

	innerWidth := maxContent + pad + rightPad
	outerWidth := innerWidth + 2

	if tw := terminalWidth(); outerWidth > tw {
		outerWidth = tw
		innerWidth = outerWidth - 2
	}

	nl := "\n"
	if f.RawMode {
		nl = "\r\n"
	}

	bc := f.BorderColor
	if bc == "" {
		bc = cDarkGray
	}

	var buf strings.Builder
	buf.Grow(outerWidth * (len(f.Header) + len(body) + len(f.Footer) + 4))

	leftSpace := strings.Repeat(" ", pad)
	rightSpace := strings.Repeat(" ", rightPad)
	hBar := strings.Repeat("━", innerWidth)

	writeLine := func(line string) {
		buf.WriteString(bc + "│" + cReset)
		buf.WriteString(leftSpace)
		fill := innerWidth - pad - rightPad - visibleWidth(line)
		buf.WriteString(line)
		if fill > 0 {
			buf.WriteString(strings.Repeat(" ", fill))
		}
		buf.WriteString(rightSpace)
		buf.WriteString(bc + "│" + cReset)
		buf.WriteString(nl)
	}

	// top border
	buf.WriteString(bc + "╭")
	if f.Title != "" {
		buf.WriteString("━ " + cReset + f.Title + bc + " ")
		if remaining := innerWidth - titleVis - 3; remaining > 0 {
			buf.WriteString(strings.Repeat("━", remaining))
		}
	} else {
		buf.WriteString(hBar)
	}
	buf.WriteString("╮" + cReset)
	buf.WriteString(nl)

	// title decoration
	if f.Title != "" {
		buf.WriteString(bc + "│" + cReset)
		buf.WriteString(leftSpace)
		//bar := strings.Repeat("▬", innerWidth-pad-rightPad)
		bar := strings.Repeat("╍", innerWidth-pad-rightPad+1)
		buf.WriteString(bc + bar + cReset)
		buf.WriteString(bc + " │" + cReset)
		buf.WriteString(nl)
	}

	// header
	if len(f.Header) > 0 {
		for _, line := range f.Header {
			writeLine(line)
		}
		buf.WriteString(bc + "├" + hBar + "┤" + cReset)
		buf.WriteString(nl)
	}

	// body
	for _, line := range body {
		writeLine(line)
	}

	// footer
	if len(f.Footer) > 0 {
		buf.WriteString(bc + "├" + hBar + "┤" + cReset)
		buf.WriteString(nl)
		for _, line := range f.Footer {
			writeLine(line)
		}
	}

	// bottom border
	buf.WriteString(bc + "╰" + hBar + "╯" + cReset)
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

func terminalHeight() int {
	_, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || h <= 0 {
		return 24
	}
	return h
}
