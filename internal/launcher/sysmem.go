package launcher

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemStats is a snapshot of macOS unified-memory and swap usage in bytes.
// FreeRAM follows the Activity Monitor "available" definition
// (free + inactive + speculative + purgeable pages). Compressed is the
// byte count held by the kernel's memory compressor. GPU fields are sourced
// from ioreg's AGXAccelerator entry (Apple Silicon only); they read zero on
// Intel Macs or when ioreg is unavailable.
type MemStats struct {
	TotalRAM, FreeRAM, UsedRAM uint64
	Compressed                 uint64
	SwapTotal, SwapUsed        uint64
	GPUUtilPct                 uint64
	GPUUsedRAM, GPUAllocRAM    uint64
}

// memStatsCacheTTL sits just below the menu's 1-second status tick
// (statusTickInterval) so every tick reads fresh values while keystroke
// bursts between ticks still hit the cache.
const memStatsCacheTTL = 900 * time.Millisecond

var (
	memCacheMu   sync.Mutex
	memCacheAt   time.Time
	memCacheData MemStats
	memCacheErr  error
)

// ReadMemStats returns current memory and swap usage on macOS. Results are
// cached for memStatsCacheTTL to keep keystroke-driven re-renders cheap.
func ReadMemStats() (MemStats, error) {
	memCacheMu.Lock()
	defer memCacheMu.Unlock()

	if time.Since(memCacheAt) < memStatsCacheTTL && (memCacheErr != nil || memCacheData.TotalRAM > 0) {
		return memCacheData, memCacheErr
	}

	stats, err := readMemStatsLive()
	memCacheAt = time.Now()
	memCacheData = stats
	memCacheErr = err
	return stats, err
}

func readMemStatsLive() (MemStats, error) {
	var s MemStats

	totalOut, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return s, fmt.Errorf("sysctl hw.memsize: %w", err)
	}
	total, err := strconv.ParseUint(strings.TrimSpace(string(totalOut)), 10, 64)
	if err != nil {
		return s, fmt.Errorf("parse hw.memsize: %w", err)
	}
	s.TotalRAM = total

	vmOut, err := exec.Command("vm_stat").Output()
	if err != nil {
		return s, fmt.Errorf("vm_stat: %w", err)
	}
	free, compressed, perr := parseVMStat(string(vmOut))
	if perr != nil {
		return s, perr
	}
	s.FreeRAM = free
	if s.FreeRAM > s.TotalRAM {
		s.FreeRAM = s.TotalRAM
	}
	s.UsedRAM = s.TotalRAM - s.FreeRAM
	s.Compressed = compressed

	swapOut, err := exec.Command("sysctl", "-n", "vm.swapusage").Output()
	if err != nil {
		return s, fmt.Errorf("sysctl vm.swapusage: %w", err)
	}
	swapTotal, swapUsed, perr := parseSwapUsage(string(swapOut))
	if perr != nil {
		return s, perr
	}
	s.SwapTotal = swapTotal
	s.SwapUsed = swapUsed

	if gpuOut, gerr := exec.Command("ioreg", "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator").Output(); gerr == nil {
		util, used, alloc := parseIOAccelerator(string(gpuOut))
		s.GPUUtilPct = util
		s.GPUUsedRAM = used
		s.GPUAllocRAM = alloc
	}

	return s, nil
}

var ioregGPURe = map[string]*regexp.Regexp{
	"util":  regexp.MustCompile(`"Device Utilization %"\s*=\s*(\d+)`),
	"used":  regexp.MustCompile(`"In use system memory"\s*=\s*(\d+)`),
	"alloc": regexp.MustCompile(`"Alloc system memory"\s*=\s*(\d+)`),
}

// parseIOAccelerator extracts GPU utilization (0–100) and unified-memory
// byte counts from `ioreg -r -c IOAccelerator` output. The relevant fields
// live in the AGXAccelerator entry's PerformanceStatistics dict on Apple
// Silicon; missing or unparseable keys return 0 rather than erroring so
// non-AS hardware degrades silently.
func parseIOAccelerator(out string) (utilPct, usedBytes, allocBytes uint64) {
	parse := func(key string) uint64 {
		m := ioregGPURe[key].FindStringSubmatch(out)
		if m == nil {
			return 0
		}
		n, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return parse("util"), parse("used"), parse("alloc")
}

var (
	vmStatPageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)
	vmStatPageRe     = regexp.MustCompile(`^(.*?):\s+(\d+)\.?\s*$`)
)

// parseVMStat parses `vm_stat` output and returns two byte counts: the
// "available" memory Activity Monitor reports (free + inactive +
// speculative + purgeable pages) and the bytes held by the kernel's
// memory compressor. Both are multiplied by the page size declared in
// the header.
func parseVMStat(out string) (free, compressed uint64, err error) {
	m := vmStatPageSizeRe.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, fmt.Errorf("vm_stat: missing page size header")
	}
	pageSize, perr := strconv.ParseUint(m[1], 10, 64)
	if perr != nil {
		return 0, 0, fmt.Errorf("vm_stat: parse page size: %w", perr)
	}

	freeKeys := map[string]bool{
		"Pages free":        true,
		"Pages inactive":    true,
		"Pages speculative": true,
		"Pages purgeable":   true,
	}
	const compressorKey = "Pages occupied by compressor"

	var freePages, compressorPages uint64
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		match := vmStatPageRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		key := strings.TrimSpace(match[1])
		n, perr := strconv.ParseUint(match[2], 10, 64)
		if perr != nil {
			continue
		}
		switch {
		case freeKeys[key]:
			freePages += n
		case key == compressorKey:
			compressorPages = n
		}
	}
	return freePages * pageSize, compressorPages * pageSize, nil
}

var swapFieldRe = regexp.MustCompile(`(total|used)\s*=\s*([\d.]+)([KMGT]?)`)

// parseSwapUsage parses `sysctl vm.swapusage` output (e.g.
// "vm.swapusage: total = 4096.00M  used = 2113.50M  free = ...") and
// returns total and used bytes. Suffixes are 1024-based, matching the
// kernel's reporting.
func parseSwapUsage(out string) (total, used uint64, err error) {
	matches := swapFieldRe.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return 0, 0, fmt.Errorf("swap: no fields matched in %q", strings.TrimSpace(out))
	}
	for _, m := range matches {
		v, perr := strconv.ParseFloat(m[2], 64)
		if perr != nil {
			return 0, 0, fmt.Errorf("swap: parse value %q: %w", m[2], perr)
		}
		bytes := uint64(v * suffixMultiplier(m[3]))
		switch m[1] {
		case "total":
			total = bytes
		case "used":
			used = bytes
		}
	}
	return total, used, nil
}

func suffixMultiplier(suffix string) float64 {
	switch suffix {
	case "K":
		return 1 << 10
	case "M":
		return 1 << 20
	case "G":
		return 1 << 30
	case "T":
		return 1 << 40
	default:
		return 1
	}
}

// humanBytes renders a byte count as a short macOS-style string ("12.4GB",
// "512MB", "0B"). Units are 1024-based; whole-unit values drop the decimal.
func humanBytes(b uint64) string {
	const k = 1024.0
	switch {
	case b < k:
		return fmt.Sprintf("%dB", b)
	case b < k*k:
		return formatUnit(float64(b)/k, "KB")
	case b < k*k*k:
		return formatUnit(float64(b)/(k*k), "MB")
	case b < k*k*k*k:
		return formatUnit(float64(b)/(k*k*k), "GB")
	default:
		return formatUnit(float64(b)/(k*k*k*k), "TB")
	}
}

func formatUnit(v float64, unit string) string {
	if v == float64(uint64(v)) {
		return fmt.Sprintf("%d%s", uint64(v), unit)
	}
	return fmt.Sprintf("%.1f%s", v, unit)
}

// DefaultMemoryStatusTemplate is the readout shown when memory_status_format
// is unset in the config.
const DefaultMemoryStatusTemplate = "RAM: {free_ram} free · Swap: {swap_used} used"

// FormatMemoryLine substitutes memory readout placeholders in template
// with humanised byte values (e.g. "12 GB") or integer percentages
// (e.g. "23%"). Unknown placeholders are left in place. Percentage
// placeholders return "0%" when the denominator is zero.
func FormatMemoryLine(s MemStats, template string) string {
	freeSwap := uint64(0)
	if s.SwapTotal > s.SwapUsed {
		freeSwap = s.SwapTotal - s.SwapUsed
	}
	r := strings.NewReplacer(
		"{free_ram}", humanBytes(s.FreeRAM),
		"{used_ram}", humanBytes(s.UsedRAM),
		"{total_ram}", humanBytes(s.TotalRAM),
		"{compressed_ram}", humanBytes(s.Compressed),
		"{swap_used}", humanBytes(s.SwapUsed),
		"{swap_total}", humanBytes(s.SwapTotal),
		"{free_swap}", humanBytes(freeSwap),
		"{free_ram_pct}", percentString(s.FreeRAM, s.TotalRAM),
		"{used_ram_pct}", percentString(s.UsedRAM, s.TotalRAM),
		"{swap_used_pct}", percentString(s.SwapUsed, s.SwapTotal),
		"{gpu_util_pct}", fmt.Sprintf("%d%%", s.GPUUtilPct),
		"{gpu_used_ram}", humanBytes(s.GPUUsedRAM),
		"{gpu_alloc_ram}", humanBytes(s.GPUAllocRAM),
	)
	return r.Replace(template)
}

// percentString renders n/d as a rounded integer percentage with a
// trailing "%". Returns "0%" when d is zero so swap-disabled systems
// don't show a divide-by-zero.
func percentString(n, d uint64) string {
	if d == 0 {
		return "0%"
	}
	p := (n*100 + d/2) / d
	return fmt.Sprintf("%d%%", p)
}
