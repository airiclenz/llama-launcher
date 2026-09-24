package launcher

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"testing"
	"time"
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
		t.Run(fmt.Sprintf("%d_of_%d", tc.n, tc.d), func(t *testing.T) {
			t.Parallel()
			got := percentValue(tc.n, tc.d)
			if got != tc.want {
				t.Errorf("percentValue(%d, %d) = %d, want %d", tc.n, tc.d, got, tc.want)
			}
		})
	}
}

// Canned readout-command outputs for the ReadMemStats tests.
const (
	cannedMemsize   = "17179869184\n"
	cannedVMStat    = "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free:     1000.\nPages inactive: 1000.\n"
	cannedSwapUsage = "vm.swapusage: total = 1024.00M  used = 512.00M  free = 512.00M  (encrypted)\n"
	cannedIoreg     = `"PerformanceStatistics" = {"Alloc system memory"=2048,"Device Utilization %"=42,"In use system memory"=1024}`
)

// memCmdKey names a readout command by its program and first argument, the
// pair that tells the two sysctl calls apart.
func memCmdKey(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + args[0] + " " + args[len(args)-1]
}

var cannedMemOutputs = map[string]string{
	"sysctl -n hw.memsize":   cannedMemsize,
	"vm_stat":                cannedVMStat,
	"sysctl -n vm.swapusage": cannedSwapUsage,
	"ioreg -r IOAccelerator": cannedIoreg,
}

// memCmdFake records how many readout commands ran and hangs the ones named
// in hung until their context expires, as a stuck subprocess would.
type memCmdFake struct {
	mu      sync.Mutex
	calls   int
	hung    map[string]bool
	entered chan string
}

func (f *memCmdFake) output(ctx context.Context, name string, args ...string) ([]byte, error) {
	key := memCmdKey(name, args)
	f.mu.Lock()
	f.calls++
	isHung := f.hung[key]
	f.mu.Unlock()
	if isHung {
		select {
		case f.entered <- key:
		default:
		}
		<-ctx.Done()
		return nil, errors.New("signal: killed")
	}
	out, ok := cannedMemOutputs[key]
	if !ok {
		return nil, fmt.Errorf("unexpected command %q", key)
	}
	return []byte(out), nil
}

func (f *memCmdFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *memCmdFake) setHung(keys ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hung = map[string]bool{}
	for _, key := range keys {
		f.hung[key] = true
	}
}

// installMemCmdFake swaps the readout command runner for a fake, shortens
// the subprocess timeout and empties the cache, restoring all three when
// the test ends. Callers must not run in parallel: the cache is package
// state.
func installMemCmdFake(t *testing.T, timeout time.Duration) *memCmdFake {
	t.Helper()
	fake := &memCmdFake{hung: map[string]bool{}, entered: make(chan string, 1)}
	savedOutput, savedTimeout := memCmdOutput, memCmdTimeout
	resetMemCache := func() {
		memCacheMu.Lock()
		defer memCacheMu.Unlock()
		memCacheAt, memCacheData, memCacheErr, isMemRefreshing = time.Time{}, MemStats{}, nil, false
	}
	resetMemCache()
	memCmdOutput, memCmdTimeout = fake.output, timeout
	t.Cleanup(func() {
		memCmdOutput, memCmdTimeout = savedOutput, savedTimeout
		resetMemCache()
	})
	return fake
}

// expireMemCache backdates the cache stamp past the TTL so the next read
// refreshes, without sleeping through the real TTL.
func expireMemCache() {
	memCacheMu.Lock()
	defer memCacheMu.Unlock()
	memCacheAt = time.Now().Add(-2 * memStatsCacheTTL)
}

func TestReadMemStatsHungCommandIsBoundedAndThrottled(t *testing.T) {
	const timeout = 300 * time.Millisecond
	fake := installMemCmdFake(t, timeout)
	fake.setHung("vm_stat")

	type result struct {
		stats   MemStats
		err     error
		elapsed time.Duration
	}
	firstDone := make(chan result, 1)
	go func() {
		start := time.Now()
		stats, err := ReadMemStats()
		firstDone <- result{stats, err, time.Since(start)}
	}()
	<-fake.entered

	// A read racing the hung refresh must neither block nor spawn its own.
	callsBefore := fake.callCount()
	if _, err := ReadMemStats(); !errors.Is(err, errMemStatsPending) {
		t.Errorf("concurrent read err = %v, want errMemStatsPending", err)
	}
	if got := fake.callCount(); got != callsBefore {
		t.Errorf("concurrent read ran %d commands, want 0", got-callsBefore)
	}
	select {
	case <-firstDone:
		t.Fatal("concurrent read waited for the hung refresh to finish")
	default:
	}

	first := <-firstDone
	if !errors.Is(first.err, context.DeadlineExceeded) {
		t.Errorf("hung refresh err = %v, want context.DeadlineExceeded", first.err)
	}
	if first.elapsed > timeout+time.Second {
		t.Errorf("hung refresh took %v, want about %v", first.elapsed, timeout)
	}

	callsAfterTimeout := fake.callCount()
	if _, err := ReadMemStats(); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("read inside the TTL err = %v, want the published timeout", err)
	}
	if got := fake.callCount(); got != callsAfterTimeout {
		t.Errorf("read inside the TTL ran %d commands, want 0", got-callsAfterTimeout)
	}
}

func TestReadMemStatsTimeoutKeepsLastGoodData(t *testing.T) {
	fake := installMemCmdFake(t, 100*time.Millisecond)
	good, err := ReadMemStats()
	if err != nil || good.TotalRAM == 0 {
		t.Fatalf("priming read = %+v, %v; want data", good, err)
	}

	expireMemCache()
	fake.setHung("vm_stat")
	got, err := ReadMemStats()
	if err != nil {
		t.Fatalf("timed-out refresh err = %v, want the last good data", err)
	}
	if got != good {
		t.Errorf("timed-out refresh = %+v, want last good %+v", got, good)
	}

	callsAfterTimeout := fake.callCount()
	if got, err := ReadMemStats(); err != nil || got != good {
		t.Errorf("read inside the TTL = %+v, %v; want last good data", got, err)
	}
	if got := fake.callCount(); got != callsAfterTimeout {
		t.Errorf("read inside the TTL ran %d commands, want 0", got-callsAfterTimeout)
	}
}

func TestReadMemStatsIoregTimeoutZeroesGPU(t *testing.T) {
	fake := installMemCmdFake(t, 100*time.Millisecond)
	fake.setHung("ioreg -r IOAccelerator")

	got, err := ReadMemStats()
	if err != nil {
		t.Fatalf("err = %v, want the readout without GPU fields", err)
	}
	if got.TotalRAM == 0 || got.SwapTotal == 0 {
		t.Errorf("memory fields = %+v, want them filled", got)
	}
	if got.GPUUtilPct != 0 || got.GPUUsedRAM != 0 || got.GPUAllocRAM != 0 {
		t.Errorf("GPU fields = %d/%d/%d, want zero", got.GPUUtilPct, got.GPUUsedRAM, got.GPUAllocRAM)
	}
}

func TestReadMemStatsCannedReadout(t *testing.T) {
	installMemCmdFake(t, time.Second)

	got, err := ReadMemStats()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	want := MemStats{
		TotalRAM: 17179869184, FreeRAM: 2000 * 16384, UsedRAM: 17179869184 - 2000*16384,
		SwapTotal: 1024 << 20, SwapUsed: 512 << 20,
		GPUUtilPct: 42, GPUUsedRAM: 1024, GPUAllocRAM: 2048,
	}
	if got != want {
		t.Errorf("ReadMemStats() = %+v, want %+v", got, want)
	}
}

func TestSysmemCommandTimeoutKillsSubprocess(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary on PATH")
	}
	saved := memCmdTimeout
	memCmdTimeout = 100 * time.Millisecond
	t.Cleanup(func() { memCmdTimeout = saved })

	start := time.Now()
	_, err := runMemCmd("sleep", "10")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("runMemCmd took %v, want about %v", elapsed, memCmdTimeout)
	}
}
