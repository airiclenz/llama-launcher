package launcher

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// ProgressFunc reports lifecycle step transitions to the UI layer.
type ProgressFunc func(step string)

func reportStep(fn ProgressFunc, step string) {
	if fn != nil {
		fn(step)
	}
}

// NoticeFunc delivers user-facing notices — config warnings, the ADR-0007
// drift notice — to the UI layer; nil discards.
type NoticeFunc func(notice string)

func reportNotice(fn NoticeFunc, notice string) {
	if fn != nil {
		fn(notice)
	}
}

// progressTickInterval is how often an open TUI progress popup redraws so
// the elapsed time on its active step keeps ticking.
const progressTickInterval = time.Second

// progressTracker is the TUI progress popup. It redraws in place on every
// step and, between steps, once per tick so the active step's elapsed time
// stays current. The ticker goroutine runs until Close.
type progressTracker struct {
	title string
	out   io.Writer
	now   func() time.Time

	mu         sync.Mutex
	steps      []string
	stepStart  time.Time
	closed     bool
	prevRows   int
	prevWidth  int
	startRow   int
	startCol   int
	stopTicker chan struct{}
	tickerDone chan struct{}
	closeOnce  sync.Once
}

func newTUIProgress(title string) (*progressTracker, ProgressFunc) {
	return newProgressTracker(title, os.Stdout, time.Now, progressTickInterval)
}

// newProgressTracker builds a tracker drawing to out, reading time from now
// and redrawing every interval. It starts the ticker goroutine; the caller
// must Close the tracker before it draws anything else over the popup.
func newProgressTracker(title string, out io.Writer, now func() time.Time, interval time.Duration) (*progressTracker, ProgressFunc) {
	t := &progressTracker{
		title:      title,
		out:        out,
		now:        now,
		stopTicker: make(chan struct{}),
		tickerDone: make(chan struct{}),
	}
	go t.tick(interval)

	fn := func(step string) {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.steps = append(t.steps, step)
		t.stepStart = t.now()
		if !t.closed {
			t.render()
		}
	}
	return t, fn
}

func (t *progressTracker) tick(interval time.Duration) {
	defer close(t.tickerDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopTicker:
			return
		case <-ticker.C:
			t.mu.Lock()
			if !t.closed && len(t.steps) > 0 {
				t.render()
			}
			t.mu.Unlock()
		}
	}
}

// Close stops the ticker and waits for its goroutine to exit; the popup is
// never redrawn afterwards, not even by a late step. Safe to call twice.
func (t *progressTracker) Close() {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		close(t.stopTicker)
		<-t.tickerDone
	})
}

// formatElapsed renders d as m:ss.
func formatElapsed(d time.Duration) string {
	secs := int(d / time.Second)
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

func newCLIProgress(title string) ProgressFunc {
	fmt.Printf("  %s\n", title)
	return func(step string) {
		fmt.Printf("    %s...\n", step)
	}
}

// printStopSteps renders the steps a completed Stop/Unload took, in the
// CLI progress style. The unified stop/unload entry points return their
// steps in a StopResult instead of streaming them through a callback, so
// the CLI prints them after the fact.
func printStopSteps(title string, steps []string) {
	fmt.Printf("  %s\n", title)
	for _, step := range steps {
		fmt.Printf("    %s...\n", step)
	}
}

// render draws the popup. The caller holds t.mu.
func (t *progressTracker) render() {
	body := []string{""}
	body = append(body, fmt.Sprintf("%s%s...%s", cBoldLightGray, t.title, cReset))
	body = append(body, "")

	for i, step := range t.steps {
		if i < len(t.steps)-1 {
			body = append(body, fmt.Sprintf("%s%s%s", cDim, step, cReset))
		} else {
			line := fmt.Sprintf("%s▸ %s...%s", cBoldCyan, step, cReset)
			if elapsed := t.now().Sub(t.stepStart); elapsed >= time.Second {
				line += fmt.Sprintf(" %s%s%s", cDim, formatElapsed(elapsed), cReset)
			}
			body = append(body, line)
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

	// A popup that lost rows or columns no longer covers the previous
	// rect, so blank that rect first or its old border cells stay behind.
	if t.startRow > 0 && (t.prevRows > len(popupLines) || t.prevWidth > popupWidth) {
		blank := strings.Repeat(" ", t.prevWidth)
		for r := t.startRow; r < t.startRow+t.prevRows; r++ {
			fmt.Fprintf(&buf, "\033[%d;%dH%s", r, t.startCol, blank)
		}
	}

	for i, line := range popupLines {
		fmt.Fprintf(&buf, "\033[%d;%dH%s", startRow+i, startCol, line)
	}
	io.WriteString(t.out, buf.String())

	t.prevRows = len(popupLines)
	t.prevWidth = popupWidth
	t.startRow = startRow
	t.startCol = startCol
}
