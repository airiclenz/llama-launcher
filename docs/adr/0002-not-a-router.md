# llama-launcher is a process manager, not a request router

`llama-launcher` starts and stops LLM Servers and asks them to load and unload Models. It does not expose its own HTTP endpoint, does not proxy or route client requests, and does not select a Model on a client's behalf. Clients always talk to an LLM Server directly via its native address and pick the Model in the request body (as they would when using LM Studio or Ollama without llml).

## Why

"Routing" in this domain has two meanings that are easy to conflate:

1. **Server-internal dispatch.** When LM Studio, Ollama, or `llama-server`'s router mode holds several Models in one process, the server reads the `"model"` field on each incoming request and dispatches inference to the right Model. This is the server's job, not llml's.
2. **Cross-server proxying.** A separate process accepts client requests and forwards them to whichever LLM Server currently hosts the right Model.

llml does neither. Future contributors will eventually wonder "should llml expose `/v1/chat/completions` and route to the active backend?" — this ADR exists so the answer is "no, by design." Adding a proxy layer would couple llml to the request lifecycle of every supported LLM Server and contradicts the one-shot, zero-resident-memory design goal.

## Consequences

- llml has no HTTP server of its own and no client-facing API surface.
- Clients configure their tools (e.g. an IDE plugin) to point at the LLM Server's address directly. When llml stops or restarts that server, clients get connection errors until the new server is up — this is intentional and matches the model.
- llml may still tell an LLM Server to hold multiple Models (for backends where that works well, e.g. LM Studio, Ollama). That is server-internal dispatch, not routing — see point 1 above.
