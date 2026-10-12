---
title: "ADR-0001 decision: stop signals the listener at the instance's address and runs the type's stop hook, whoever started the server"
---

`llama-launcher` does not distinguish between LLM Servers it started and LLM Servers that were already running. Any `stop` (whether triggered by the user via `llml stop` or implicitly via `auto_stop_server: true` when switching backends) terminates the server: the process listening at the instance's `host:port` is signalled (SIGTERM, escalating to SIGKILL), and the LLM Server type's own stop hook runs where one exists (`lms server stop` for `lmstudio`). Ollama has no usable stop hook — its CLI has no server-stop command (`ollama stop MODEL` only unloads a model), so the address-scoped signal *is* its stop mechanism; the launcher deliberately does not sweep every `ollama serve` process on the host, which would kill instances at other addresses it was not asked to stop (see ADR-0006).
