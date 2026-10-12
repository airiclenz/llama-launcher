---
title: "ADR-0002 decision: the launcher starts, stops, loads and unloads, and never exposes an endpoint or routes client requests"
---

`llama-launcher` starts and stops LLM Servers and asks them to load and unload Models. It does not expose its own HTTP endpoint, does not proxy or route client requests, and does not select a Model on a client's behalf. Clients always talk to an LLM Server directly via its native address and pick the Model in the request body (as they would when using LM Studio or Ollama without llml).
