---
title: "ADR-0013 why a scoped exception to ADR-0002: the Gateway is the cross-server proxying ADR-0002 declined, scoped to its own binary and the OpenAI surface"
---

ADR-0002 exists so that "should llml expose `/v1/chat/completions` and route to the active backend?" is answered "no, by design" — for the CLI. That answer stands: nothing in the `llama-launcher` build opens a socket or touches a request body.

The Gateway is exactly the **cross-server proxying** ADR-0002 declined (its meaning 2), so it cannot hide behind the control-plane defense that covered the MCP adapter (ADR-0008 — "that reasoning is about the data plane"). This ADR accepts the data-plane coupling consciously and scopes it twice: to the **Gateway binary only** (the ADR-0008 pattern — a separate opt-in sibling, so the CLI's guarantees stay visibly intact), and to the **OpenAI-compatible surface** all three LLM Server types share — the Gateway proxies `/v1/*`-shaped traffic, never each server's full native API, which caps the request-lifecycle coupling ADR-0002 warned about.
