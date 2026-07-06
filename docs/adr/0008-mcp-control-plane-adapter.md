# Remote control runs through a separate MCP adapter, not a listener in the CLI

`llama-launcher` (the CLI) stays a one-shot process manager with no network surface. When a client on another machine — typically a containerized coding agent — needs to control which Model is running on the host, that control is provided by a **separate, optional binary**, `llama-launcher-mcp`, which exposes the existing lifecycle commands as MCP tools over HTTP and implements each by shelling out to the CLI. The adapter dispatches *control* commands only; it never proxies inference traffic.

## Why this does not contradict ADR-0002

[ADR-0002](0002-not-a-router.md) says llml has "no HTTP server of its own and no client-facing API surface," because adding a proxy layer would couple llml to the request lifecycle of every supported LLM Server and contradict the one-shot, zero-resident-memory design.

That reasoning is about the **data plane** — `/v1/chat/completions` traffic. The MCP adapter is a **control plane**: it forwards `load` / `unload` / `start` / `stop` / `status` / `list`, exactly the surface a human or the `manage-llm-server` skill already drives. It holds no Models, parses no inference requests, and is not coupled to any LLM Server's request lifecycle. The CLI binary itself remains listener-free, so ADR-0002's literal guarantee is intact.

Keeping it a **separate binary** (rather than a `llama-launcher serve` subcommand) preserves that property visibly: nothing in the `llama-launcher` build opens a socket. The adapter is co-located in the repo (`cmd/llama-launcher-mcp`) only for shared versioning and distribution.

## Why shell out instead of importing the package

The adapter maps each MCP tool to a `llama-launcher <subcommand> --json` invocation and returns the output. The CLI already emits purpose-built JSON (`status --json`, `list --json`) designed for exactly this kind of consumption. Shelling out keeps the adapter a thin, auditable shim, decouples it from llml's internals, and lets it run against whatever installed version is on the host. Call volume is a handful of commands per session, so subprocess overhead is irrelevant. Importing `internal/launcher` in-process was rejected: it would pull llml's internals into a network-facing process for a latency win that does not matter here.

## Trust model: IP allowlist, no secret

The driving constraint is that the remote client is a **cloud LLM agent that must not be given credentials** — anything in the container's filesystem or environment is effectively handed to the model. An SSH key or bearer token placed in the container fails this test.

The adapter therefore authenticates by **source IP allowlist** (`--allow <ip|cidr|host>`, repeatable; a hostname is resolved to its addresses once at startup — or `--allow-interface <name>`, which allows the subnet(s) of a local interface such as the bridge, so any IP the bridge assigns the container is covered without knowing or pinning it, and a private bridge subnet cannot collide with a public hostname) and is meant to **bind the container-facing bridge interface** (`--listen`), not `0.0.0.0`. The client receives no secret it could leak; access is "you are the container, on the private bridge." The IP check is defense-in-depth layered on the narrow bind, not a replacement for it. When no `--allow` is given the allowlist defaults to loopback only, so an accidentally-started adapter is never exposed.

A `--read-only` flag exposes only the read tools (`list_profiles`, `server_status`, `tail_log`) for containers that should observe but not mutate. Judgment rules that require context (e.g. "never swap Models mid-simulation") stay with the agent via the `manage-llm-server` skill; the adapter exposes the mutating tools plainly.

Because the client is assumed prompt-injectable, tool inputs are treated as hostile. Every free-form value the adapter forwards to the CLI as a positional (`target` on `tail_log`/`stop_server`, `profile` on `unload_model`) passes through a single validation gate that rejects values starting with `-` or matching a CLI subcommand keyword, returning an MCP tool error without invoking the CLI. Without this, `tail_log{"target":"clean"}` would run the destructive `logs clean` through the read surface, and `tail_log{"target":"-f"}` would block the adapter on a `logs --follow` that never returns. The gate lives in the adapter; the CLI's own argument parsing is unchanged.

## Consequences

- A small **resident control-plane process** now runs on the host — a conscious, documented exception to the zero-resident-memory goal. It is separate from `llama-launcher`, holds no Models, and only dispatches control commands.
- One new dependency (`github.com/modelcontextprotocol/go-sdk`) enters the module, scoped to the adapter binary; the core CLI build stays dependency-light.
- Allowlist strength depends on the bridge network not permitting source-IP spoofing from other reachable hosts; mitigated by binding narrowly and pinning a stable container IP.
- Adding control tools means extending the adapter's tool surface (`cmd/llama-launcher-mcp`), never adding a listener to the CLI.
