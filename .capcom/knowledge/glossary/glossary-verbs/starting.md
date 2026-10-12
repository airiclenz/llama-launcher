---
title: "Starting: the state of an LLM Server whose process is up but not yet ready to serve"
---

The window after Start in which the LLM Server's process is up but not yet ready to serve. For `llamacpp` this lasts as long as the Model load takes, and the server already holds its address. `splash` binds its address only once the Model has loaded, so the launcher finds a loading Splash by its process instead (see ADR-0015). Ollama and LM Studio have no such window — their server is ready before any Model is loaded. A Starting LLM Server is a first-class instance: it is visible (displayed as "starting…") and stoppable, but Activation will not displace it except via `--restart` — see ADR-0010.
_Avoid_: "still-loading server", "booting", "warming up".
