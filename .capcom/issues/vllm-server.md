---
title: vLLM is not a supported LLM Server
status: done
type: feature
priority: 3
plan: 2026-11-17 - 01 - vllm-mouse-autostart-plan
created: 2026-10-13T11:47:04Z
closed: 2026-11-17T09:22:50Z
reason: Resolved by plan "2026-11-17 - 01 - vllm-mouse-autostart-plan" (archived at cbeed77), items 1-5
---

vLLM (and other servers) are not supported LLM Servers. Each would be a new `backend_<name>.go` implementing the `LLMServer` interface (TDD §5.3).

## Acceptance

- vLLM is a supported LLM Server: start, stop, health check, model load/unload and runtime discovery work as for the existing servers.
- The glossary, TDD and README list it alongside `llamacpp`, `ollama`, `lmstudio` and `splash`.
- Unit tests in the style of TDD §12.1 cover its health check and detection, including not mistaking another server for it.
