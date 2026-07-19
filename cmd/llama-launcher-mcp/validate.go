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

// targetCharset is the only shape a target may take before further checks:
// letters, digits, and the punctuation of a backend name or a (bracketed
// IPv6) host:port. Everything else — spaces, shell metacharacters, control
// bytes — fails immediately.
var targetCharset = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]+$`)

// validateTarget vets an untrusted free-form target before it is forwarded
// as a positional argument to the CLI. Only a positive allowlist passes:
// empty (the CLI auto-selects the single running instance), a known backend
// name, or a host:port with a valid port number. This keeps flag injection
// ("-f", "--days"), subcommand injection ("clean"), and shell metacharacters
// out of the shell-out — the CLI's own argument grammar is not relied on as
// a security boundary. It matters most for tail_log, which stays exposed
// under --read-only and must never reach a mutating or blocking CLI path.
func validateTarget(target string) error {
	if target == "" {
		return nil
	}
	if targetCharset.MatchString(target) && !strings.HasPrefix(target, "-") {
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

// toolError wraps a rejected input as an MCP tool error result.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
