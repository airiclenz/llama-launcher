# llamacpp uses restart-per-Profile, not multi-Model hosting

When a user activates a Profile that targets `llamacpp`, llml stops the existing `llama-server` (if any) and starts a fresh one with that Profile's Model and hardware parameters baked into the start arguments (`-m`, `-c`, `-ngl`, ...). llml does *not* use `llama-server`'s `/models/load` HTTP API, even though that API exists and would allow a single `llama-server` to hold several Models simultaneously.

## Why

`llama-server`'s multi-Model mode has a known upstream bug: per-Model hardware overrides (`context_size`, `gpu_layers`) are silently ignored — every Model loaded after the first inherits the server's startup hardware settings. See [llama.cpp #20851](https://github.com/ggml-org/llama.cpp/issues/20851).

In practice this means a Profile that asks for a larger `context_size` or different `gpu_layers` would not get them, and the user would have no indication. Restarting per-Profile guarantees each Profile actually gets the hardware parameters it asked for, at the cost of (a) holding only one Model in memory on `llamacpp` at a time and (b) paying the server-startup cost when switching Profiles.

This is the right trade-off as long as the upstream bug exists: honest hardware settings matter more than keeping multiple Models warm, especially given the alternative is silently misleading users about what their Profile is doing.

## Consequences

- `Backend.LoadModel` and `Backend.UnloadModel` are no-ops on `llamacpp` (Model swap goes through stop-and-restart, not the API).
- `auto_unload` is meaningful only for LLM Servers that load Models via API (Ollama, LM Studio). For `llamacpp`, restart implies unload; the flag is silently ignored on `llamacpp` Profiles.
- The hardware-param-conflict warning that existed for the never-shipped multi-Model path is removed — under restart-per-Profile no conflict is possible.
- LM Studio and Ollama may still hold multiple Models at once (their multi-Model implementations work). This ADR is `llamacpp`-specific.

## Trigger to revisit

If llama.cpp #20851 is fixed upstream and per-Model hardware overrides start to apply correctly, multi-Model hosting on `llamacpp` becomes a real option to weigh against restart-per-Profile. The trade-off then is "switching cost" vs "memory pressure from N Models warm" — a different conversation than the one this ADR resolved.
