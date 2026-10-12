---
title: "ADR-0008 why shell out: the CLI's JSON output keeps the adapter a thin shim decoupled from the launcher's internals"
---

The adapter maps each MCP tool to a `llama-launcher` subcommand invocation and returns the output, passing `--json` where the CLI offers it. The CLI already emits purpose-built JSON (`status --json`, `list --json`) designed for exactly this kind of consumption; the lifecycle subcommands return exit-code-mapped text. Shelling out keeps the adapter a thin, auditable shim, decouples it from llml's internals, and lets it run against whatever installed version is on the host. Call volume is a handful of commands per session, so subprocess overhead is irrelevant. Importing `internal/launcher` in-process was rejected: it would pull llml's internals into a network-facing process for a latency win that does not matter here.
