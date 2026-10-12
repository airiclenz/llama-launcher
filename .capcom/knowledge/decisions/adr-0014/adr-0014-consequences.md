---
title: "ADR-0014 consequences: resolution fails with the install command, and the check follows Splash's cache layout"
---

- A Splash Profile whose Model is not installed fails at resolution with the install command in the error, on every path that resolves Profiles, not only at start.
- The check follows Splash's cache layout. If Splash changes where or how it pins installed snapshots, this check must change with it.
- A Model installed into a different hub than the launcher's environment resolves (for example a launchd or MCP-adapter process with a different `HF_HOME`) is reported as not installed.
- The launcher never drives Splash's download or install; that stays out of scope.
