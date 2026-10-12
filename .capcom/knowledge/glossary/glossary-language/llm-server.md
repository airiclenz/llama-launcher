---
title: "LLM Server: a piece of LLM-serving software the launcher drives, as a class or as a running instance at one host:port"
---

A piece of LLM-serving software the launcher knows how to drive (e.g. `llamacpp`, `ollama`, `lmstudio`, `splash`). The same term covers both the class ("llamacpp is an LLM Server") and a running instance ("the LLM Server on port 8080"); context disambiguates. A running LLM Server is identified by its `host:port` address — multiple LLM Servers of any type may run concurrently as long as each binds a distinct address (see ADR-0006). The launcher can start and stop any LLM Server it supports; how that happens is an implementation detail of each LLM Server type and not exposed to the user.
_Avoid_: Backend, server software, engine, runtime, "managed server", "external server".
