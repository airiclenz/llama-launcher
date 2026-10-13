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
- `adr-0009`: Activation orchestration drives an operations seam
- `adr-0010`: A Starting LLM Server is visible and stoppable; only `--restart` displaces it
- `adr-0011`: A curated public facade makes the launcher importable as a library
- `adr-0012`: The library compiles everywhere and actuates where it can
- `adr-0013`: On-demand Model serving runs through a separate Gateway binary, not the CLI
- `adr-0014`: A Splash Model must already be installed; the launcher never downloads one
- `adr-0015`: A loading Splash is found by its process, not by its address
- `adr-0016`: api_key_cmd runs only from a config the user owns
- `adr-0017`: The repo owns its release pipeline: cmd/release publishes prebuilt archives and a prebuilt Homebrew formula
