# A loading Splash is found by its process, not by its address

Splash binds its address only once its Model has loaded. While it loads, a connection to the address is refused, so no HTTP probe and no `lsof` lookup can see it. The launcher finds a loading Splash in the process table instead. This amends [ADR-0010](0010-starting-instances-are-visible-and-stoppable.md): for Splash, a Starting instance is a server *process* that is up but has not bound its address yet.

- **The match.** A process is a loading Splash for `host:port` when all of these hold:
  - it leads its own process group (PGID == PID). The launcher forks every managed server with `Setsid`, so a launcher-started server is always a group leader.
  - its command line is a Splash server launch. Every form carries `--model`, and either the word before `serve` is `splash` or `launcher.py`, or the value of `--binary` is `…/splash`. One launch `exec`s through `/bin/sh …/splash serve`, the checkout's `splash` script, `python …/install/launcher.py serve`, and finally `python …/server/server.py … --binary …/build/splash`, all in the same process. These forms were observed on a real Splash.
  - the last `--host` and the last `--port` on the command line equal the address. The last occurrence wins because Splash's argument parser keeps it, so an `extra_args` override is honoured. When a flag is absent, Splash's default address (`127.0.0.1:8000`) supplies it.
- **Only while nothing listens.** The match counts only while no process listens on the address's port (`lsof`). Once anything holds the address, the HTTP probes and `lsof` identify it as before. A listener that fails those probes is never attributed to Splash by its command line.
- **Where it is used.** The match is used wherever ADR-0010 already asks "is this Starting?", and nowhere else:
  - discovery's Starting fallback, which also supplies the PID shown for the instance;
  - the start path's still-starting refusal, so no duplicate server is forked;
  - the stop path's second identification pass;
  - the stop path's "stopped" check.

  The stop signals the matched PID and its process group through the same `terminatePID` escalation as every other stop.
- **Splash only.** The lookup sits behind a `LoadingProcessFinder` interface that only Splash implements. llamacpp, Ollama and LM Studio never read the process table.
- **Unix only.** The process table is read with `ps -A -ww -o pid=,pgid=,command=`. ps joins each command line's arguments with spaces, so it supplies only the PID and PGID; a session leader's command line is its true argv, read from the kernel (`kern.procargs2` on darwin, `/proc/<pid>/cmdline` on linux). On Windows the read refuses with `ErrUnsupported`, so a loading Splash stays invisible there, where the launcher could not signal it anyway ([ADR-0012](0012-the-library-compiles-everywhere-and-actuates-where-it-can.md)).

## Why

A loading Splash was invisible to everything ADR-0010 built. `status` did not show it, `stop` refused it with "no server running", and a second `load` could fork a duplicate onto the same address. The loading takes about a minute for a large Model. ADR-0010's reasoning applies unchanged: the user who says "stop" means it, and a duplicate start must be refused.

Two other ways were considered:

- **A persisted launch record** (PID, address and start time written at fork, and removed on stop or when found stale). This would cover exactly the servers the launcher started and needs no command-line parsing. It was rejected because the launcher persists nothing between invocations (TDD §7). A record file would reverse that design, bring back the stale-state drift that live derivation removed, and conflict with the legacy `state-*.json` cleanup.
- **Leaving it documented as a limitation.** This was rejected because the duplicate-fork and unstoppable-load gaps are real bugs, not edge cases.

The attribution stays at least as strong as the one ADR-0010 already accepts: a bare 503 answer for llamacpp. Several things must line up at once: a group-leader process whose command line is a Splash launch with `--model`, the exact host and port configured for Splash, and nothing listening there. The foreign-occupant protection of ADR-0006 and ADR-0010 is not weakened. An address where nothing matches still refuses with "no server running", and a process that listens is never judged by its command line.

## Consequences

- Discovery runs one `ps` per Splash address that fails its health check, plus one kernel argv read per session leader it lists. Only Splash addresses pay this cost.
- A Splash started by hand in a terminal is also matched when it is its group's leader. An interactive shell usually makes a foreground job the leader of its own group. This follows ADR-0001: stop is unconditional and does not ask who started the server.
- The match reads a session leader's true argv, so a `--binary` or `server.py` path, `--host` or `--port` containing spaces is recognised. When the kernel will not show that argv (another user's process), the leader falls back to ps's whitespace-split command line, where such a value is not recognised. *(Amended 2026-09-24: the match originally split ps's command line on whitespace for every process.)*
- The match follows Splash's command-line forms. If Splash changes how `splash serve` execs into its server, the match must change with it. The integration test `TestSplashStopWhileLoading` catches such a change.
- A Splash that is still downloading its Model before it binds is matched too. ADR-0014 still refuses to start an uninstalled Model, so this happens only for a server started outside the launcher.
