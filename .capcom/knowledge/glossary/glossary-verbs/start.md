---
title: "Start / Stop: bring an LLM Server's process up or down; Stop is unconditional"
aliases: [stop]
---

Bringing the LLM Server's process up or down. The mechanism is internal to each LLM Server type (fork-and-detach for `llamacpp` and `splash`; for `ollama`, spawning `ollama serve` to start and signalling the process listening at the instance's address to stop — its CLI has no server-stop command, `ollama stop MODEL` only unloads a model; `lms server start` / `lms server stop` for `lmstudio`). Stop is unconditional — see ADR-0001.
