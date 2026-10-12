---
title: "ADR-0009 why: the orchestration had zero tests, and injecting LLMServer plus a process controller would not split along its effects"
---

The activation orchestration is where the launcher's core decisions live — "is this a no-op?", "what must be stopped or unloaded first?", "start or connect?" — and before this seam it had **zero** tests: every path ended in a real `exec.Command`, `lsof`, `syscall.Kill`, or live HTTP call, so only its pure leaf helpers were testable. The two Phase-1 correctness fixes that touched exactly this logic (cross-backend switching on a shared address; the spurious drift notice) shipped without orchestration-level regression tests because none could exist.

Two seam shapes were considered:

- **An operations interface (chosen).** One interface listing the effects the orchestration performs, with a real adapter and an in-memory fake. The orchestration's complete dependency surface is explicit in a single declaration, and a test states the world ("healthy at :8080, serving model X, these instances running") as plain data.
- **Injecting `LLMServer` plus a `processController` (rejected).** The existing `LLMServer` registry already abstracts the per-backend HTTP calls, so only the fork/kill parts would need a new interface. But the orchestration's effects do not split along that line: `stop` and `unloadInstance` span *multiple* backends (they re-identify whatever answers at an address), and `discover` belongs to neither. The split would leave the decision logic still reaching around the injected pieces for free functions, and a test would have to fake two seams plus the registry to control one scenario.
