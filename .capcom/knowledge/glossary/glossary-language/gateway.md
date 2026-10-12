---
title: "Gateway: the planned resident sibling binary that serves one OpenAI-compatible endpoint and activates Profiles on demand"
---

The optional resident data-plane sibling binary (decided, not yet built — ADR-0013): one OpenAI-compatible endpoint that treats a request's `"model"` field as a Profile name, activates that Profile on demand through the library facade, queues requests during activation (priority-ordered, `interactive` above `background`, with an interactive stickiness window against background displacement), and proxies inference to the serving LLM Server — side-by-side with other Profiles when declared memory budgets fit. A conscious, scoped exception to ADR-0002 for this binary only; the CLI stays a one-shot process manager with no listener.
_Avoid_: "router" / "proxy" as names (ADR-0002 reserves the plain words for what the CLI is *not*; this component is the Gateway), "load balancer" (it serializes onto scarce slots; it does not balance replicas).
