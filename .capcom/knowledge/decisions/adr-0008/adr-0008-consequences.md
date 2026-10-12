---
title: "ADR-0008 consequences: a resident control-plane process is a documented exception, and control grows only in the adapter"
---

- A small **resident control-plane process** now runs on the host — a conscious, documented exception to the zero-resident-memory goal. It is separate from `llama-launcher`, holds no Models, and only dispatches control commands.
- One new dependency (`github.com/modelcontextprotocol/go-sdk`) enters the module, scoped to the adapter binary; the core CLI build stays dependency-light.
- Allowlist strength depends on the bridge network not permitting source-IP spoofing from other reachable hosts; mitigated by binding narrowly and pinning a stable container IP.
- Adding control tools means extending the adapter's tool surface (`cmd/llama-launcher-mcp`), never adding a listener to the CLI.
