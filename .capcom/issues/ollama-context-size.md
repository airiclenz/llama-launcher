---
title: Ollama Profiles cannot set a context size
status: open
type: feature
priority: 3
labels: [config]
created: 2026-10-13T11:47:04Z
---

Ollama Profiles cannot set a context size: Ollama's load request carries only the model name and a keep-alive, so its `ParamSpecs` is empty and the context column renders blank for Ollama rows (TDD §3).

## Acceptance

- An Ollama Profile's `context_size` is sent to Ollama on load (e.g. as `options.num_ctx`).
- Ollama's `ParamSpecs` carries the context-size spec, so the context column and "Show model config" show it with no change to the list renderers.
- Unit tests cover the load payload with and without `context_size`.
