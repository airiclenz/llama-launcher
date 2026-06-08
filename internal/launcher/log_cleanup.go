package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const logTimestampFormat = "20060102-150405"

type CleanupResult struct {
	Removed int
	Freed   int64
}

func cleanupLogs(cfg *Config, logDir string, maxAge time.Duration, deleteAll bool) (CleanupResult, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return CleanupResult{}, nil
		}
		return CleanupResult{}, fmt.Errorf("reading log directory: %w", err)
	}

	active := activeLogFiles(cfg)
	now := time.Now()
	var result CleanupResult

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}

		path := filepath.Join(logDir, e.Name())
		if active[path] {
			continue
		}

		if !deleteAll {
			ts, err := parseLogTimestamp(e.Name())
			if err != nil {
				continue
			}
			if now.Sub(ts) < maxAge {
				continue
			}
		}

		info, err := e.Info()
		if err != nil {
			continue
		}
		size := info.Size()

		if err := os.Remove(path); err != nil {
			continue
		}
		result.Removed++
		result.Freed += size
	}

	return result, nil
}

func parseLogTimestamp(filename string) (time.Time, error) {
	if !strings.HasSuffix(filename, ".log") {
		return time.Time{}, fmt.Errorf("cannot parse timestamp from %q: not a .log file", filename)
	}
	name := strings.TrimSuffix(filename, ".log")
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		return time.Time{}, fmt.Errorf("cannot parse timestamp from %q", filename)
	}
	stamp := parts[len(parts)-2] + "-" + parts[len(parts)-1]
	t, err := time.Parse(logTimestampFormat, stamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot parse timestamp from %q: %w", filename, err)
	}
	return t, nil
}

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1fKB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// activeLogFiles maps the current log-file path for each running instance.
// Discovered via live probing, then resolved to a path via the deterministic
// log-naming convention. Used by cleanupLogs to skip files in active use.
// Returns an empty map when no config is provided (the autoCleanupLogs
// fast-path on log creation has no cfg to share).
func activeLogFiles(cfg *Config) map[string]bool {
	active := make(map[string]bool)
	if cfg == nil {
		return active
	}
	for _, inst := range DiscoverRunningInstances(cfg) {
		fillRuntimeDetails(cfg, inst)
		if inst.LogFile != "" {
			active[inst.LogFile] = true
		}
	}
	return active
}

func autoCleanupLogs(logDir string, retentionDays int) {
	maxAge := time.Duration(retentionDays) * 24 * time.Hour
	cleanupLogs(nil, logDir, maxAge, false)
}
