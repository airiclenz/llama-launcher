// Command llama-launcher-mcp is an optional, host-side control-plane adapter
// that exposes llama-launcher's lifecycle commands as MCP tools over HTTP.
//
// It runs on the host (where llama-launcher and the LLM servers live) and
// implements every tool by shelling out to the installed llama-launcher CLI.
// It never proxies inference traffic — it only dispatches control commands —
// so llama-launcher itself keeps no listener and ADR-0002 ("not a router")
// stays intact (see docs/adr/0008-mcp-control-plane-adapter.md).
//
// Access is gated by an IP allowlist plus a narrow bind to the container-facing
// interface; no token or key is required, so a containerized client (e.g. a
// cloud LLM agent) receives no secret it could leak.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is stamped at build time via -ldflags (see Makefile).
var Version = "dev"

func main() {
	cfg, err := parseFlags(os.Args[1:])
	switch {
	case err == errVersion:
		return // version already printed
	case err == errHelp:
		fmt.Println(usage)
		return
	case err != nil:
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	// exec.LookPath resolves both bare names (via PATH) and explicit paths.
	if _, err := exec.LookPath(cfg.llamaLauncherBin); err != nil {
		fmt.Fprintf(os.Stderr, "Error: llama-launcher CLI %q not found (set --llama-launcher-bin): %v\n", cfg.llamaLauncherBin, err)
		os.Exit(2)
	}

	server := newServer(cfg)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)

	mux := http.NewServeMux()
	mux.Handle("/mcp", allowlistMiddleware(cfg.allow, mcpHandler))

	fmt.Printf("llama-launcher-mcp %s listening on http://%s/mcp\n", Version, cfg.listen)
	fmt.Printf("  allow: %s\n", describeAllow(cfg.allow))
	fmt.Printf("  cli:   %s%s\n", cfg.llamaLauncherBin, readOnlySuffix(cfg.readOnly))

	srv := &http.Server{Addr: cfg.listen, Handler: mux}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// newServer builds the MCP server and registers the tool surface. Read tools
// are always available; mutating tools are omitted entirely when --read-only.
func newServer(cfg *config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "llama-launcher",
		Version: Version,
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_profiles",
		Description: "List the configured llama-launcher profiles (name, backend, model, parameters) as JSON.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		return cfg.run(ctx, "list", "--json"), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "server_status",
		Description: "Report each enabled backend's status (running, address, active_profile, active_model, pid, uptime_seconds) as JSON.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		return cfg.run(ctx, "status", "--json"), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tail_log",
		Description: "Return the tail of a running instance's log. Target is optional when exactly one server is running; otherwise pass a backend name or host:port.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args targetArgs) (*mcp.CallToolResult, any, error) {
		return cfg.runWithPositional(ctx, "logs", "target", args.Target), nil, nil
	})

	if cfg.readOnly {
		return s
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "load_profile",
		Description: "Activate a profile (start the server if needed and load its model). Idempotent: a no-op if already active unless restart is true.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args loadArgs) (*mcp.CallToolResult, any, error) {
		cmd := []string{"load", args.Name}
		if args.Restart {
			cmd = append(cmd, "--restart")
		}
		return cfg.run(ctx, cmd...), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "unload_model",
		Description: "Unload the model (for managed backends this stops the server). Profile is optional when only one model is loaded.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args profileArgs) (*mcp.CallToolResult, any, error) {
		return cfg.runWithPositional(ctx, "unload", "profile", args.Profile), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "start_server",
		Description: "Start a server, optionally with a profile. Without a profile it starts the default backend with no model loaded.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args profileArgs) (*mcp.CallToolResult, any, error) {
		cmd := []string{"start"}
		if args.Profile != "" {
			cmd = append(cmd, "--profile", args.Profile)
		}
		return cfg.run(ctx, cmd...), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "stop_server",
		Description: "Stop a running server. Target is optional when exactly one server is running; otherwise pass a backend name or host:port.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args targetArgs) (*mcp.CallToolResult, any, error) {
		return cfg.runWithPositional(ctx, "stop", "target", args.Target), nil, nil
	})

	return s
}

// Tool input schemas. Empty struct => no arguments.
type noArgs struct{}

type loadArgs struct {
	Name    string `json:"name" jsonschema:"the profile name to activate"`
	Restart bool   `json:"restart,omitempty" jsonschema:"force a restart even if the profile is already active"`
}

type profileArgs struct {
	Profile string `json:"profile,omitempty" jsonschema:"optional profile name"`
}

type targetArgs struct {
	Target string `json:"target,omitempty" jsonschema:"optional backend name or host:port"`
}

// argsFor builds a subcommand line, appending the optional positional argument
// only when it is non-empty.
func argsFor(sub, arg string) []string {
	if arg == "" {
		return []string{sub}
	}
	return []string{sub, arg}
}

// runWithPositional is the single gate for tools that forward free-form input
// to the CLI as a positional (tail_log, stop_server, unload_model). It rejects
// values the CLI would parse as something other than a plain target/profile —
// returning an MCP tool error without invoking the CLI — so no tool input can
// select a different subcommand (e.g. turn the read-only tail_log into the
// destructive `logs clean`) or trigger flag behaviour (`logs -f` follows
// forever, blocking the adapter). Safe values are forwarded unchanged.
func (c *config) runWithPositional(ctx context.Context, sub, field, value string) *mcp.CallToolResult {
	if err := validatePositional(field, value); err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		}
	}
	return c.run(ctx, argsFor(sub, value)...)
}

// launcherSubcommands are the llama-launcher CLI subcommand keywords. `logs`
// treats a "clean" positional as its destructive sub-subcommand; the rest are
// rejected defensively so a forwarded positional can never be read as a
// command word by any current or future CLI parsing.
var launcherSubcommands = map[string]bool{
	"load": true, "unload": true, "start": true, "stop": true,
	"status": true, "list": true, "logs": true, "clean": true,
	"validate": true, "init": true, "reset": true,
}

// validatePositional rejects a tool-supplied value that the CLI would misread
// as a flag or a subcommand keyword instead of a plain positional argument.
// An empty value is valid: the positional is optional on every tool.
func validatePositional(field, value string) error {
	switch {
	case value == "":
		return nil
	case strings.HasPrefix(value, "-"):
		return fmt.Errorf("invalid %s %q: must not start with \"-\"", field, value)
	case launcherSubcommands[value]:
		return fmt.Errorf("invalid %s %q: launcher subcommand keywords are not accepted", field, value)
	}
	return nil
}
