package main

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// knownBackends mirrors the backend names registered in internal/launcher
// (backend_*.go). The adapter deliberately does not import that package —
// ADR-0008 keeps it a thin shell-out shim — so the names are pinned here.
var knownBackends = map[string]bool{
	"llamacpp": true,
	"lmstudio": true,
	"ollama":   true,
}

// targetCharset is the only shape an untrusted positional argument may take
// before further checks: letters, digits, and the punctuation of a backend
// name, a (bracketed IPv6) host:port, or a profile name. Everything else —
// spaces, shell metacharacters, control bytes — fails immediately.
var targetCharset = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]+$`)

// hasSafeShape reports whether an untrusted string consists only of
// allowlisted characters and cannot be mistaken for a flag. It is the shared
// core of every positional-argument check in this file.
func hasSafeShape(s string) bool {
	return targetCharset.MatchString(s) && !strings.HasPrefix(s, "-")
}

// validateTarget vets an untrusted free-form target before it is forwarded
// as a positional argument to the CLI. Only a positive allowlist passes:
// empty (the CLI auto-selects the single discovered instance), a known backend
// name, or a host:port with a valid port number. This keeps flag injection
// ("-f", "--days"), subcommand injection ("clean"), and shell metacharacters
// out of the shell-out — the CLI's own argument grammar is not relied on as
// a security boundary. It matters most for tail_log, which stays exposed
// under --read-only and must never reach a mutating or blocking CLI path;
// stop_server forwards its target through the same check because `stop`
// shares the `logs` target grammar.
func validateTarget(target string) error {
	if target == "" {
		return nil
	}
	if hasSafeShape(target) {
		if knownBackends[target] {
			return nil
		}
		if host, port, err := net.SplitHostPort(target); err == nil && host != "" {
			if n, err := strconv.Atoi(port); err == nil && n >= 1 && n <= 65535 {
				return nil
			}
		}
	}
	return fmt.Errorf("invalid target %q: must be a backend name (llamacpp, lmstudio, ollama) or host:port", target)
}

// validateProfile vets an untrusted profile name before it is forwarded to
// the CLI, whether as a positional argument (unload_model ->
// `unload [profile]`, load_profile -> `load <name>`) or as a flag value
// (start_server -> `start --profile <profile>`, where a leading dash could
// be read as another flag).
// Profile names are user-defined in a config the adapter deliberately does
// not parse (ADR-0008), so no pinned allowlist of names exists; the charset
// allowlist plus the no-leading-dash rule keeps flag-shaped arguments,
// extra words, and shell metacharacters out of the shell-out, and resolving
// the name is left to the CLI, which fails cleanly on an unknown or empty
// profile.
func validateProfile(profile string) error {
	if profile == "" {
		return nil
	}
	if hasSafeShape(profile) {
		return nil
	}
	return fmt.Errorf("invalid profile %q: only letters, digits, and . _ : - are allowed, and it must not start with -", profile)
}

// toolError wraps a rejected input as an MCP tool error result.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
