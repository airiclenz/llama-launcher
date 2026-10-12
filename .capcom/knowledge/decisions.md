---
title: "Decision records: why llama-launcher works the way it does, one Note per record with its sections as children"
---

llama-launcher's decision records: the choices that shape how the launcher works and why. Each
record is a Note `adr-NNNN` whose body opens with its Status line and a short summary that gives
the reading order of its children: the decision, its reasoning, its consequences and any dated
amendments, each verbatim. A citation "ADR-NNNN" resolves to its record.

The records:

- `adr-0001`: Stop is unconditional, regardless of who started the LLM Server
- `adr-0002`: llama-launcher is a process manager, not a request router
- `adr-0003`: llamacpp uses restart-per-Profile, not multi-Model hosting
- `adr-0004`: `auto_unload` is one rule covering both same-server and cross-server cases
- `adr-0005`: A Profile must name its LLM Server explicitly
- `adr-0006`: LLM Server instances are keyed by `host:port`, not by type
- `adr-0007`: Profile activation is idempotent by name within an address slot
- `adr-0008`: Remote control runs through a separate MCP adapter, not a listener in the CLI
