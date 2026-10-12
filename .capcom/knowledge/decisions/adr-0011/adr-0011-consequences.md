---
title: "ADR-0011 consequences: the facade is the SemVer surface, internal/launcher stays free to refactor, and the MCP adapter is unaffected"
---

- `internal/launcher/` gains exactly two seams, both behaviour-preserving: `LoadConfig` and the
  activation orchestration accept a notice sink, with the CLI binding stderr printers whose
  output is byte-identical to today's. Everything else the facade re-exports untouched.
- The facade is the SemVer surface. From `v1.6.0` on, changing a documented facade symbol is a
  breaking change; adding one is a minor bump. `internal/launcher/` remains free to refactor.
- The MCP adapter (ADR-0008) is unaffected — it shells out to the CLI and does not import
  `internal/launcher`. In-process library and remote MCP control are complementary access
  paths to the same core.
- The launcher stays agent-free: the dependency direction is strictly client → launcher. The
  facade adds no dependencies to `go.mod`.
- Doors left open, all additive: ctx-taking verb variants if a client ever needs Go-level
  cancellation; an instance-scoped handle if a multi-config client ever appears (requires
  de-globalising the backend registry first); exporting the network of narrower helpers
  (`EnsureServer`, `StartServer`, `WaitForHealth`, `FillRuntimeDetails`) if a client asks.
- `Config.Reload` remains CLI-flavoured (it prints warnings to stderr via `LoadConfig`'s
  binding); library clients re-read config by calling the facade's `LoadConfig` again. Recorded
  in the package documentation.
