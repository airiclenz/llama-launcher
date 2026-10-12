---
title: "ADR-0015 consequences: one ps per failing Splash address, hand-started leaders match too, and the match tracks Splash's launch forms"
---

- Discovery runs one `ps` per Splash address that fails its health check, plus one kernel argv read per session leader it lists. Only Splash addresses pay this cost.
- A Splash started by hand in a terminal is also matched when it is its group's leader. An interactive shell usually makes a foreground job the leader of its own group. This follows ADR-0001: stop is unconditional and does not ask who started the server.
- The match reads a session leader's true argv, so a `--binary` or `server.py` path, `--host` or `--port` containing spaces is recognised. When the kernel will not show that argv (another user's process), the leader falls back to ps's whitespace-split command line, where such a value is not recognised. *(Amended 2026-09-24: the match originally split ps's command line on whitespace for every process.)*
- The match follows Splash's command-line forms. If Splash changes how `splash serve` execs into its server, the match must change with it. The integration test `TestSplashStopWhileLoading` catches such a change.
- A Splash that is still downloading its Model before it binds is matched too. ADR-0014 still refuses to start an uninstalled Model, so this happens only for a server started outside the launcher.
