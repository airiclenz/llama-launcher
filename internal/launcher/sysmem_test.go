package launcher

import (
	"strings"
	"testing"
)

func TestParseSwapUsage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		input     string
		wantTotal uint64
		wantUsed  uint64
		wantErr   bool
	}{
		{
			name:      "zero swap",
			input:     "vm.swapusage: total = 0.00M  used = 0.00M  free = 0.00M  (encrypted)\n",
			wantTotal: 0,
			wantUsed:  0,
		},
		{
			name:      "megabytes",
			input:     "vm.swapusage: total = 4096.00M  used = 2113.50M  free = 1982.50M  (encrypted)\n",
			wantTotal: 4096 * 1024 * 1024,
			wantUsed:  uint64(2113.5 * float64(1<<20)),
		},
		{
			name:      "gigabytes",
			input:     "vm.swapusage: total = 2.00G  used = 0.50G  free = 1.50G  (encrypted)\n",
			wantTotal: 2 * 1024 * 1024 * 1024,
			wantUsed:  uint64(0.5 * float64(1<<30)),
		},
		{
			name:    "unparseable",
			input:   "nothing here\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			total, used, err := parseSwapUsage(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if used != tc.wantUsed {
				t.Errorf("used = %d, want %d", used, tc.wantUsed)
			}
		})
	}
}

func TestParseVMStat(t *testing.T) {
	t.Parallel()

	sample16k := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                     3929.
Pages active:                                 487417.
Pages inactive:                               449039.
Pages speculative:                             37622.
Pages throttled:                                   0.
Pages wired down:                            1013634.
Pages purgeable:                               12251.
"Translation faults":                       97228747.
`
	// 3929 + 449039 + 37622 + 12251 = 502841 pages * 16384 bytes
	wantBytes16k := uint64(502841) * 16384

	sample4k := `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages free:                                     1000.
Pages inactive:                                 2000.
Pages speculative:                               500.
Pages purgeable:                                 100.
Pages wired down:                              99999.
`
	wantBytes4k := uint64(3600) * 4096

	cases := []struct {
		name    string
		input   string
		want    uint64
		wantErr bool
	}{
		{name: "16 KiB pages (Apple Silicon)", input: sample16k, want: wantBytes16k},
		{name: "4 KiB pages (Intel)", input: sample4k, want: wantBytes4k},
		{name: "no page size header", input: "Pages free: 1000.\n", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseVMStat(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got != tc.want {
				t.Errorf("got %d bytes, want %d", got, tc.want)
			}
		})
	}
}

func TestHumanBytes(t *testing.T) {
	t.Parallel()

	gib := float64(1 << 30)
	cases := []struct {
		bytes uint64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1 MB"},
		{999 * 1024 * 1024, "999 MB"},
		{1024 * 1024 * 1024, "1 GB"},
		{uint64(31.9 * gib), "31.9 GB"},
		{32 * 1024 * 1024 * 1024, "32 GB"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := humanBytes(tc.bytes)
			if got != tc.want {
				t.Errorf("humanBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
			}
		})
	}
}

func TestFormatMemoryLine(t *testing.T) {
	t.Parallel()

	stats := MemStats{
		TotalRAM:  32 * 1024 * 1024 * 1024,
		FreeRAM:   12 * 1024 * 1024 * 1024,
		UsedRAM:   20 * 1024 * 1024 * 1024,
		SwapTotal: 4 * 1024 * 1024 * 1024,
		SwapUsed:  uint64(1.5 * float64(1<<30)),
	}

	cases := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "default template",
			template: DefaultMemoryStatusTemplate,
			want:     "RAM: 12 GB free · Swap: 1.5 GB used",
		},
		{
			name:     "used/total template",
			template: "Mem {used_ram}/{total_ram} · Swap {swap_used}",
			want:     "Mem 20 GB/32 GB · Swap 1.5 GB",
		},
		{
			name:     "unknown placeholder passes through",
			template: "free={free_ram} unknown={foo}",
			want:     "free=12 GB unknown={foo}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := FormatMemoryLine(stats, tc.template)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if !strings.Contains(got, "GB") && tc.want != "" {
				t.Errorf("expected GB unit somewhere in %q", got)
			}
		})
	}
}
