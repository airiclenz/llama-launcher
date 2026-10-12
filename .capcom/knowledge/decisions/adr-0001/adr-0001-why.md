---
title: "ADR-0001 why: the managed flag forced a two-axis model, and only a real stop frees the port auto_stop_server needs"
---

The earlier design carried a `managed` boolean on each state file: `true` if llml had started the process, `false` if llml had merely connected to an already-running server. `managed: false` made `stop` a soft disconnect — the state file was removed but the server was left alive. This forced a two-axis mental model on every operation ("did we start it?" × "what does stop mean here?") and leaked into the user-facing language as "managed vs external" backends.

Removing the distinction collapses the model to one rule: **stop means stop**. The user does not need to remember whether they launched LM Studio via the GUI or via llml — the result of `llml stop` is the same. This is also the only behaviour that makes `auto_stop_server` actually do its job (prevent two LLM Servers from fighting over the same port); a soft disconnect would leave the port held.
