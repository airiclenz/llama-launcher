# Activation orchestration drives an operations seam

`LoadProfile`'s orchestration — the ADR-0007 idempotency/drift check, the ADR-0004 `auto_stop_server`/`auto_unload` pass, and the managed/external activation fork — does not call processes or servers directly. It drives a package-private operations interface (`activationOps` in `server.go`) that captures every process, health, and probe effect the activation sequence needs: `healthy`, `loadedModel`, `liveDrift`, `discover`, `start`, `waitHealthy`, `stop`, `unloadInstance`, `loadModel`, `unloadModel`. The production adapter (`realOps`) delegates each operation to the live implementation — `exec`/`lsof`/signals for processes, each backend's HTTP API for probes — and the exported `LoadProfile` binds it. Tests substitute a fake. The orchestration also derives the target address (`host:port`) from the resolved profile exactly once and carries it, instead of re-deriving it per call site.

## Why

The activation orchestration is where the launcher's core decisions live — "is this a no-op?", "what must be stopped or unloaded first?", "start or connect?" — and before this seam it had **zero** tests: every path ended in a real `exec.Command`, `lsof`, `syscall.Kill`, or live HTTP call, so only its pure leaf helpers were testable. The two Phase-1 correctness fixes that touched exactly this logic (cross-backend switching on a shared address; the spurious drift notice) shipped without orchestration-level regression tests because none could exist.

Two seam shapes were considered:

- **An operations interface (chosen).** One interface listing the effects the orchestration performs, with a real adapter and an in-memory fake. The orchestration's complete dependency surface is explicit in a single declaration, and a test states the world ("healthy at :8080, serving model X, these instances running") as plain data.
- **Injecting `LLMServer` plus a `processController` (rejected).** The existing `LLMServer` registry already abstracts the per-backend HTTP calls, so only the fork/kill parts would need a new interface. But the orchestration's effects do not split along that line: `stop` and `unloadInstance` span *multiple* backends (they re-identify whatever answers at an address), and `discover` belongs to neither. The split would leave the decision logic still reaching around the injected pieces for free functions, and a test would have to fake two seams plus the registry to control one scenario.

## Consequences

- The first orchestration tests exist (`server_test.go`): the idempotent no-op (with and without a drift notice), `--restart`, the `auto_stop_server`/`auto_unload` matrix including the foreign-occupant-at-the-shared-address case, and the external swap/connect fork — all against the fake, with no process forked and no socket opened.
- Integration-style tests that want the real probes but not real signals embed `realOps` and override only `stop` (`stopRecordingOps`), replacing the earlier ad-hoc `stopInstanceFn` package variable — the seam is now the interface, not a mutable global, so fake-driven tests can run in parallel.
- `realOps` must stay logic-free: every method is a one-line delegation, so the real/fake split cannot drift. Orchestration decisions belong in `loadProfile`; effect implementations belong in the functions `realOps` delegates to.
- The unified `Unload`/`Stop` entry points planned next build on the same seam rather than growing their own.
- The seam is package-private. It is a testing/structure decision, not API: callers keep using `LoadProfile`, and nothing outside `internal/launcher` can see the interface.
