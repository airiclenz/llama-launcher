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
