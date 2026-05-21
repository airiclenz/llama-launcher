package launcher

import (
	"fmt"
	"os"
	"strings"
)

// ProgressFunc reports lifecycle step transitions to the UI layer.
type ProgressFunc func(step string)

func reportStep(fn ProgressFunc, step string) {
	if fn != nil {
		fn(step)
	}
}

type progressTracker struct {
	title    string
	steps    []string
	prevRows int
	startRow int
	startCol int
}

func newTUIProgress(title string) (*progressTracker, ProgressFunc) {
	t := &progressTracker{title: title}
	fn := func(step string) {
		t.steps = append(t.steps, step)
		t.render()
	}
	return t, fn
}

func newCLIProgress(title string) ProgressFunc {
	fmt.Printf("  %s\n", title)
	return func(step string) {
		fmt.Printf("    %s...\n", step)
	}
}

func (t *progressTracker) render() {
	body := []string{""}
	body = append(body, fmt.Sprintf("%s%s...%s", cBoldLightGray, t.title, cReset))
	body = append(body, "")

	for i, step := range t.steps {
		if i < len(t.steps)-1 {
			body = append(body, fmt.Sprintf("%s%s%s", cDim, step, cReset))
		} else {
			body = append(body, fmt.Sprintf("%s▸ %s...%s", cBoldCyan, step, cReset))
		}
	}

	body = append(body, "")

	f := Frame{Padding: 3, BorderColor: cLightGray}
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

	if t.prevRows > len(popupLines) && t.startRow > 0 {
		blankWidth := terminalWidth()
		blank := strings.Repeat(" ", blankWidth)
		for r := t.startRow; r < t.startRow+t.prevRows; r++ {
			fmt.Fprintf(&buf, "\033[%d;1H%s", r, blank)
		}
	}

	for i, line := range popupLines {
		fmt.Fprintf(&buf, "\033[%d;%dH%s", startRow+i, startCol, line)
	}
	os.Stdout.WriteString(buf.String())

	t.prevRows = len(popupLines)
	t.startRow = startRow
	t.startCol = startCol
}
