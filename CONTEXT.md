# llama-launcher

A profile-driven launcher for local LLM serving software. Users name **Profiles**; the launcher resolves them into a running **LLM Server** with a **Model** loaded.

## Language

**LLM Server**:
A piece of LLM-serving software the launcher knows how to drive (e.g. `llamacpp`, `ollama`, `lmstudio`). The same term covers both the class ("llamacpp is an LLM Server") and a running instance ("the LLM Server on port 8080"); context disambiguates. A running LLM Server is identified by its `host:port` address — multiple LLM Servers of any type may run concurrently as long as each binds a distinct address (see [ADR-0006](docs/adr/0006-instances-are-keyed-by-address.md)). The launcher can start and stop any LLM Server it supports; how that happens is an implementation detail of each LLM Server type and not exposed to the user.
_Avoid_: Backend, server software, engine, runtime, "managed server", "external server".

**Model**:
The weights an LLM Server loads to serve requests. A file path on disk for llamacpp; an opaque identifier (`llama3.1:8b`, `lmstudio-community/...`) for Ollama and LM Studio. Models are *loaded into* an LLM Server, never run on their own.
_Avoid_: Weights file, GGUF (too narrow), checkpoint.

**Profile**:
A named, user-defined launch recipe = LLM Server + Model + parameter overrides + description. The only artefact the user names directly; everything else (which server is running, which model is loaded) is derived from the selected Profile at runtime.
_Avoid_: Preset, config entry, model config (too narrow — a Profile is more than its Model).

## Verbs

**Activate** (a Profile):
The orchestration the launcher performs when a user selects a Profile: resolve it, start the LLM Server if needed, load the Model if needed, update state. The user-facing CLI verb is `load` (`llml load <profile>`), but inside the codebase this whole operation is *activation* to avoid colliding with the lower-level Model load. One Profile is activated at a time per LLM Server.

**Load** / **Unload** (a Model into an LLM Server):
The API-level operation of putting Model weights into a running LLM Server's memory (or removing them). For LLM Servers with a load/unload HTTP API (Ollama, LM Studio), this is an HTTP call. For `llamacpp`, there is no API load — the Model is baked into the server's start arguments, so "loading a Model" means starting (or restarting) the LLM Server with that Model in `-m`.

**Start** / **Stop** (an LLM Server):
Bringing the LLM Server's process up or down. The mechanism is internal to each LLM Server type (fork-and-detach for `llamacpp`, `ollama serve` / `ollama stop`, `lms server start` / `lms server stop`). Stop is unconditional — see [ADR-0001](docs/adr/0001-stop-is-unconditional.md).

## Flagged ambiguities

- The Go interface is currently named `Backend`; the config key is `servers:`. The domain language above resolves to **LLM Server**, so `Backend` is a code-level alias awaiting rename.
- There is no global "active" or "current" Profile or LLM Server. Each running LLM Server has its own active Profile/Model (recorded in its per-server state file), but the system never picks one as canonical. When an action could apply to several running LLM Servers, the user selects which one — in the CLI via an optional argument (`llml stop [server]`, `llml unload [profile]`), in the TUI via a sub-list. When only one is a valid target, selection collapses to a single auto-resolved option. Avoid "active server," "primary server," "current server" as cross-cutting terms.
