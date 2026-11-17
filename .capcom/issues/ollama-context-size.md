---
title: Ollama Profiles cannot set a context size
status: done
type: feature
priority: 3
labels: [config]
plan: 2026-11-17 - 00 - p3-features-sweep-plan
created: 2026-10-13T11:47:04Z
closed: 2026-11-17T07:43:03Z
reason: resolved by plan "2026-11-17 - 00 - p3-features-sweep-plan" (archived in 2eb4448), item 1
---

Ollama Profiles cannot set a context size: Ollama's load request carries only the model name and a keep-alive, so its `ParamSpecs` is empty and the context column renders blank for Ollama rows (TDD §3).

## Acceptance

- An Ollama Profile's `context_size` is sent to Ollama on load (e.g. as `options.num_ctx`).
- Ollama's `ParamSpecs` carries the context-size spec, so the context column and "Show model config" show it with no change to the list renderers.
- Unit tests cover the load payload with and without `context_size`.
