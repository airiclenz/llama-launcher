package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const usage = `Usage: llama-launcher-mcp [flags]

Exposes llama-launcher's lifecycle commands as MCP tools over HTTP for a
container-to-host control plane. Runs on the host; shells out to the CLI.

Flags:
  --listen host:port        Bind address. Use the container-facing bridge IP,
                            not 0.0.0.0. (default 127.0.0.1:7331)
  --allow ip|cidr|host      Allow this client IP, CIDR, or hostname. Repeatable.
                            A hostname is resolved to its IPs once at startup
                            (restart if the IP later changes). When neither this
                            nor --allow-interface is given, only loopback is
                            allowed.
  --allow-interface name    Allow the subnet(s) of this local interface, e.g.
                            bridge100 (the container-facing bridge). Repeatable.
                            Covers any IP the bridge hands the container, so you
                            never need to know or pin it.
  --llama-launcher-bin path  Path to the llama-launcher CLI (default: PATH lookup).
  --config path             llama-launcher config path, forwarded to each call.
  --read-only               Expose only read tools (no load/unload/start/stop).
  --version                 Print version and exit.`

type config struct {
	listen           string
	allow            []allowEntry
	llamaLauncherBin string
	configPath       string
	readOnly         bool
}

func parseFlags(argv []string) (*config, error) {
	cfg := &config{
		listen:           "127.0.0.1:7331",
		llamaLauncherBin: "llama-launcher",
	}
	var allowSpecs []string
	var ifaceSpecs []string

	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		next := func() (string, error) {
			if i+1 >= len(argv) {
				return "", fmt.Errorf("%s requires a value", arg)
			}
			i++
			return argv[i], nil
		}
		switch arg {
		case "--listen":
			v, err := next()
			if err != nil {
				return nil, err
			}
			cfg.listen = v
		case "--allow":
			v, err := next()
			if err != nil {
				return nil, err
			}
			allowSpecs = append(allowSpecs, v)
		case "--allow-interface":
			v, err := next()
			if err != nil {
				return nil, err
			}
			ifaceSpecs = append(ifaceSpecs, v)
		case "--llama-launcher-bin":
			v, err := next()
			if err != nil {
				return nil, err
			}
			cfg.llamaLauncherBin = v
		case "--config":
			v, err := next()
			if err != nil {
				return nil, err
			}
			cfg.configPath = v
		case "--read-only":
			cfg.readOnly = true
		case "--version", "-v":
			fmt.Println(Version)
			// Signal a clean exit to the caller via a sentinel error.
			return nil, errVersion
		case "-h", "--help":
			return nil, errHelp
		default:
			return nil, fmt.Errorf("unknown flag %q", arg)
		}
	}

	allow, err := resolveAllowlist(allowSpecs, ifaceSpecs)
	if err != nil {
		return nil, err
	}
	cfg.allow = allow

	return cfg, nil
}

var (
	errVersion = errors.New("version requested")
	errHelp    = errors.New("help requested")
)

// maxCapturedOutput caps how many bytes of the CLI's stdout and stderr (each)
// run retains, so a runaway subprocess cannot grow the adapter's memory — and
// the MCP response — without bound. 1 MiB comfortably covers the largest
// legitimate output (profile/status JSON, log tails).
const maxCapturedOutput = 1 << 20

// truncationNotice is appended to a stream's returned content when it hit
// maxCapturedOutput, so the caller knows the output is incomplete.
const truncationNotice = "[output truncated: 1MiB cap reached]"

// limitedWriter retains at most limit bytes of what is written to it and
// discards the rest, recording that truncation happened. Write always reports
// the full input as consumed and never returns an error, so the subprocess
// keeps running (and exits on its own terms) after the cap is hit instead of
// dying on a broken pipe.
type limitedWriter struct {
	b         strings.Builder
	limit     int
	truncated bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if room := w.limit - w.b.Len(); n > room {
		w.truncated = true
		p = p[:room]
	}
	w.b.Write(p)
	return n, nil
}

// text returns the retained content trimmed of trailing newlines, with the
// truncation notice appended when the cap was hit.
func (w *limitedWriter) text() string {
	s := strings.TrimRight(w.b.String(), "\n")
	if w.truncated {
		s += "\n" + truncationNotice
	}
	return s
}

// run executes `llama-launcher [--config path] <args...>` and maps the result
// to an MCP tool result. stdout is returned as the tool's text content; when
// the command exits non-zero with no stdout, the result is flagged as an error
// carrying stderr. Several subcommands (e.g. `status`) exit non-zero to signal
// an informational negative while still printing valid JSON on stdout — those
// are returned as normal content so the caller keeps the data. Each captured
// stream is capped at maxCapturedOutput; content past the cap is dropped and
// a truncation notice is appended.
func (c *config) run(ctx context.Context, args ...string) *mcp.CallToolResult {
	full := args
	if c.configPath != "" {
		full = append([]string{"--config", c.configPath}, args...)
	}

	cmd := exec.CommandContext(ctx, c.llamaLauncherBin, full...)
	stdout := &limitedWriter{limit: maxCapturedOutput}
	stderr := &limitedWriter{limit: maxCapturedOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()

	out := stdout.text()
	errOut := stderr.text()

	if err != nil && out == "" {
		msg := errOut
		if msg == "" {
			msg = err.Error()
		}
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		}
	}

	text := out
	if errOut != "" {
		if text != "" {
			text += "\n"
		}
		text += errOut
	}
	if text == "" {
		text = "(no output)"
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func readOnlySuffix(ro bool) string {
	if ro {
		return "  (read-only)"
	}
	return ""
}
