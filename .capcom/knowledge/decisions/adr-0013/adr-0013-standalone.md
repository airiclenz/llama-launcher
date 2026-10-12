---
title: "ADR-0013 why worth having standalone: on-demand switching behind one stable endpoint serves every OpenAI client, most of all on llamacpp"
---

On-demand switching behind one stable endpoint is llama-swap's whole, proven value proposition — independent of any agent: every OpenAI-compatible client (chat UIs, editor plugins, scripts) gets "request any Profile by name and the host makes it so", plus queue-during-swap instead of connection errors. Today a request for an unloaded Model simply fails until a human runs `llml load`. apogee is one client among many; its TUI-vs-daemon contention resolving in the Gateway's queue is just the priority feature below.

The value concentrates on `llamacpp`: Ollama and LM Studio already dispatch multiple Models server-internally (ADR-0002, meaning 1), so for those backends the Gateway adds mainly the priority queue and the single stable port.
