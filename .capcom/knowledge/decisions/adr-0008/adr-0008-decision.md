---
title: "ADR-0008 decision: remote control is a separate llama-launcher-mcp binary that shells out to the CLI and never proxies inference"
---

`llama-launcher` (the CLI) stays a one-shot process manager with no network surface. When a client on another machine — typically a containerized coding agent — needs to control which Model is running on the host, that control is provided by a **separate, optional binary**, `llama-launcher-mcp`, which exposes the existing lifecycle commands as MCP tools over HTTP and implements each by shelling out to the CLI. The adapter dispatches *control* commands only; it never proxies inference traffic.
