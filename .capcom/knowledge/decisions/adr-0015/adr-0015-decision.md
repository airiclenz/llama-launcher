---
title: "ADR-0015 decision: a group-leader process with a Splash launch for the address counts as Starting while nothing listens there"
---

Splash binds its address only once its Model has loaded. While it loads, a connection to the address is refused, so no HTTP probe and no `lsof` lookup can see it. The launcher finds a loading Splash in the process table instead. This amends ADR-0010: for Splash, a Starting instance is a server *process* that is up but has not bound its address yet.

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
- **Unix only.** The process table is read with `ps -A -ww -o pid=,pgid=,command=`. ps joins each command line's arguments with spaces, so it supplies only the PID and PGID; a session leader's command line is its true argv, read from the kernel (`kern.procargs2` on darwin, `/proc/<pid>/cmdline` on linux). On Windows the read refuses with `ErrUnsupported`, so a loading Splash stays invisible there, where the launcher could not signal it anyway (ADR-0012).
