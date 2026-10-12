---
title: "ADR-0006 decision: a running LLM Server is identified by its host:port, so any number of instances of a type can run"
---

A running LLM Server is identified by the `host:port` it binds to, not by its type. llml supports an arbitrary number of instances of any LLM Server type running concurrently, as long as each binds a distinct address. Two Profiles that resolve to the same `host:port` share an instance slot — activating one replaces the other on that address.
