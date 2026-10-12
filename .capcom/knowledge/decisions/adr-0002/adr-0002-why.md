---
title: "ADR-0002 why: routing means server-internal dispatch or cross-server proxying, and the launcher does neither by design"
---

"Routing" in this domain has two meanings that are easy to conflate:

1. **Server-internal dispatch.** When LM Studio, Ollama, or `llama-server`'s router mode holds several Models in one process, the server reads the `"model"` field on each incoming request and dispatches inference to the right Model. This is the server's job, not llml's.
2. **Cross-server proxying.** A separate process accepts client requests and forwards them to whichever LLM Server currently hosts the right Model.

llml does neither. Future contributors will eventually wonder "should llml expose `/v1/chat/completions` and route to the active backend?" — this ADR exists so the answer is "no, by design." Adding a proxy layer would couple llml to the request lifecycle of every supported LLM Server and contradicts the one-shot, zero-resident-memory design goal.
