---
title: "ADR-0013 why import the library: the Gateway sits on the request path, where polling the CLI is the wrong tool"
---

ADR-0008 chose shell-out for the MCP adapter: a handful of control commands per session, thin auditable shim. The Gateway sits on the request path: it must watch Starting instances for readiness, derive serving addresses continuously, and make activation decisions per request — polling the CLI for that is the wrong tool. The public facade exists for exactly this class of consumer (ADR-0011), and ADR-0012's posture carries over: the Gateway compiles everywhere and actuates where the library can.
