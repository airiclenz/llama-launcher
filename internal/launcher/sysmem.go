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
// (free + inactive + speculative + purgeable pages).
type MemStats struct {
	TotalRAM, FreeRAM, UsedRAM uint64
	SwapTotal, SwapUsed        uint64
}

const memStatsCacheTTL = 2 * time.Second

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
	free, perr := parseVMStat(string(vmOut))
	if perr != nil {
		return s, perr
	}
	s.FreeRAM = free
	if s.FreeRAM > s.TotalRAM {
		s.FreeRAM = s.TotalRAM
	}
	s.UsedRAM = s.TotalRAM - s.FreeRAM

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

	return s, nil
}

var (
	vmStatPageSizeRe = regexp.MustCompile(`page size of (\d+) bytes`)
	vmStatPageRe     = regexp.MustCompile(`^(.*?):\s+(\d+)\.?\s*$`)
)

// parseVMStat parses `vm_stat` output and returns the byte count Activity
// Monitor considers "available" memory: free + inactive + speculative +
// purgeable pages, times the page size declared in the header.
func parseVMStat(out string) (uint64, error) {
	m := vmStatPageSizeRe.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("vm_stat: missing page size header")
	}
	pageSize, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("vm_stat: parse page size: %w", err)
	}

	wanted := map[string]bool{
		"Pages free":         true,
		"Pages inactive":     true,
		"Pages speculative":  true,
		"Pages purgeable":    true,
	}

	var pages uint64
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		match := vmStatPageRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		key := strings.TrimSpace(match[1])
		if !wanted[key] {
			continue
		}
		n, err := strconv.ParseUint(match[2], 10, 64)
		if err != nil {
			continue
		}
		pages += n
	}
	return pages * pageSize, nil
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

// humanBytes renders a byte count as a short macOS-style string ("12.4 GB",
// "512 MB", "0 B"). Units are 1024-based; whole-unit values drop the decimal.
func humanBytes(b uint64) string {
	const k = 1024.0
	switch {
	case b < k:
		return fmt.Sprintf("%d B", b)
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
		return fmt.Sprintf("%d %s", uint64(v), unit)
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

// DefaultMemoryStatusTemplate is the readout shown when memory_status_format
// is unset in the config.
const DefaultMemoryStatusTemplate = "RAM: {free_ram} free · Swap: {swap_used} used"

// FormatMemoryLine substitutes {free_ram}, {used_ram}, {total_ram},
// {swap_used}, {swap_total} placeholders in template with humanized byte
// values. Unknown placeholders are left in place.
func FormatMemoryLine(s MemStats, template string) string {
	r := strings.NewReplacer(
		"{free_ram}", humanBytes(s.FreeRAM),
		"{used_ram}", humanBytes(s.UsedRAM),
		"{total_ram}", humanBytes(s.TotalRAM),
		"{swap_used}", humanBytes(s.SwapUsed),
		"{swap_total}", humanBytes(s.SwapTotal),
	)
	return r.Replace(template)
}
