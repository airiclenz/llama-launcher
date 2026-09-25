package launcher

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer is a bytes.Buffer safe for the tracker's ticker goroutine and
// the test goroutine to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// fakeClock is a settable clock for the tracker's now seam.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// cursorMove matches the cursor-positioning sequence render writes before
// each popup row, so splitting on it yields one popup row per element.
var cursorMove = regexp.MustCompile(`\x1b\[\d+;\d+H`)

// renderNow draws the tracker synchronously, as a tick would.
func renderNow(tr *progressTracker) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.render()
}

func TestProgressTracker_ElapsedOnActiveStep(t *testing.T) {
	t.Parallel()

	out := &syncBuffer{}
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	// An hour-long interval keeps the ticker out of the way.
	tr, progress := newProgressTracker("Loading big", out, clock.Now, time.Hour)
	defer tr.Close()

	progress("Starting server")
	clock.Advance(5 * time.Second)
	progress("Waiting for server")
	if strings.Contains(out.String(), "0:05") {
		t.Errorf("finished step shows an elapsed time:\n%q", out.String())
	}

	clock.Advance(67 * time.Second)
	out.Reset()
	renderNow(tr)
	rendered := out.String()

	var activeLine, doneLine string
	for _, line := range cursorMove.Split(rendered, -1) {
		if strings.Contains(line, "Waiting for server") {
			activeLine = line
		}
		if strings.Contains(line, "Starting server") {
			doneLine = line
		}
	}
	if !strings.Contains(activeLine, "Waiting for server...") || !strings.Contains(activeLine, "1:07") {
		t.Errorf("active step lacks the 1:07 elapsed time: %q", activeLine)
	}
	if strings.Contains(doneLine, ":0") || strings.Contains(doneLine, "1:") {
		t.Errorf("finished step carries an elapsed time: %q", doneLine)
	}
}

func TestProgressTracker_NoElapsedUnderOneSecond(t *testing.T) {
	t.Parallel()

	out := &syncBuffer{}
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	tr, progress := newProgressTracker("Loading big", out, clock.Now, time.Hour)
	defer tr.Close()

	progress("Waiting for server")
	if strings.Contains(out.String(), "0:00") {
		t.Errorf("fresh step shows 0:00:\n%q", out.String())
	}
}

func TestProgressTracker_CloseStopsTicker(t *testing.T) {
	t.Parallel()

	out := &syncBuffer{}
	tr, progress := newProgressTracker("Loading big", out, time.Now, 2*time.Millisecond)
	progress("Waiting for server")

	deadline := time.Now().Add(2 * time.Second)
	first := len(out.String())
	for len(out.String()) == first && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(out.String()) == first {
		t.Fatal("ticker never redrew the popup")
	}

	tr.Close()
	tr.Close() // idempotent
	closedLen := len(out.String())

	progress("Late step")
	time.Sleep(20 * time.Millisecond)
	if got := len(out.String()); got != closedLen {
		t.Errorf("popup redrawn after Close: %d bytes written, want %d", got, closedLen)
	}
}

func TestProgressTracker_NarrowerPopupBlanksOldCells(t *testing.T) {
	t.Parallel()

	out := &syncBuffer{}
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	tr, progress := newProgressTracker("Go", out, clock.Now, time.Hour)
	defer tr.Close()

	progress("Waiting for server")
	clock.Advance(3 * time.Second)
	renderNow(tr)
	if !strings.Contains(out.String(), "0:03") {
		t.Fatalf("active step lacks 0:03:\n%q", out.String())
	}

	tr.mu.Lock()
	prevRow, prevCol, prevWidth, prevRows := tr.startRow, tr.startCol, tr.prevWidth, tr.prevRows
	tr.mu.Unlock()

	out.Reset()
	progress("Go") // the old active line drops its suffix; the new one is shorter
	rendered := out.String()

	tr.mu.Lock()
	newWidth := tr.prevWidth
	tr.mu.Unlock()
	if newWidth >= prevWidth {
		t.Fatalf("popup did not narrow: width %d, previous %d", newWidth, prevWidth)
	}

	blank := strings.Repeat(" ", prevWidth)
	for r := prevRow; r < prevRow+prevRows; r++ {
		want := fmt.Sprintf("\033[%d;%dH%s", r, prevCol, blank)
		idx := strings.Index(rendered, want)
		if idx < 0 {
			t.Fatalf("row %d of the previous rect not blanked:\n%q", r, rendered)
		}
		if first := strings.Index(rendered, "╭"); first >= 0 && idx > first {
			t.Errorf("row %d blanked after the popup was drawn", r)
		}
	}
}
