---
title: "ADR-0003 why: llama-server's multi-Model mode silently ignores per-Model hardware overrides (llama.cpp #20851)"
---

`llama-server`'s multi-Model mode has a known upstream bug: per-Model hardware overrides (`context_size`, `gpu_layers`) are silently ignored — every Model loaded after the first inherits the server's startup hardware settings. See [llama.cpp #20851](https://github.com/ggml-org/llama.cpp/issues/20851).

In practice this means a Profile that asks for a larger `context_size` or different `gpu_layers` would not get them, and the user would have no indication. Restarting per-Profile guarantees each Profile actually gets the hardware parameters it asked for, at the cost of (a) holding only one Model in memory on `llamacpp` at a time and (b) paying the server-startup cost when switching Profiles.

This is the right trade-off as long as the upstream bug exists: honest hardware settings matter more than keeping multiple Models warm, especially given the alternative is silently misleading users about what their Profile is doing.
