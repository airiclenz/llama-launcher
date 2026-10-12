---
title: "ADR-0002 consequences: clients talk to the LLM Server's address directly and see connection errors across a restart"
---

- llml has no HTTP server of its own and no client-facing API surface.
- Clients configure their tools (e.g. an IDE plugin) to point at the LLM Server's address directly. When llml stops or restarts that server, clients get connection errors until the new server is up — this is intentional and matches the model.
- llml may still tell an LLM Server to hold multiple Models (for backends where that works well, e.g. LM Studio, Ollama). That is server-internal dispatch, not routing — see point 1 above.
