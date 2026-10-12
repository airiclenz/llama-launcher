---
title: "ADR-0015 why: a loading Splash was invisible and unstoppable, and a persisted launch record would reverse the stateless design"
---

A loading Splash was invisible to everything ADR-0010 built. `status` did not show it, `stop` refused it with "no server running", and a second `load` could fork a duplicate onto the same address. The loading takes about a minute for a large Model. ADR-0010's reasoning applies unchanged: the user who says "stop" means it, and a duplicate start must be refused.

Two other ways were considered:

- **A persisted launch record** (PID, address and start time written at fork, and removed on stop or when found stale). This would cover exactly the servers the launcher started and needs no command-line parsing. It was rejected because the launcher persists nothing between invocations (TDD §7). A record file would reverse that design, bring back the stale-state drift that live derivation removed, and conflict with the legacy `state-*.json` cleanup.
- **Leaving it documented as a limitation.** This was rejected because the duplicate-fork and unstoppable-load gaps are real bugs, not edge cases.

The attribution stays at least as strong as the one ADR-0010 already accepts: a bare 503 answer for llamacpp. Several things must line up at once: a group-leader process whose command line is a Splash launch with `--model`, the exact host and port configured for Splash, and nothing listening there. The foreign-occupant protection of ADR-0006 and ADR-0010 is not weakened. An address where nothing matches still refuses with "no server running", and a process that listens is never judged by its command line.
