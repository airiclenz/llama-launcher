---
title: "ADR-0007 decision: loading a Profile already loaded at its address is a no-op that notices parameter drift and points to --restart"
---

When `llml load <profile>` is invoked against an address (`host:port`) that already has the requested Profile's Model loaded, the launcher does nothing by default — even if the Profile's resolved parameters in the config have changed since activation. If the resolved parameters differ from what is actually running, the launcher prints a notice naming the divergent fields and points the user to `llml load <profile> --restart`, which forces re-activation with the current parameters.
