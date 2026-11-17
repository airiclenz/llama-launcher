---
title: "ADR-0015 consequences: one ps per failing Splash or vLLM address, hand-started leaders match too, and the match tracks each server's launch forms"
---

- Discovery runs one `ps` per Splash address that fails its health check, plus one kernel argv read per session leader it lists. Only Splash and vLLM addresses pay this cost. *(Amended 2026-11-17: originally Splash only.)*
- A Splash started by hand in a terminal is also matched when it is its group's leader. An interactive shell usually makes a foreground job the leader of its own group. This follows ADR-0001: stop is unconditional and does not ask who started the server.
- The match reads a session leader's true argv, so a `--binary` or `server.py` path, `--host` or `--port` containing spaces is recognised. When the kernel will not show that argv (another user's process), the leader falls back to ps's whitespace-split command line, where such a value is not recognised. *(Amended 2026-09-24: the match originally split ps's command line on whitespace for every process.)*
- The match follows Splash's command-line forms. If Splash changes how `splash serve` execs into its server, the match must change with it. The integration test `TestSplashStopWhileLoading` catches such a change.
- A Splash that is still downloading its Model before it binds is matched too. ADR-0014 still refuses to start an uninstalled Model, so this happens only for a server started outside the launcher.
- *(Amended 2026-11-17.)* The match now also covers vLLM's launch forms: `vllm serve …`, seen by the kernel as `…/bin/python3 …/bin/vllm serve …` for a pip or uv install, and `python -m vllm.entrypoints.openai.api_server …`. A vLLM started by hand is matched when it leads its group, as for Splash. vLLM has no install check, so a vLLM downloading its Model on first start is matched as loading too. If vLLM changes its entry point, the match must change with it.
