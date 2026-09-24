# Gateway: go/no-go decision brief

Source: [`docs/adr/0013-gateway-data-plane-sibling.md`](0013-gateway-data-plane-sibling.md). This brief restates that design for a build decision and does not change it.

## What the Gateway is

The Gateway is an optional, resident sibling binary in this repo. It exposes one OpenAI-compatible endpoint and treats each request's `"model"` field as a Profile name. If that Profile is not serving, the Gateway activates it through the public library facade (ADR-0011), queues the request during activation, and then proxies inference to the serving LLM Server. The first cut is single-host, orders its queue by an `interactive`/`background` hint, and runs Profiles side by side when their declared footprints fit a global budget. The `llama-launcher` CLI itself does not change: it stays one-shot and listener-free.

## Cost

- **A new resident binary.** It is a second exception to the zero-resident-memory goal, after the MCP adapter. It would ship through the tap beside the CLI and the adapter.
- **New configuration surface.** The config gains a Gateway section (listen address, global memory budget, stickiness window N), and Profiles gain an optional declared footprint.
- **A scoped ADR-0002 exception.** The Gateway is the cross-server proxying that ADR-0002 declined, and it knowingly takes on data-plane coupling. The scope has two limits: the Gateway binary only, and only the `/v1/*` OpenAI-compatible surface that all three LLM Server types share. The server's full native API is never proxied.

## Benefit

Any ordinary OpenAI-compatible tool (a chat UI, an editor plugin, a script) can request a Profile by name at one stable address. The host loads that Profile, and the request waits in the queue during a swap instead of failing with a connection error. Today, a request for an unloaded Model fails until someone runs `llml load`. Most of the value falls on `llamacpp`. Ollama and LM Studio already dispatch multiple Models internally, so for those backends the Gateway mainly adds the priority queue and the single port.

## Today's alternative: the MCP adapter

`llama-launcher-mcp` (ADR-0008) is a control plane only. It exposes lifecycle tools (`load_profile`, `unload_model`, `start_server`, `stop_server`, `server_status`, `list_profiles`, `tail_log`) and runs each one by shelling out to the CLI. It never proxies inference. Before any untrusted `target` reaches the CLI, the adapter checks it against a positive allowlist. Only an empty value, a known backend name, or a `host:port` with a valid port is accepted. This keeps flag injection, subcommand injection and shell metacharacters out of the shell-out, without treating the CLI's argument grammar as a security boundary (`cmd/llama-launcher-mcp/validate.go`). An MCP-capable agent can therefore load a Profile and then call the server's native address. Ordinary OpenAI clients cannot do this: they do not speak MCP, and they get no queue during a swap.

## Open items the ADR leaves to implementation

- **Stickiness window N.** The ADR says it is set in config but gives no default.
- **Memory budget.** The ADR defines the global budget and its arithmetic but gives no default and no unit convention.
- **Per-Profile footprint declaration.** It is optional, and an undeclared Profile consumes the whole budget, which means one Profile at a time. The declaration's format and the path to auto-detection are open.
- **Non-loopback trust.** The first cut binds loopback or the bridge. If the Gateway is ever exposed beyond that, ADR-0008's IP allowlist is the prior art.
- **API-key handling on the proxy path.** ADR-0013 does not cover this. Three questions are open: whether the Gateway presents each Profile's resolved key to the upstream server, whether clients must present a key to the Gateway, and how keys stay out of Gateway logs. The unconditional log redaction in `internal/launcher/redact.go` (`RedactLogText`) is the prior art for the last question.

## Recommendation

**Go, gated on design.** The benefit is not available today, and the cost is scoped by the sibling-binary pattern that already works for the MCP adapter. Before writing code, settle API-key handling and pick defaults for N and the memory budget, so the first cut does not ship an unspecified trust or eviction surface.

## Decision requested

Choose one:

1. **Go.** Build the Gateway as ADR-0013 describes, once the open items above are settled.
2. **No-go.** Keep the MCP adapter as the only remote surface, and mark ADR-0013 as superseded.
3. **Defer.** Leave ADR-0013 decided but unbuilt, and revisit it when a non-MCP client needs on-demand switching.
