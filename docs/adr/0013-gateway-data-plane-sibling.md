# On-demand Model serving runs through a separate Gateway binary, not the CLI

An optional, resident **Gateway** binary — co-located in this repo like the MCP adapter — exposes one OpenAI-compatible endpoint. It treats each request's `"model"` field as a **Profile** name: if that Profile is not serving, the Gateway activates it through the public library facade ([ADR-0011](0011-public-library-facade.md)), queues the request during activation, and then proxies inference to the serving LLM Server. `llama-launcher` (the CLI) is untouched: one-shot, listener-free, zero resident memory.

Decided 2026-08-05 in a cross-repo design grill with apogee (its ADR 0034 records the demand side); not yet built.

## Why this is a scoped exception to ADR-0002, not a reversal

[ADR-0002](0002-not-a-router.md) exists so that "should llml expose `/v1/chat/completions` and route to the active backend?" is answered "no, by design" — for the CLI. That answer stands: nothing in the `llama-launcher` build opens a socket or touches a request body.

The Gateway is exactly the **cross-server proxying** ADR-0002 declined (its meaning 2), so it cannot hide behind the control-plane defense that covered the MCP adapter ([ADR-0008](0008-mcp-control-plane-adapter.md) — "that reasoning is about the data plane"). This ADR accepts the data-plane coupling consciously and scopes it twice: to the **Gateway binary only** (the ADR-0008 pattern — a separate opt-in sibling, so the CLI's guarantees stay visibly intact), and to the **OpenAI-compatible surface** all three LLM Server types share — the Gateway proxies `/v1/*`-shaped traffic, never each server's full native API, which caps the request-lifecycle coupling ADR-0002 warned about.

## Why it is worth having standalone

On-demand switching behind one stable endpoint is llama-swap's whole, proven value proposition — independent of any agent: every OpenAI-compatible client (chat UIs, editor plugins, scripts) gets "request any Profile by name and the host makes it so", plus queue-during-swap instead of connection errors. Today a request for an unloaded Model simply fails until a human runs `llml load`. apogee is one client among many; its TUI-vs-daemon contention resolving in the Gateway's queue is just the priority feature below.

The value concentrates on `llamacpp`: Ollama and LM Studio already dispatch multiple Models server-internally (ADR-0002, meaning 1), so for those backends the Gateway adds mainly the priority queue and the single stable port.

## Behavior

- **v1 is single-host.** The Gateway activates Profiles on this machine via the library. Multi-host arrives later as **federation** — the routing table gains remote Gateway entries beside local Profiles — never as remote actuation (no SSH surface).
- **Priority.** Requests carry an `interactive` | `background` hint; absent means `interactive` (an unhinted client is assumed to be a human's tool). The queue orders by priority, then FIFO. apogee's daemon marks its Firings `background`.
- **Eviction: interactive stickiness window.** A Profile that served an interactive request within the last N minutes (config) cannot be displaced by a background request — the background request waits until the window lapses or capacity allows side-by-side. A lapsed window means the human walked away; the background work proceeds. Background-vs-background displacement is plain demand order.
- **Side-by-side under declared budgets.** Profiles may declare a memory footprint and the Gateway config a global budget; activation is side-by-side when the arithmetic fits (concurrent instances are native — [ADR-0006](0006-instances-are-keyed-by-address.md)), displacement otherwise. An undeclared Profile is treated as consuming the whole budget, which degrades to today's one-at-a-time behavior. No estimation code: auto-detected footprints may later replace declarations without changing the policy, and a wrong guess OOMing a serving host is the failure this avoids.

## Why the Gateway imports the library instead of shelling out

ADR-0008 chose shell-out for the MCP adapter: a handful of control commands per session, thin auditable shim. The Gateway sits on the request path: it must watch Starting instances for readiness, derive serving addresses continuously, and make activation decisions per request — polling the CLI for that is the wrong tool. The public facade exists for exactly this class of consumer (ADR-0011), and [ADR-0012](0012-the-library-compiles-everywhere-and-actuates-where-it-can.md)'s posture carries over: the Gateway compiles everywhere and actuates where the library can.

## Consequences

- A second resident sibling process joins the repo — like the MCP adapter, a conscious, documented exception to the zero-resident-memory goal, opt-in and inert unless started. The CLI build remains listener-free; ADR-0002's literal guarantees are intact and its plain-word "router" stays reserved (the Gateway is named **Gateway**).
- Clients may now point at the Gateway's address instead of an LLM Server's native one. Nothing changes for clients that don't: direct native addresses keep working exactly as ADR-0002 describes.
- The config grows a Gateway section (listen address, global memory budget, stickiness window N) and Profiles gain an optional declared footprint.
- The Gateway binary joins the tap/distribution beside the CLI and the MCP adapter.
- Trust surface: v1 binds loopback/bridge like the MCP adapter; if it is ever exposed beyond that, ADR-0008's allowlist reasoning applies to it as prior art.
- **Gateway** enters `CONTEXT.md` as a canonical term.
