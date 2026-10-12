---
title: "ADR-0013 consequences: a second resident sibling, a Gateway config section, declared Profile footprints and a tap entry"
---

- A second resident sibling process joins the repo — like the MCP adapter, a conscious, documented exception to the zero-resident-memory goal, opt-in and inert unless started. The CLI build remains listener-free; ADR-0002's literal guarantees are intact and its plain-word "router" stays reserved (the Gateway is named **Gateway**).
- Clients may now point at the Gateway's address instead of an LLM Server's native one. Nothing changes for clients that don't: direct native addresses keep working exactly as ADR-0002 describes.
- The config grows a Gateway section (listen address, global memory budget, stickiness window N) and Profiles gain an optional declared footprint.
- The Gateway binary joins the tap/distribution beside the CLI and the MCP adapter.
- Trust surface: v1 binds loopback/bridge like the MCP adapter; if it is ever exposed beyond that, ADR-0008's allowlist reasoning applies to it as prior art.
- **Gateway** enters `CONTEXT.md` as a canonical term.
