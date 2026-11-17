package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// logTimestampFormat is the start stamp createLogPath writes into a log
// name: millisecond precision, so same-second starts get distinct names that
// read back in start order by parseLogName.
const logTimestampFormat = "20060102-150405.000"

// legacyLogTimestampFormat is the second-precision stamp of logs written
// before logTimestampFormat gained milliseconds; such logs are still parsed.
const legacyLogTimestampFormat = "20060102-150405"

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
		if e.IsDir() || !strings.HasSuffix(e.Name(), logFileSuffix) {
			continue
		}

		path := filepath.Join(logDir, e.Name())
		if active[path] {
			continue
		}

		if !deleteAll {
			name, ok := parseLogName(e.Name())
			if !ok {
				continue
			}
			if now.Sub(name.started) < maxAge {
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

// logName is the name of a launcher-managed log file, split into the parts
// the launcher reads back: the backend that wrote it, the port of the
// instance it belongs to, the Profile it was started for and its start time.
// Three shapes exist on disk:
//
//	{backend}-{port}-{profile}-{stamp}.log  a start for a Profile
//	{backend}-{port}-{stamp}.log            a start with no Profile (ollama serve)
//	{backend}-{stamp}.log                   legacy, written before the port was named
//
// The stamp is logTimestampFormat, or legacyLogTimestampFormat on older logs.
// Backend names never contain '-', and the Profile part is a logProfileSlug,
// so a name splits back into its parts unambiguously.
type logName struct {
	// backend is the LLM Server's name (llamacpp, ollama, ...).
	backend string
	// port is the instance's port; 0 on a legacy name, which records none.
	port int
	// profile is the filename-safe Profile name; "" when the log has none.
	profile string
	// started is the start time the stamp records.
	started time.Time
}

// logFileSuffix is the extension every launcher-managed log name ends with.
const logFileSuffix = ".log"

// fileName formats n as a log file name. A positive port is written after the
// backend and the Profile after the port; a name without a port is the legacy
// shape, so a Profile is only written alongside a port.
func (n logName) fileName() string {
	parts := []string{n.backend}
	if n.port > 0 {
		parts = append(parts, strconv.Itoa(n.port))
		if n.profile != "" {
			parts = append(parts, n.profile)
		}
	}
	parts = append(parts, n.started.Format(logTimestampFormat))
	return strings.Join(parts, "-") + logFileSuffix
}

// parseLogName reads a log file name in any of logName's three shapes. It
// reports false for a name that is not a launcher-managed log: no .log
// suffix, no backend, a non-numeric port or an unparsable stamp.
func parseLogName(filename string) (logName, bool) {
	stem, isLog := strings.CutSuffix(filename, logFileSuffix)
	if !isLog {
		return logName{}, false
	}
	parts := strings.Split(stem, "-")
	if len(parts) < 3 || parts[0] == "" {
		return logName{}, false
	}
	started, ok := parseLogStamp(parts[len(parts)-2] + "-" + parts[len(parts)-1])
	if !ok {
		return logName{}, false
	}
	name := logName{backend: parts[0], started: started}
	owner := parts[1 : len(parts)-2]
	if len(owner) == 0 {
		return name, true
	}
	port, err := strconv.Atoi(owner[0])
	if err != nil || port <= 0 {
		return logName{}, false
	}
	name.port = port
	// A Profile slug may itself contain '-', so its parts are rejoined.
	name.profile = strings.Join(owner[1:], "-")
	return name, true
}

// parseLogStamp parses a log name's start stamp at millisecond precision, or
// at the second precision of logs written before milliseconds were added.
func parseLogStamp(stamp string) (time.Time, bool) {
	if t, err := time.Parse(logTimestampFormat, stamp); err == nil {
		return t, true
	}
	if t, err := time.Parse(legacyLogTimestampFormat, stamp); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// logProfileSlug makes a Profile name safe for a log file name: every rune
// outside [A-Za-z0-9._-] becomes '_', so no path separator, space or shell
// metacharacter reaches the file system.
func logProfileSlug(profileName string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, profileName)
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
// Returns an empty map when cfg is nil — nothing can be discovered without a
// config, so callers must pass one for the protection to apply.
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

// autoCleanupLogs silently removes logs older than cfg.LogRetention days
// before a new log file is created. An unset or non-positive retention
// disables cleanup entirely — 0 must never mean "delete everything". Passing
// cfg through to cleanupLogs keeps the logs of running servers protected on
// the automatic path, exactly as on the manual `logs clean` path.
func autoCleanupLogs(cfg *Config) {
	if cfg.LogRetention == nil || *cfg.LogRetention <= 0 {
		return
	}
	maxAge := time.Duration(*cfg.LogRetention) * 24 * time.Hour
	cleanupLogs(cfg, cfg.LogDir, maxAge, false)
}
