---
title: "Model: the weights an LLM Server loads to serve requests, never run on their own"
---

The weights an LLM Server loads to serve requests. A file path on disk for llamacpp; an opaque identifier (`llama3.1:8b`, `lmstudio-community/...`) for Ollama and LM Studio; a Hugging Face `owner/repo` id that Splash has installed into the Hugging Face cache, for Splash (the launcher refuses one that is not installed — see ADR-0014). Models are *loaded into* an LLM Server, never run on their own.
_Avoid_: Weights file, GGUF (too narrow), checkpoint.
