---
name: manage-llm-server
description: Inspect and switch the local LLM via `llama-launcher` — list profiles, check what's running, swap models between sim sessions, tail logs. Use when the user wants to know what model is loaded, change models, restart the upstream LLM server, or troubleshoot a stuck load. Especially useful during long sim sessions where different models need to be tried sequentially.
---

# manage-llm-server

`llama-launcher` is the user's CLI for the local LLM server that Apogee proxies to (default upstream `:1111` for llamacpp, plus an optional lmstudio backend). Use this skill to inspect state, switch profiles, and tail logs. **Never** swap models while a simulation or benchmark is in flight — the user runs sims sequentially and a mid-run swap will corrupt the run.

## Safety rules (do these first)

1. **Always start with a passive inspection** — `llama-launcher status --json` and `llama-launcher list --json`. Do not call `load`, `unload`, `start`, or `stop` until you've shown the user what's currently running and have explicit confirmation to change it.
2. **Check for active sims/benches before mutating.** If unsure, ask the user. Signals that something is running: recent activity in `~/.apogee/logs/`, `apogee sim` / `apogee sim sweep` / `apogee bench` in `ps`, or a high `uptime_seconds` paired with the user actively working in the terminal.
3. **`load <profile>` is idempotent** — it's a no-op if the requested profile is already active. Only reach for `--restart` if the user explicitly asks to force-restart, or if the server is in a bad state confirmed via logs.
4. **`unload` and `stop` are destructive in context** — they end the LLM session and any in-flight requests will fail. Confirm before calling.

## Command reference

Passive (safe to run any time):

| Command | Purpose |
|---|---|
| `llama-launcher list` / `--json` | Enumerate available profiles (name, title, description, backend, model file, gpu_layers, context_size). `title` is the human-readable label; `description` is omitted when unset. |
| `llama-launcher status` / `--json` | Per-backend: running, address, active_profile, active_model, pid, uptime_seconds. |
| `llama-launcher logs <backend>` | Tail the last chunk of an instance's log. Add `-f` to follow. |
| `llama-launcher logs clean --days N` / `--all` | Prune old logs. |
| `llama-launcher config validate` | Check config file for errors. |
| `llama-launcher version` | Print version. |

Mutating (confirm with user first):

| Command | Purpose |
|---|---|
| `llama-launcher load <profile>` | Activate a profile. No-op if already active. |
| `llama-launcher load <profile> --restart` | Force a restart even if active. |
| `llama-launcher unload [profile]` | Stop the server / unload the model. |
| `llama-launcher start [--profile p]` | Start a server, optionally with a profile. |
| `llama-launcher stop [target]` | Stop a server by `host:port` or backend name. |

## Same operations over MCP (when running in a container)

When you don't have direct CLI access to the host — e.g. you're a coding agent inside a container and `llama-launcher` lives on the host — the same lifecycle operations are exposed as **MCP tools** by the optional host-side adapter `llama-launcher-mcp` (see the project's ADR-0008 / README "Remote control from a container"). The adapter shells out to the CLI; it dispatches control commands only and never proxies inference. If the `llama-launcher` MCP server is configured, prefer these tools over trying to reach the host CLI directly.

| MCP tool | Equivalent CLI | Kind |
|---|---|---|
| `list_profiles` | `list --json` | passive |
| `server_status` | `status --json` | passive |
| `tail_log {target?}` | `logs [target]` | passive |
| `load_profile {name, restart?}` | `load <name> [--restart]` | mutating |
| `unload_model {profile?}` | `unload [profile]` | mutating |
| `start_server {profile?}` | `start [--profile p]` | mutating |
| `stop_server {target?}` | `stop [target]` | mutating |

- **All the safety rules above apply unchanged** — inspect with `server_status` / `list_profiles` first, check for active sims/benches, never swap mid-run, and confirm before any mutating tool. The adapter exposes the mutating tools plainly; the judgment stays with you.
- `target` / `profile` are optional and disambiguate when more than one server is running or more than one model is loaded — same semantics as the CLI's optional positional args.
- The adapter may run **`--read-only`**, in which case only `list_profiles`, `server_status`, and `tail_log` are registered and the mutating tools simply won't be present. If a mutating tool is missing, the operator has intentionally restricted you to observation — report state and ask the user to act, don't look for a workaround.
- Tool results are the CLI's stdout (the same JSON you'd parse from `--json`). A failed command surfaces stderr as an error result.

## Identifying the active profile

`status --json` returns `active_model` (the GGUF filename) but `active_profile` may be empty when a server was started outside a profile boundary. To map the running filename back to a profile name, intersect with `list --json` on the `model` field. Use the profile name in any subsequent `load`, since profile names are stable while filenames may not be.

Example mapping logic (conceptual):

```sh
active=$(llama-launcher status --json | jq -r '.[] | select(.running) | .active_model')
llama-launcher list --json | jq -r --arg m "$active" '.[] | select(.model == $m) | .name'
```

## Typical flows

**"What's loaded right now?"** — run `status --json` and `list --json` in parallel, map filename → profile, report `<backend> running <profile> (<model>) on <address>, up <uptime>`.

**"Switch to gpt-oss-20b for the next sim"** —
1. Run `status --json`; if a sim is running, refuse and tell the user. If unsure, ask.
2. Show the user what's currently active and what you're about to load.
3. Run `llama-launcher load <profile>` (no `--restart` unless asked).
4. Re-check `status --json` and confirm `active_model` matches the new profile's `model`. Note that `uptime_seconds` resets on a real switch — if it didn't reset and the model name is unchanged, the load was a no-op.

**"The server seems stuck"** — `logs <backend>` (no `-f` first; just sample the tail) and report what you see before suggesting `--restart` or `stop` + `start`.

**Between sim batches in a long session** — passive `status --json` to confirm the expected model is still active, then proceed. Sims should always be run sequentially against the local server.

## Notes on output

- All JSON commands print well-formed arrays/objects — parse with `jq` or read directly. Don't grep for filenames; use the structured fields.
- `uptime_seconds` is the server's uptime, not the model's load time. A long uptime + matching `active_model` means the model has been resident for that whole period.
- `lmstudio` backend may appear in `status --json` with `running: false` even when llamacpp is active. That's normal — only act on backends with `running: true`.
