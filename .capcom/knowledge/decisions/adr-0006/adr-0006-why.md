---
title: "ADR-0006 why: restart-per-Profile needs several llama-server processes, and the address is an instance's only natural identity"
---

After ADR-0003 (`llamacpp` is restart-per-Profile), the only way to have two Models loaded on `llamacpp` at the same time is to run two `llama-server` processes on different ports. The earlier state-file model — one file per LLM Server *type* (`state-llamacpp.json`) — made this impossible: the second instance would overwrite the first's state. Keying state by address instead of by type unblocks the use case and generalises cleanly: Ollama and LM Studio, which normally run a single instance per machine, behave exactly as before (one address, one state record).

The address is the only natural identity for an instance. It is what the user puts in the Profile, what the OS uses to multiplex (port binding), and what a client uses to reach the server. Profile name is *not* the right key: a Profile is a recipe, and the same recipe could be re-activated against the same address repeatedly without creating a new instance.
