---
title: "Load / Unload: put a Model's weights into a running LLM Server's memory, or take them out"
aliases: [unload]
---

The API-level operation of putting Model weights into a running LLM Server's memory (or removing them). For LLM Servers with a load/unload HTTP API (Ollama, LM Studio), this is an HTTP call. For `llamacpp` and `splash`, there is no API load — the Model is baked into the server's start arguments, so "loading a Model" means starting (or restarting) the LLM Server with that Model in `--model`.
