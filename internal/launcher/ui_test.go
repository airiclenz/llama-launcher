package launcher

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

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
