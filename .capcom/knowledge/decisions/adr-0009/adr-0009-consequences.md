---
title: "ADR-0009 consequences: the first orchestration tests run against a fake, realOps stays logic-free, and the seam is not API"
---

- The first orchestration tests exist (`server_test.go`): the idempotent no-op (with and without a drift notice), `--restart`, the `auto_stop_server`/`auto_unload` matrix including the foreign-occupant-at-the-shared-address case, and the external swap/connect fork — all against the fake, with no process forked and no socket opened.
- Integration-style tests that want the real probes but not real signals embed `realOps` and override only `stop` (`stopRecordingOps`), replacing the earlier ad-hoc `stopInstanceFn` package variable — the seam is now the interface, not a mutable global, so fake-driven tests can run in parallel.
- `realOps` must stay logic-free: every method is a one-line delegation, so the real/fake split cannot drift. Orchestration decisions belong in `loadProfile`; effect implementations belong in the functions `realOps` delegates to.
- The unified `Unload`/`Stop` entry points (planned next when this was decided, since shipped in `server.go`) were built on the same seam rather than growing their own.
- The seam is package-private. It is a testing/structure decision, not API: callers keep using `LoadProfile`, and nothing outside `internal/launcher` can see the interface.
