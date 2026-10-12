---
title: "Activate: resolve a Profile, then start its LLM Server and load its Model if needed"
---

The orchestration the launcher performs when a user selects a Profile: resolve it, start the LLM Server if needed, load the Model if needed. Nothing is persisted. The user-facing CLI verb is `load` (`llama-launcher load <profile>`), but inside the codebase this whole operation is *activation* to avoid colliding with the lower-level Model load. One Profile is activated at a time per LLM Server.
