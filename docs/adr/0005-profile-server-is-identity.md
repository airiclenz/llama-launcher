# A Profile must name its LLM Server explicitly

Every Profile in the config declares which LLM Server it targets via `server:`. The current `defaults.server` field is soft-deprecated: if a Profile omits `server:` and only one LLM Server is enabled, the launcher auto-detects (existing behaviour). If a Profile omits `server:` and more than one LLM Server is enabled, validation emits a warning naming the Profile and the `defaults.server` fallback used. The fallback is removed entirely in a later release.

## Why

A Profile's LLM Server is not a parameter — it is part of the Profile's identity. The Model identifier format, supported parameters, and lifecycle semantics all depend on it. `qwen2.5-32b-instruct-Q4_K_M.gguf` is a valid Model for `llamacpp` and a meaningless string for Ollama; `gpu_layers` is a `llamacpp` flag with no counterpart in LM Studio. There is no defensible default for "which kind of LLM Server does this Profile run on" once multiple LLM Servers are enabled — the right answer is always "the one the Profile author intended," which must be written down.

Treating `server` as just another defaultable parameter invites silent misconfiguration: a user adds a new Profile, forgets `server:`, and gets it implicitly routed to whatever `defaults.server` says — possibly an LLM Server that cannot run the Profile's Model. The launcher would only surface the problem at activation, not at config load.

## Consequences

- `Config.Validate` emits a warning when a Profile lacks `server:` and `defaults.server` is being used as a fallback, naming the Profile.
- Single-LLM-Server users are unaffected: auto-detection from §4.6 covers them.
- `defaults.server` is removed in a future release; the warning is the deprecation notice.
- Operational parameters (`gpu_layers`, `threads`, `context_size`, etc.) remain in `defaults` — those *are* parameters with sensible defaults. Only `server` is removed from `defaults`.
