package launcher

import (
	"fmt"
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
Pages occupied by compressor:                  74215.
"Translation faults":                       97228747.
`
	// 3929 + 449039 + 37622 + 12251 = 502841 pages * 16384 bytes
	wantFree16k := uint64(502841) * 16384
	wantCompressed16k := uint64(74215) * 16384

	sample4k := `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages free:                                     1000.
Pages inactive:                                 2000.
Pages speculative:                               500.
Pages purgeable:                                 100.
Pages wired down:                              99999.
`
	wantFree4k := uint64(3600) * 4096

	cases := []struct {
		name           string
		input          string
		wantFree       uint64
		wantCompressed uint64
		wantErr        bool
	}{
		{name: "16 KiB pages (Apple Silicon)", input: sample16k, wantFree: wantFree16k, wantCompressed: wantCompressed16k},
		{name: "4 KiB pages (Intel, no compressor line)", input: sample4k, wantFree: wantFree4k, wantCompressed: 0},
		{name: "no page size header", input: "Pages free: 1000.\n", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			free, compressed, err := parseVMStat(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if free != tc.wantFree {
				t.Errorf("free = %d bytes, want %d", free, tc.wantFree)
			}
			if compressed != tc.wantCompressed {
				t.Errorf("compressed = %d bytes, want %d", compressed, tc.wantCompressed)
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
		{0, "0B"},
		{512, "512B"},
		{1024, "1KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1MB"},
		{999 * 1024 * 1024, "999MB"},
		{1024 * 1024 * 1024, "1GB"},
		{uint64(31.9 * gib), "31.9GB"},
		{32 * 1024 * 1024 * 1024, "32GB"},
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

func TestParseIOAccelerator(t *testing.T) {
	t.Parallel()

	appleSilicon := `+-o AGXAcceleratorG14X  <class AGXAcceleratorG14X, id 0x1000004b2, registered, matched, active, busy 0 (346 ms), retain 47>
    {
      "PerformanceStatistics" = {"In use system memory (driver)"=0,"Alloc system memory"=16541990912,"Tiler Utilization %"=3,"recoveryCount"=0,"lastRecoveryTime"=0,"Renderer Utilization %"=3,"TiledSceneBytes"=3309568,"Device Utilization %"=42,"SplitSceneCount"=0,"Allocated PB Size"=126091264,"In use system memory"=830029824}
      "model" = "Apple M2 Pro"
    }
`

	cases := []struct {
		name      string
		input     string
		wantUtil  uint64
		wantUsed  uint64
		wantAlloc uint64
	}{
		{
			name:      "Apple Silicon AGX entry",
			input:     appleSilicon,
			wantUtil:  42,
			wantUsed:  830029824,
			wantAlloc: 16541990912,
		},
		{
			name:      "no IOAccelerator entries",
			input:     "no relevant ioreg output here\n",
			wantUtil:  0,
			wantUsed:  0,
			wantAlloc: 0,
		},
		{
			name:      "intel discrete GPU (different schema, missing AGX keys)",
			input:     `"PerformanceStatistics"={"vramFreeBytes"=15728640}`,
			wantUtil:  0,
			wantUsed:  0,
			wantAlloc: 0,
		},
		{
			name:      "malformed values are ignored",
			input:     `"Device Utilization %" = abc, "Alloc system memory" = , "In use system memory" = 1024`,
			wantUtil:  0,
			wantUsed:  1024,
			wantAlloc: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			util, used, alloc := parseIOAccelerator(tc.input)
			if util != tc.wantUtil {
				t.Errorf("util = %d, want %d", util, tc.wantUtil)
			}
			if used != tc.wantUsed {
				t.Errorf("used = %d, want %d", used, tc.wantUsed)
			}
			if alloc != tc.wantAlloc {
				t.Errorf("alloc = %d, want %d", alloc, tc.wantAlloc)
			}
		})
	}
}

func TestPercentValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		n, d uint64
		want uint64
	}{
		{0, 0, 0},
		{1, 0, 0},
		{0, 100, 0},
		{50, 100, 50},
		{1, 3, 33},
		{2, 3, 67},
		{1, 2, 50},
		{100, 100, 100},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d_%d", tc.n, tc.d), func(t *testing.T) {
			t.Parallel()
			got := percentValue(tc.n, tc.d)
			if got != tc.want {
				t.Errorf("percentValue(%d, %d) = %d, want %d", tc.n, tc.d, got, tc.want)
			}
		})
	}
}
