package launcher

import (
	"strings"
	"testing"
)

func TestFrameBasic(t *testing.T) {
	f := Frame{Title: "llama-launcher"}
	out := f.Render([]string{"hello", "world"})

	if !strings.Contains(out, "llama-launcher") {
		t.Errorf("missing title in top border:\n%s", out)
	}
	if !strings.Contains(out, "╰") || !strings.Contains(out, "╯") {
		t.Errorf("missing bottom border:\n%s", out)
	}

	if !strings.Contains(out, "╍") {
		t.Errorf("missing title decoration line:\n%s", out)
	}

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 5 {
		t.Errorf("expected 5 lines (top + decoration + 2 body + bottom), got %d:\n%s", len(lines), out)
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

	if !strings.HasPrefix(out, cDarkGray+"╭━") {
		t.Errorf("expected dark gray top border:\n%s", out)
	}
}

func TestFrameRawMode(t *testing.T) {
	f := Frame{RawMode: true}
	out := f.Render([]string{"test"})

	if !strings.Contains(out, "\r\n") {
		t.Errorf("raw mode should use \\r\\n line endings")
	}
}

func TestFrameHeaderFooter(t *testing.T) {
	f := Frame{
		Title:  "test",
		Header: []string{"Status: ok"},
		Footer: []string{"press q to quit"},
	}
	out := f.Render([]string{"body line"})

	if !strings.Contains(out, "├") || !strings.Contains(out, "┤") {
		t.Errorf("missing divider:\n%s", out)
	}

	dividerCount := strings.Count(out, "├")
	if dividerCount != 2 {
		t.Errorf("expected 2 dividers (header + footer), got %d:\n%s", dividerCount, out)
	}

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	topVis := visibleWidth(lines[0])
	for i, line := range lines {
		if vis := visibleWidth(line); vis != topVis {
			t.Errorf("line %d visible width %d != top width %d:\n%s", i, vis, topVis, out)
		}
	}
}

func TestFrameHeaderOnly(t *testing.T) {
	f := Frame{Header: []string{"header"}}
	out := f.Render([]string{"body"})

	if strings.Count(out, "├") != 1 {
		t.Errorf("expected 1 divider (header only):\n%s", out)
	}
}

func TestFrameFooterOnly(t *testing.T) {
	f := Frame{Footer: []string{"footer"}}
	out := f.Render([]string{"body"})

	if strings.Count(out, "├") != 1 {
		t.Errorf("expected 1 divider (footer only):\n%s", out)
	}
}

func TestFrameANSIContent(t *testing.T) {
	f := Frame{
		Title:  "status",
		Header: []string{cGreen + "● running" + cReset},
		Footer: []string{cDim + "hints" + cReset},
	}
	out := f.Render([]string{"plain text"})

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	topVis := visibleWidth(lines[0])
	for i, line := range lines {
		if vis := visibleWidth(line); vis != topVis {
			t.Errorf("line %d visible width %d != top width %d:\n%s", i, vis, topVis, out)
		}
	}
}

func TestFrameCustomPadding(t *testing.T) {
	f := Frame{Padding: 3}
	out := f.Render([]string{"x"})

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	content := lines[1]
	if !strings.Contains(content, "│"+cReset+"   x") {
		t.Errorf("expected 3-space left padding, got: %q", content)
	}
}

func TestFrameDarkGrayBorders(t *testing.T) {
	f := Frame{Title: "test"}
	out := f.Render([]string{"content"})

	if !strings.HasPrefix(out, cDarkGray) {
		t.Errorf("border should start with dark gray color code")
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
