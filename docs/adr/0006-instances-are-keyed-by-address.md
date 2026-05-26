# LLM Server instances are keyed by `host:port`, not by type

A running LLM Server is identified by the `host:port` it binds to, not by its type. llml supports an arbitrary number of instances of any LLM Server type running concurrently, as long as each binds a distinct address. Two Profiles that resolve to the same `host:port` share an instance slot — activating one replaces the other on that address.

## Why

After ADR-0003 (`llamacpp` is restart-per-Profile), the only way to have two Models loaded on `llamacpp` at the same time is to run two `llama-server` processes on different ports. The earlier state-file model — one file per LLM Server *type* (`state-llamacpp.json`) — made this impossible: the second instance would overwrite the first's state. Keying state by address instead of by type unblocks the use case and generalises cleanly: Ollama and LM Studio, which normally run a single instance per machine, behave exactly as before (one address, one state record).

The address is the only natural identity for an instance. It is what the user puts in the Profile, what the OS uses to multiplex (port binding), and what a client uses to reach the server. Profile name is *not* the right key: a Profile is a recipe, and the same recipe could be re-activated against the same address repeatedly without creating a new instance.

## How state is stored

One state record per running instance. The file naming convention is `state-{backend}-{port}.json` (host is omitted when `127.0.0.1`; included as `state-{backend}-{host}-{port}.json` otherwise). Examples:

- `state-llamacpp-8080.json`
- `state-llamacpp-8081.json`
- `state-ollama-11434.json`
- `state-lmstudio-1234.json`

`ReadAllStates()` becomes a glob over `state-*.json`. `ReadBackendState(backend)` is replaced by `ReadInstanceState(addr)` or `ReadInstancesForBackend(backend)` (returns a slice).

The `managed` field is removed in the same pass (per [ADR-0001](0001-stop-is-unconditional.md)) — both this ADR and ADR-0001 touch state file schema, and combining the rewrite is cheaper than two passes. Per-instance PID tracking continues for any LLM Server type that supports it (used for log file association and PID display in `status`), but is not used to decide whether `stop` should kill the process — stop is unconditional per ADR-0001.

## Conflict resolution

- **Different `host:port`s** → two distinct instances, both run.
- **Same `host:port`, same Profile** → no-op (already running).
- **Same `host:port`, different Profile** → the new Profile takes the slot. For `llamacpp` this means stop-and-restart with new args; for Ollama / LM Studio it depends on `auto_unload`: `true` unloads the previous Model and loads the new one, `false` keeps both loaded on the same instance.

## Consequences

- `auto_stop_server` retains its meaning ("stop other instances when activating") but now applies per-instance rather than per-type. With `auto_stop_server: false`, any number of instances of any type can run simultaneously.
- `status`, `stop`, `unload`, and the TUI menu all need to enumerate instances rather than lookup-by-type. The CLI's optional positional argument (today: `[backend]`) becomes `[address-or-backend]` and disambiguates against a possibly-non-unique backend name.
- A migration step removes any legacy `state.json` or `state-{backend}.json` files on first run (treating them as stale; if their process is alive, the user can re-activate the relevant Profile).
- TDD §7 (State Files) needs a full rewrite reflecting the per-instance model.
