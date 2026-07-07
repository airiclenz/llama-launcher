package launcher

import (
	"strings"
	"testing"
)

// memTestStats mirrors the fixture in sysmem_test.go: 32GB RAM with 12GB
// free (38%), 4GB swap with 1.5GB used (38%), GPU at 17%.
func memTestStats() MemStats {
	return MemStats{
		TotalRAM:    32 * 1024 * 1024 * 1024,
		FreeRAM:     12 * 1024 * 1024 * 1024,
		UsedRAM:     20 * 1024 * 1024 * 1024,
		Compressed:  2 * 1024 * 1024 * 1024,
		SwapTotal:   4 * 1024 * 1024 * 1024,
		SwapUsed:    uint64(1.5 * float64(1<<30)),
		GPUUtilPct:  17,
		GPUUsedRAM:  512 * 1024 * 1024,
		GPUAllocRAM: 15 * 1024 * 1024 * 1024,
	}
}

func testBarDefaults() BarDefaults {
	return BarDefaults{Width: 4, Fg: "\033[32m", Bg: "\033[100m"}
}

func TestCompileMemoryTemplate_StyleTags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		template string
		want     string
		styled   bool
	}{
		{
			name:     "plain template is not styled",
			template: "RAM: {free_ram} free",
			want:     "RAM: 12GB free",
			styled:   false,
		},
		{
			name:     "color tags resolve to ANSI escapes",
			template: "{red}hot{reset}",
			want:     "\033[31mhot\033[0m",
			styled:   true,
		},
		{
			name:     "bold and dim tags",
			template: "{bold}a{dim}b{reset}",
			want:     "\033[1ma\033[2mb\033[0m",
			styled:   true,
		},
		{
			name:     "gray aliases bright-black",
			template: "{gray}x{bright-black}y",
			want:     "\033[90mx\033[90my",
			styled:   true,
		},
		{
			name:     "bright color variant",
			template: "{bright-cyan}c",
			want:     "\033[96mc",
			styled:   true,
		},
		{
			name:     "256-color palette index",
			template: "{208}x{240}y",
			want:     "\033[38;5;208mx\033[38;5;240my",
			styled:   true,
		},
		{
			name:     "hex truecolor",
			template: "{#7aa2f7}x",
			want:     "\033[38;2;122;162;247mx",
			styled:   true,
		},
		{
			name:     "short hex expands per component",
			template: "{#f80}x",
			want:     "\033[38;2;255;136;0mx",
			styled:   true,
		},
		{
			name:     "out-of-range palette index passes through",
			template: "{256}x",
			want:     "{256}x",
			styled:   false,
		},
		{
			name:     "malformed hex passes through",
			template: "{#12345}x{#gggggg}y",
			want:     "{#12345}x{#gggggg}y",
			styled:   false,
		},
		{
			name:     "styled values mix tags and placeholders",
			template: "{dim}RAM{reset} {green}{free_ram}{reset}",
			want:     "\033[2mRAM\033[0m \033[32m12GB\033[0m",
			styled:   true,
		},
		{
			name:     "unknown tag passes through and stays unstyled",
			template: "{sparkle}{free_ram}",
			want:     "{sparkle}12GB",
			styled:   false,
		},
		{
			name:     "unclosed brace passes through",
			template: "RAM {free_ram",
			want:     "RAM {free_ram",
			styled:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tpl := CompileMemoryTemplate(tc.template, testBarDefaults())
			if got := tpl.Render(memTestStats()); got != tc.want {
				t.Errorf("Render = %q, want %q", got, tc.want)
			}
			if tpl.Styled() != tc.styled {
				t.Errorf("Styled = %v, want %v", tpl.Styled(), tc.styled)
			}
		})
	}
}

func TestCompileMemoryTemplate_BarTokens(t *testing.T) {
	t.Parallel()

	stats := memTestStats() // used_ram_pct = 63%

	cases := []struct {
		name     string
		template string
		want     string
	}{
		{
			// 63% of 4 cells = 20.16 eighths, rounds to 20: 2 full + 4/8 partial + 1 empty.
			name:     "bar with defaults",
			template: "{used_ram_pct:bar}",
			want:     "\033[32m██\033[100m▌ \033[0m",
		},
		{
			// 63% of 8 cells = 40.32 eighths, rounds to 40: 5 full + 3 empty.
			name:     "inline width override",
			template: "{used_ram_pct:bar:8}",
			want:     "\033[32m█████\033[100m   \033[0m",
		},
		{
			name:     "inline fill color override",
			template: "{used_ram_pct:bar:8:red}",
			want:     "\033[31m█████\033[100m   \033[0m",
		},
		{
			name:     "inline background color override",
			template: "{used_ram_pct:bar:8:red:blue}",
			want:     "\033[31m█████\033[44m   \033[0m",
		},
		{
			name:     "empty width part keeps default width",
			template: "{used_ram_pct:bar::red}",
			want:     "\033[31m██\033[100m▌ \033[0m",
		},
		{
			name:     "256-color and hex bar colors",
			template: "{used_ram_pct:bar:8:208:#334155}",
			want:     "\033[38;5;208m█████\033[48;2;51;65;85m   \033[0m",
		},
		{
			name:     "non-numeric width passes through",
			template: "{used_ram_pct:bar:abc}",
			want:     "{used_ram_pct:bar:abc}",
		},
		{
			name:     "unknown fill color passes through",
			template: "{used_ram_pct:bar:8:notacolor}",
			want:     "{used_ram_pct:bar:8:notacolor}",
		},
		{
			name:     "unknown background color passes through",
			template: "{used_ram_pct:bar:8:red:notacolor}",
			want:     "{used_ram_pct:bar:8:red:notacolor}",
		},
		{
			name:     "bar on byte field passes through",
			template: "{free_ram:bar}",
			want:     "{free_ram:bar}",
		},
		{
			name:     "bar on unknown field passes through",
			template: "{nope_pct:bar}",
			want:     "{nope_pct:bar}",
		},
		{
			name:     "too many parts passes through",
			template: "{used_ram_pct:bar:8:red:blue:extra}",
			want:     "{used_ram_pct:bar:8:red:blue:extra}",
		},
		{
			name:     "non-bar modifier passes through",
			template: "{used_ram_pct:sparkline}",
			want:     "{used_ram_pct:sparkline}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tpl := CompileMemoryTemplate(tc.template, testBarDefaults())
			if got := tpl.Render(stats); got != tc.want {
				t.Errorf("Render = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompileMemoryTemplate_BarStyledAndWidth(t *testing.T) {
	t.Parallel()

	tpl := CompileMemoryTemplate("{used_ram_pct:bar}", testBarDefaults())
	if !tpl.Styled() {
		t.Error("bar template should report Styled")
	}

	for _, tc := range []struct {
		template  string
		wantWidth int
	}{
		{"{used_ram_pct:bar}", 4},
		{"{used_ram_pct:bar:12}", 12},
		{"{used_ram_pct:bar:0}", minBarWidth},
		{"{used_ram_pct:bar:500}", maxBarWidth},
	} {
		got := CompileMemoryTemplate(tc.template, testBarDefaults()).Render(memTestStats())
		if w := visibleWidth(got); w != tc.wantWidth {
			t.Errorf("visibleWidth(%q render) = %d, want %d", tc.template, w, tc.wantWidth)
		}
	}
}

func TestWriteBar_Boundaries(t *testing.T) {
	t.Parallel()

	spec := barSpec{width: 8, fg: "<F>", bg: "<B>"}
	reset := "\033[0m"

	cases := []struct {
		name string
		pct  uint64
		spec barSpec
		want string
	}{
		{
			name: "zero percent is all background",
			pct:  0,
			spec: spec,
			want: "<B>        " + reset,
		},
		{
			name: "hundred percent is all full blocks",
			pct:  100,
			spec: spec,
			want: "<F>████████" + reset,
		},
		{
			name: "over hundred clamps to full",
			pct:  250,
			spec: spec,
			want: "<F>████████" + reset,
		},
		{
			name: "fifty percent splits evenly",
			pct:  50,
			spec: spec,
			want: "<F>████<B>    " + reset,
		},
		{
			name: "nonzero percent shows at least a sliver",
			pct:  1,
			spec: spec,
			want: "<F><B>▏       " + reset,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var b strings.Builder
			writeBar(&b, tc.pct, tc.spec)
			if got := b.String(); got != tc.want {
				t.Errorf("writeBar(%d) = %q, want %q", tc.pct, got, tc.want)
			}
		})
	}
}

func TestWriteBar_EighthGlyphs(t *testing.T) {
	t.Parallel()

	// Width 1 exposes every partial glyph: eighths = round(pct*8/100).
	spec := barSpec{width: 1, fg: "<F>", bg: "<B>"}
	reset := "\033[0m"

	cases := []struct {
		pct  uint64
		want string
	}{
		{6, "<F><B>▏" + reset},  // rounds to 0, min-sliver rule applies
		{25, "<F><B>▎" + reset}, // 2 eighths
		{38, "<F><B>▍" + reset}, // 3 eighths
		{50, "<F><B>▌" + reset}, // 4 eighths
		{63, "<F><B>▋" + reset}, // 5 eighths
		{75, "<F><B>▊" + reset}, // 6 eighths
		{88, "<F><B>▉" + reset}, // 7 eighths
		{94, "<F>█" + reset},    // rounds to 8, full block
	}

	for _, tc := range cases {
		var b strings.Builder
		writeBar(&b, tc.pct, spec)
		if got := b.String(); got != tc.want {
			t.Errorf("writeBar(%d%%) = %q, want %q", tc.pct, got, tc.want)
		}
	}
}

func TestMemoryTemplate_SwapDisabledBarIsEmpty(t *testing.T) {
	t.Parallel()

	stats := memTestStats()
	stats.SwapTotal = 0
	stats.SwapUsed = 0

	got := CompileMemoryTemplate("{swap_used_pct:bar}", testBarDefaults()).Render(stats)
	want := "\033[100m    \033[0m"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestMemoryTemplate_LegacyByteCompat(t *testing.T) {
	t.Parallel()

	// Placeholder-only templates must render byte-for-byte as the plain
	// pre-1.5 readout did, and must not report Styled (no style tags or bars).
	stats := memTestStats()
	cases := []struct {
		template string
		want     string
	}{
		{"RAM: {free_ram} free · Swap: {swap_used} used", "RAM: 12GB free · Swap: 1.5GB used"}, // pre-1.5 default
		{"Mem {used_ram}/{total_ram} · Swap {swap_used}", "Mem 20GB/32GB · Swap 1.5GB"},
		{"{free_ram_pct} free / {used_ram_pct} used", "38% free / 63% used"},
		{"GPU {gpu_util_pct} · {gpu_used_ram} / {gpu_alloc_ram}", "GPU 17% · 512MB / 15GB"},
		{"free={free_ram} unknown={foo}", "free=12GB unknown={foo}"},
	}
	for _, tc := range cases {
		tpl := CompileMemoryTemplate(tc.template, builtinBarDefaults())
		if got := tpl.Render(stats); got != tc.want {
			t.Errorf("template %q: Render = %q, want %q", tc.template, got, tc.want)
		}
		if tpl.Styled() {
			t.Errorf("template %q should not report Styled", tc.template)
		}
	}
}

func TestDefaultMemoryStatusTemplate(t *testing.T) {
	t.Parallel()

	tpl := CompileMemoryTemplate(DefaultMemoryStatusTemplate, builtinBarDefaults())
	if !tpl.Styled() {
		t.Error("default template should report Styled")
	}

	// Fixture: 12GB free (38%), 63% used, 1.5GB swap used, GPU at 17%.
	want := "\033[1mFree RAM:\033[0m \033[33m12GB \033[94m38%\033[0m " +
		"\033[32m██████\033[100m▎   \033[0m" +
		" ✦ \033[1mSwap:\033[0m \033[33m1.5GB\033[0m ✦ \033[1mGPU:\033[0m " +
		"\033[32m█\033[100m▊        \033[0m"
	if got := tpl.Render(memTestStats()); got != want {
		t.Errorf("default template render = %q, want %q", got, want)
	}
}

func BenchmarkMemoryTemplateRender(b *testing.B) {
	tpl := CompileMemoryTemplate(
		"{dim}RAM{reset} {used_ram_pct:bar} {free_ram} free · {dim}Swap{reset} {swap_used_pct:bar:6:yellow} {swap_used}",
		builtinBarDefaults(),
	)
	stats := memTestStats()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = tpl.Render(stats)
	}
}
