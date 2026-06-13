---
name: manage-llm-server
description: Inspect and control the local LLM server(s) via `llama-launcher` — list profiles, see what's running, switch models, tail logs. Use when the user wants to know what model is loaded, change models, start/stop or restart the server, or troubleshoot a stuck load.
---

# manage-llm-server

`llama-launcher` is a CLI for running and switching local LLM servers (llama.cpp, Ollama, LM Studio). Models are defined as **profiles**; loading a profile starts the right server and loads its model. Use this skill to inspect state, switch profiles, and tail logs.

## Safety rules (do these first)

1. **Always start with a passive inspection** — `llama-launcher status --json` and `llama-launcher list --json`. Do not call `load`, `unload`, `start`, or `stop` until you've shown the user what's currently running and have explicit confirmation to change it.
2. **Check for in-flight work before mutating.** Switching, unloading, or stopping a model interrupts any request or job currently using it. If a high `uptime_seconds` coincides with the user actively working, assume the model may be in use and confirm before changing it.
3. **`load <profile>` is idempotent** — it's a no-op if the requested profile is already active. Only reach for `--restart` if the user explicitly asks to force a restart, or if the server is in a bad state confirmed via logs.
4. **`unload` and `stop` end the session** — any in-flight requests will fail. Confirm before calling.

## Command reference

Passive (safe to run any time):

| Command | Purpose |
|---|---|
| `llama-launcher list` / `--json` | Enumerate available profiles (name, title, description, backend, model file, gpu_layers, context_size). `title` is the human-readable label; `description` is omitted when unset. |
| `llama-launcher status` / `--json` | Per-backend: running, address, active_profile, active_model, pid, uptime_seconds. |
| `llama-launcher logs <backend>` | Tail the last chunk of an instance's log. Add `-f` to follow. |
| `llama-launcher logs clean --days N` / `--all` | Prune old logs. |
| `llama-launcher config validate` | Check the config file for errors. |
| `llama-launcher version` | Print version. |

Mutating (confirm with user first):

| Command | Purpose |
|---|---|
| `llama-launcher load <profile>` | Activate a profile. No-op if already active. |
| `llama-launcher load <profile> --restart` | Force a restart even if active. |
| `llama-launcher unload [profile]` | Stop the server / unload the model. |
| `llama-launcher start [--profile p]` | Start a server, optionally with a profile. |
| `llama-launcher stop [target]` | Stop a server by `host:port` or backend name. |

## Using llama-launcher remotely over MCP

When you can't run the `llama-launcher` CLI directly — e.g. you're an agent in a container/VM and `llama-launcher` lives on a different host — the same operations may be available as **MCP tools** served by the optional `llama-launcher-mcp` adapter running on that host.

**Finding the MCP server:**

1. First check whether an MCP server (typically named `llama-launcher`) is already configured in your client. If it is, just use its tools — no address needed.
2. If it isn't configured, the adapter listens on the host at **default port `7331`**, endpoint `http://<host-ip>:7331/mcp`.
   - `<host-ip>` is the host's address as seen from where you are. From inside a VM/container that is the host/bridge gateway IP — the same address you use to reach the LLM for inference (on macOS this is commonly `192.168.64.1`).
   - The adapter only listens if the user has started it; if `http://<host-ip>:7331/mcp` doesn't respond, it isn't running and you should fall back to asking the user or using the CLI.

**Tool ↔ CLI mapping:**

| MCP tool | Equivalent CLI | Kind |
|---|---|---|
| `list_profiles` | `list --json` | passive |
| `server_status` | `status --json` | passive |
| `tail_log {target?}` | `logs [target]` | passive |
| `load_profile {name, restart?}` | `load <name> [--restart]` | mutating |
| `unload_model {profile?}` | `unload [profile]` | mutating |
| `start_server {profile?}` | `start [--profile p]` | mutating |
| `stop_server {target?}` | `stop [target]` | mutating |

- **All the safety rules above apply unchanged** — inspect with `server_status` / `list_profiles` first, confirm before any mutating tool.
- `target` / `profile` are optional and disambiguate when more than one server is running or more than one model is loaded — same semantics as the CLI's optional positional args.
- The adapter may run **read-only**, in which case only `list_profiles`, `server_status`, and `tail_log` exist. If a mutating tool is absent, the user has intentionally restricted you to observation — report state and ask them to act; don't look for a workaround.
- Tool results are the CLI's stdout (the same JSON you'd parse from `--json`); a failed command surfaces its error output.

## Identifying the active profile

`status --json` returns `active_model` (the model filename) but `active_profile` may be empty when a server was started outside a profile boundary. To map the running filename back to a profile name, intersect with `list --json` on the `model` field. Use the profile name in any subsequent `load`, since profile names are stable while filenames may not be.

Example mapping logic (conceptual):

```sh
active=$(llama-launcher status --json | jq -r '.[] | select(.running) | .active_model')
llama-launcher list --json | jq -r --arg m "$active" '.[] | select(.model == $m) | .name'
```

## Typical flows

**"What's loaded right now?"** — run `status --json` and `list --json`, map filename → profile, report `<backend> running <profile> (<model>) on <address>, up <uptime>`.

**"Switch to <model>"** —
1. Run `status --json`; if the current model may be in use, confirm with the user before switching.
2. Show what's currently active and what you're about to load.
3. Run `llama-launcher load <profile>` (no `--restart` unless asked).
4. Re-check `status --json` and confirm `active_model` matches the new profile's `model`. `uptime_seconds` resets on a real switch — if it didn't reset and the model is unchanged, the load was a no-op.

**"The server seems stuck"** — `logs <backend>` (no `-f` first; just sample the tail) and report what you see before suggesting `--restart` or `stop` + `start`.

## Notes on output

- All JSON commands print well-formed arrays/objects — parse with `jq` or read directly. Don't grep for filenames; use the structured fields.
- `uptime_seconds` is the server's uptime, not the model's load time. A long uptime + matching `active_model` means the model has been resident for that whole period.
- A backend can appear in `status --json` with `running: false` while another is active. That's normal — only act on backends with `running: true`.
