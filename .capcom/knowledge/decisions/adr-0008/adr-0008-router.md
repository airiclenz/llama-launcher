---
title: "ADR-0008 why it does not contradict ADR-0002: the adapter is a control plane, and the CLI binary stays listener-free"
---

ADR-0002 says llml has "no HTTP server of its own and no client-facing API surface," because adding a proxy layer would couple llml to the request lifecycle of every supported LLM Server and contradict the one-shot, zero-resident-memory design.

That reasoning is about the **data plane** — `/v1/chat/completions` traffic. The MCP adapter is a **control plane**: it forwards `load` / `unload` / `start` / `stop` / `status` / `list`, exactly the surface a human or the `manage-llm-server` skill already drives. It holds no Models, parses no inference requests, and is not coupled to any LLM Server's request lifecycle. The CLI binary itself remains listener-free, so ADR-0002's literal guarantee is intact.

Keeping it a **separate binary** (rather than a `llama-launcher serve` subcommand) preserves that property visibly: nothing in the `llama-launcher` build opens a socket. The adapter is co-located in the repo (`cmd/llama-launcher-mcp`) only for shared versioning and distribution.
