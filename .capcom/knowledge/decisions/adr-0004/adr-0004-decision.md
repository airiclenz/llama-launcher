---
title: "ADR-0004 decision: one auto_unload flag unloads every Model the activated Profile does not load, on any running LLM Server"
---

llml exposes a single `auto_unload` flag (default `true`). It means: when activating a Profile, unload any Model that is no longer in active use, on any LLM Server that is still running. "In active use" means the Model the activated Profile loads. Everything else gets unloaded.
