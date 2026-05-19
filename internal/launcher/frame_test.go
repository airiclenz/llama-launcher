package launcher

import (
	"strings"
	"testing"
)

func TestFrameBasic(t *testing.T) {
	f := Frame{Title: "llama-launcher"}
	out := f.Render([]string{"hello", "world"})

	if !strings.Contains(out, "╭━ llama-launcher") {
		t.Errorf("missing title in top border:\n%s", out)
	}
	if !strings.Contains(out, "╰") || !strings.Contains(out, "╯") {
		t.Errorf("missing bottom border:\n%s", out)
	}

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Errorf("expected 4 lines (top + 2 content + bottom), got %d:\n%s", len(lines), out)
	}

	topVis := visibleWidth(lines[0])
	botVis := visibleWidth(lines[len(lines)-1])
	if topVis != botVis {
		t.Errorf("top width %d != bottom width %d", topVis, botVis)
	}
}

func TestFrameNoTitle(t *testing.T) {
	f := Frame{}
	out := f.Render([]string{"content"})

	if !strings.HasPrefix(out, "╭━") {
		t.Errorf("expected top border to start with ╭━:\n%s", out)
	}
	if strings.Contains(out, "╭━ ") {
		t.Errorf("no-title frame should not have title spacing:\n%s", out)
	}
}

func TestFrameRawMode(t *testing.T) {
	f := Frame{RawMode: true}
	out := f.Render([]string{"test"})

	if !strings.Contains(out, "\r\n") {
		t.Errorf("raw mode should use \\r\\n line endings")
	}
}

func TestFrameANSIContent(t *testing.T) {
	colored := cGreen + "● running" + cReset
	f := Frame{Title: "status"}
	out := f.Render([]string{colored, "plain text"})

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	topVis := visibleWidth(lines[0])
	for i, line := range lines {
		vis := visibleWidth(line)
		if vis != topVis {
			t.Errorf("line %d visible width %d != top width %d:\n%s", i, vis, topVis, out)
		}
	}
}

func TestFrameCustomPadding(t *testing.T) {
	f := Frame{Padding: 3}
	out := f.Render([]string{"x"})

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	content := lines[1]
	if !strings.HasPrefix(content, "│   ") {
		t.Errorf("expected 3-space padding, got: %q", content)
	}
}

func TestVisibleWidth(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"hello", 5},
		{"\033[31mred\033[0m", 3},
		{"\033[1;36mbold cyan\033[0m", 9},
		{"", 0},
		{"no escapes", 10},
	}
	for _, tt := range tests {
		got := visibleWidth(tt.input)
		if got != tt.want {
			t.Errorf("visibleWidth(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
