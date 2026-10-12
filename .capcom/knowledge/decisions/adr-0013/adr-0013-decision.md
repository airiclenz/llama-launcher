---
title: "ADR-0013 decision: a separate resident Gateway binary activates Profiles on demand and proxies inference, and the CLI is untouched"
---

An optional, resident **Gateway** binary — co-located in this repo like the MCP adapter — exposes one OpenAI-compatible endpoint. It treats each request's `"model"` field as a **Profile** name: if that Profile is not serving, the Gateway activates it through the public library facade (ADR-0011), queues the request during activation, and then proxies inference to the serving LLM Server. `llama-launcher` (the CLI) is untouched: one-shot, listener-free, zero resident memory.

Decided 2026-08-05 in a cross-repo design grill with apogee (its ADR 0034 records the demand side); not yet built.
