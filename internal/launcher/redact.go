package launcher

import (
	"regexp"
	"sort"
	"strings"
)

// redactionMark replaces every secret RedactLogText masks. It is the same
// marker internal/keystore uses for key text folded into errors, so a user
// sees one spelling for "a key was here" wherever the launcher prints one.
const redactionMark = "[redacted]"

// apiKeyFlagPattern matches an --api-key flag followed by its value, in the
// spellings argv takes when a server or wrapper echoes its command line:
// `--api-key VALUE`, `--api-key=VALUE`, either of those with the flag or value
// quoted, and a quoted list element pair (`"--api-key", "VALUE"`). The value
// stops at whitespace or a quote, so the closing quote of a quoted value
// survives. The flag must be followed by `=`, a comma or blanks, so
// `--api-key-file PATH` is not a match. Blanks exclude the newline: a flag at
// the end of one log line never swallows the first word of the next.
var apiKeyFlagPattern = regexp.MustCompile(`(--api-key["']?(?:=|[ \t]*,[ \t]*|[ \t]+)["']?)[^\s"']+`)

// RedactLogText returns text with every known API key masked, for any log text
// the launcher shows a user. It masks (a) every non-blank entry of keys wherever
// it appears, and (b) the value following any --api-key flag occurrence, whether
// or not keys is empty — a key supplied through a profile's extra_args is valid
// on llama-server alongside the configured one, and it is not in keys.
//
// Redaction is unconditional: no current llama-server build echoes its keys to
// its log, but log text reaches terminals, pasted bug reports and remote MCP
// clients, and a future build or a wrapper script that does echo them must not
// turn every log view into a leak.
func RedactLogText(text string, keys []string) string {
	// Longest first, so a key that contains another key is masked whole
	// rather than leaving the unmatched remainder of the longer one behind.
	sorted := make([]string, 0, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key != "" {
			sorted = append(sorted, key)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })

	for _, key := range sorted {
		text = strings.ReplaceAll(text, key, redactionMark)
	}
	return apiKeyFlagPattern.ReplaceAllString(text, "${1}"+redactionMark)
}
