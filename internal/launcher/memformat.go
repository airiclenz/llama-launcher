package launcher

import (
	"fmt"
	"strconv"
	"strings"
)

// memField identifies a live MemStats value referenced by a template token.
type memField int

const (
	fieldFreeRAM memField = iota
	fieldUsedRAM
	fieldTotalRAM
	fieldCompressedRAM
	fieldSwapUsed
	fieldSwapTotal
	fieldFreeSwap
	fieldFreeRAMPct
	fieldUsedRAMPct
	fieldSwapUsedPct
	fieldGPUUtilPct
	fieldGPUUsedRAM
	fieldGPUAllocRAM
)

// memFields maps template placeholder names to their MemStats field.
var memFields = map[string]memField{
	"free_ram":       fieldFreeRAM,
	"used_ram":       fieldUsedRAM,
	"total_ram":      fieldTotalRAM,
	"compressed_ram": fieldCompressedRAM,
	"swap_used":      fieldSwapUsed,
	"swap_total":     fieldSwapTotal,
	"free_swap":      fieldFreeSwap,
	"free_ram_pct":   fieldFreeRAMPct,
	"used_ram_pct":   fieldUsedRAMPct,
	"swap_used_pct":  fieldSwapUsedPct,
	"gpu_util_pct":   fieldGPUUtilPct,
	"gpu_used_ram":   fieldGPUUsedRAM,
	"gpu_alloc_ram":  fieldGPUAllocRAM,
}

// isPct reports whether the field is a 0–100 percentage (and therefore
// renderable as a bar graph).
func (f memField) isPct() bool {
	switch f {
	case fieldFreeRAMPct, fieldUsedRAMPct, fieldSwapUsedPct, fieldGPUUtilPct:
		return true
	}
	return false
}

// memColorCodes maps template color names to their SGR foreground number.
// This is the template vocabulary for both inline style tags and bar colors;
// the launcher chrome palette in ui.go is separate on purpose.
var memColorCodes = map[string]int{
	"black":          30,
	"red":            31,
	"green":          32,
	"yellow":         33,
	"blue":           34,
	"magenta":        35,
	"cyan":           36,
	"white":          37,
	"gray":           90,
	"bright-black":   90,
	"bright-red":     91,
	"bright-green":   92,
	"bright-yellow":  93,
	"bright-blue":    94,
	"bright-magenta": 95,
	"bright-cyan":    96,
	"bright-white":   97,
}

// memColor resolves a color spec to its foreground and background escapes.
// Three forms are accepted:
//
//	named    — the 16 ANSI color names (rendered per the terminal's theme)
//	0–255    — a 256-color palette index, e.g. "240"
//	#rrggbb  — an exact 24-bit color, e.g. "#7aa2f7" (also short "#rgb")
//
// Numeric and hex colors render the same regardless of terminal theme.
func memColor(spec string) (fg, bg string, ok bool) {
	if n, found := memColorCodes[spec]; found {
		return fmt.Sprintf("\033[%dm", n), fmt.Sprintf("\033[%dm", n+10), true
	}
	if n, err := strconv.Atoi(spec); err == nil && n >= 0 && n <= 255 {
		return fmt.Sprintf("\033[38;5;%dm", n), fmt.Sprintf("\033[48;5;%dm", n), true
	}
	if r, g, b, found := parseHexColor(spec); found {
		return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b), fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b), true
	}
	return "", "", false
}

// parseHexColor parses "#rrggbb" or "#rgb" into 8-bit RGB components.
func parseHexColor(spec string) (r, g, b int, ok bool) {
	if len(spec) == 0 || spec[0] != '#' {
		return 0, 0, 0, false
	}
	hex := spec[1:]
	switch len(hex) {
	case 3:
		v, err := strconv.ParseUint(hex, 16, 16)
		if err != nil {
			return 0, 0, 0, false
		}
		r, g, b = int(v>>8&0xf), int(v>>4&0xf), int(v&0xf)
		return r * 17, g * 17, b * 17, true
	case 6:
		v, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			return 0, 0, 0, false
		}
		return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff), true
	}
	return 0, 0, 0, false
}

// memStyleEscape resolves a style tag (a color spec, bold, dim, or reset)
// to its ANSI escape.
func memStyleEscape(name string) (string, bool) {
	switch name {
	case "bold":
		return "\033[1m", true
	case "dim":
		return cDim, true
	case "reset":
		return cReset, true
	}
	fg, _, ok := memColor(name)
	return fg, ok
}

// Bar geometry limits. Width is in terminal cells; eighth-block glyphs give
// eight fill levels per cell.
const (
	defaultBarWidth = 10
	minBarWidth     = 1
	maxBarWidth     = 40
)

// barEighths are the partial-cell glyphs for 1–7 eighths of a cell.
var barEighths = []rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// BarDefaults supplies the width and colors applied to {..._pct:bar} tokens
// that don't override them inline. Fg is the resolved ANSI foreground escape
// for the filled portion; Bg is the resolved ANSI background escape painted
// behind the partial cell and the empty remainder, so the fill meets the
// background color with no gap.
type BarDefaults struct {
	Width int
	Fg    string
	Bg    string
}

// builtinBarDefaults returns the bar configuration used when the config has
// no memory_status_bar block: 10 cells, green fill, gray background.
func builtinBarDefaults() BarDefaults {
	return BarDefaults{Width: defaultBarWidth, Fg: cGreen, Bg: "\033[100m"}
}

// clampBarWidth bounds a user-supplied bar width to [minBarWidth, maxBarWidth].
func clampBarWidth(w int) int {
	if w < minBarWidth {
		return minBarWidth
	}
	if w > maxBarWidth {
		return maxBarWidth
	}
	return w
}

type segKind int

const (
	segLiteral segKind = iota // raw text, including pre-resolved ANSI escapes
	segValue                  // humanised bytes or integer percentage of a field
	segBar                    // bar graph of a percentage field
)

// memSegment is one compiled piece of a memory_status_format template.
type memSegment struct {
	kind  segKind
	text  string   // segLiteral only
	field memField // segValue / segBar
	bar   barSpec  // segBar only
}

// barSpec is the resolved geometry and colors of a single bar token.
type barSpec struct {
	width int
	fg    string
	bg    string
}

// MemoryTemplate is a compiled memory_status_format string. Compile once,
// render every tick: rendering only walks the segment list, so the per-tick
// cost is independent of template complexity.
type MemoryTemplate struct {
	segments []memSegment
	styled   bool
	sizeHint int
}

// Styled reports whether the template carries its own styling (color/style
// tags or bars). Styled templates are rendered as-is; plain templates keep
// the legacy dim wrap applied by the menu.
func (t *MemoryTemplate) Styled() bool {
	return t.styled
}

// CompileMemoryTemplate parses a memory_status_format string into a
// MemoryTemplate. bar supplies the defaults for {..._pct:bar} tokens without
// inline overrides. Compilation never fails: unknown or malformed tokens are
// emitted literally so typos stay visible and old configs keep working.
//
// Token forms:
//
//	{value_name}                              — substituted value
//	{style_name}                              — color / bold / dim / reset tag
//	{pct_name:bar[:width[:color[:bgcolor]]]}  — bar graph, no value shown
//
// Empty bar parts fall back to the defaults ({used_ram_pct:bar::red} keeps
// the default width). Valid widths are clamped to [1, 40].
func CompileMemoryTemplate(template string, bar BarDefaults) *MemoryTemplate {
	t := &MemoryTemplate{}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.segments = append(t.segments, memSegment{kind: segLiteral, text: lit.String()})
			t.sizeHint += lit.Len()
			lit.Reset()
		}
	}

	rest := template
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			lit.WriteString(rest)
			break
		}
		lit.WriteString(rest[:i])
		rest = rest[i:]
		j := strings.IndexByte(rest, '}')
		if j < 0 {
			lit.WriteString(rest)
			break
		}
		token := rest[:j+1]
		body := rest[1:j]
		rest = rest[j+1:]

		parts := strings.Split(body, ":")
		switch {
		case len(parts) == 1:
			if f, ok := memFields[body]; ok {
				flush()
				t.segments = append(t.segments, memSegment{kind: segValue, field: f})
				t.sizeHint += 12
			} else if esc, ok := memStyleEscape(body); ok {
				lit.WriteString(esc)
				t.styled = true
			} else {
				lit.WriteString(token)
			}
		case parts[1] == "bar" && len(parts) <= 5:
			f, ok := memFields[parts[0]]
			if !ok || !f.isPct() {
				lit.WriteString(token)
				continue
			}
			spec, ok := parseBarSpec(parts, bar)
			if !ok {
				lit.WriteString(token)
				continue
			}
			flush()
			t.segments = append(t.segments, memSegment{kind: segBar, field: f, bar: spec})
			t.sizeHint += spec.width*3 + 20
			t.styled = true
		default:
			lit.WriteString(token)
		}
	}
	flush()
	return t
}

// parseBarSpec resolves the optional width/color/bgcolor parts of a bar
// token against the configured defaults. A non-numeric width or unknown
// color name fails the whole token so it passes through literally.
func parseBarSpec(parts []string, defaults BarDefaults) (barSpec, bool) {
	spec := barSpec{width: defaults.Width, fg: defaults.Fg, bg: defaults.Bg}
	if len(parts) >= 3 && parts[2] != "" {
		w, err := strconv.Atoi(parts[2])
		if err != nil {
			return spec, false
		}
		spec.width = clampBarWidth(w)
	}
	if len(parts) >= 4 && parts[3] != "" {
		fg, _, ok := memColor(parts[3])
		if !ok {
			return spec, false
		}
		spec.fg = fg
	}
	if len(parts) >= 5 && parts[4] != "" {
		_, bg, ok := memColor(parts[4])
		if !ok {
			return spec, false
		}
		spec.bg = bg
	}
	return spec, true
}

// Render substitutes a MemStats snapshot into the compiled template.
func (t *MemoryTemplate) Render(s MemStats) string {
	var b strings.Builder
	b.Grow(t.sizeHint)
	for _, seg := range t.segments {
		switch seg.kind {
		case segLiteral:
			b.WriteString(seg.text)
		case segValue:
			b.WriteString(fieldString(s, seg.field))
		case segBar:
			writeBar(&b, fieldPct(s, seg.field), seg.bar)
		}
	}
	return b.String()
}

// fieldString renders a field the way FormatMemoryLine always has:
// humanised bytes for byte fields, "N%" for percentage fields.
func fieldString(s MemStats, f memField) string {
	if f.isPct() {
		return fmt.Sprintf("%d%%", fieldPct(s, f))
	}
	return humanBytes(fieldBytes(s, f))
}

// fieldBytes returns the raw byte count for a byte-valued field.
func fieldBytes(s MemStats, f memField) uint64 {
	switch f {
	case fieldFreeRAM:
		return s.FreeRAM
	case fieldUsedRAM:
		return s.UsedRAM
	case fieldTotalRAM:
		return s.TotalRAM
	case fieldCompressedRAM:
		return s.Compressed
	case fieldSwapUsed:
		return s.SwapUsed
	case fieldSwapTotal:
		return s.SwapTotal
	case fieldFreeSwap:
		if s.SwapTotal > s.SwapUsed {
			return s.SwapTotal - s.SwapUsed
		}
		return 0
	case fieldGPUUsedRAM:
		return s.GPUUsedRAM
	case fieldGPUAllocRAM:
		return s.GPUAllocRAM
	}
	return 0
}

// fieldPct returns the rounded 0–100 percentage for a percentage field.
// Zero denominators (swap disabled) yield 0, matching percentString.
func fieldPct(s MemStats, f memField) uint64 {
	switch f {
	case fieldFreeRAMPct:
		return percentValue(s.FreeRAM, s.TotalRAM)
	case fieldUsedRAMPct:
		return percentValue(s.UsedRAM, s.TotalRAM)
	case fieldSwapUsedPct:
		return percentValue(s.SwapUsed, s.SwapTotal)
	case fieldGPUUtilPct:
		if s.GPUUtilPct > 100 {
			return 100
		}
		return s.GPUUtilPct
	}
	return 0
}

// writeBar renders pct (0–100) as one continuous two-color strip: the
// filled portion in full blocks with an eighth-block partial cell for
// sub-cell granularity, and the background color painted as an ANSI
// background behind the partial cell and the empty remainder — so the fill
// meets the background directly, with no terminal-default gap inside the
// partial cell. Any nonzero percentage shows at least a sliver.
func writeBar(b *strings.Builder, pct uint64, spec barSpec) {
	if pct > 100 {
		pct = 100
	}
	eighths := (pct*uint64(spec.width)*8 + 50) / 100
	if pct > 0 && eighths == 0 {
		eighths = 1
	}
	full := int(eighths / 8)
	rem := int(eighths % 8)
	empty := spec.width - full
	if rem > 0 {
		empty--
	}
	if full > 0 || rem > 0 {
		b.WriteString(spec.fg)
		for i := 0; i < full; i++ {
			b.WriteRune('█')
		}
	}
	if rem > 0 || empty > 0 {
		b.WriteString(spec.bg)
		if rem > 0 {
			b.WriteRune(barEighths[rem-1])
		}
		for i := 0; i < empty; i++ {
			b.WriteRune(' ')
		}
	}
	b.WriteString(cReset)
}
