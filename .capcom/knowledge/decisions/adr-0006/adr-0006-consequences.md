---
title: "ADR-0006 consequences: auto_stop_server and every command work per instance, and legacy state files are removed"
---

- `auto_stop_server` retains its meaning ("stop other instances when activating") but now applies per-instance rather than per-type. With `auto_stop_server: false`, any number of instances of any type can run simultaneously.
- `status`, `stop`, `unload`, and the TUI menu all need to enumerate instances rather than lookup-by-type. The CLI's optional positional argument (today: `[backend]`) becomes `[address-or-backend]` and disambiguates against a possibly-non-unique backend name.
- A migration step removes any legacy `state.json` or `state-{backend}.json` files on first run (treating them as stale; if their process is alive, the user can re-activate the relevant Profile).
- TDD §7 (State Files) needs a full rewrite reflecting the per-instance model.
