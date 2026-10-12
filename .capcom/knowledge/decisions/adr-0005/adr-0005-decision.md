---
title: "ADR-0005 decision: every Profile names its LLM Server with server:, and the defaults.server fallback warns and is deprecated"
---

Every Profile in the config declares which LLM Server it targets via `server:`. The current `defaults.server` field is soft-deprecated: if a Profile omits `server:` and only one LLM Server is enabled, the launcher auto-detects (existing behaviour). If a Profile omits `server:` and more than one LLM Server is enabled, validation emits a warning naming the Profile and the `defaults.server` fallback used. The fallback is removed entirely in a later release.
