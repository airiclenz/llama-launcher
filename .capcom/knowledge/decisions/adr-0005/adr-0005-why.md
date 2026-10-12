---
title: "ADR-0005 why: the LLM Server is part of a Profile's identity, so defaulting it invites silent misconfiguration"
---

A Profile's LLM Server is not a parameter — it is part of the Profile's identity. The Model identifier format, supported parameters, and lifecycle semantics all depend on it. `qwen2.5-32b-instruct-Q4_K_M.gguf` is a valid Model for `llamacpp` and a meaningless string for Ollama; `gpu_layers` is a `llamacpp` flag with no counterpart in LM Studio. There is no defensible default for "which kind of LLM Server does this Profile run on" once multiple LLM Servers are enabled — the right answer is always "the one the Profile author intended," which must be written down.

Treating `server` as just another defaultable parameter invites silent misconfiguration: a user adds a new Profile, forgets `server:`, and gets it implicitly routed to whatever `defaults.server` says — possibly an LLM Server that cannot run the Profile's Model. The launcher would only surface the problem at activation, not at config load.
