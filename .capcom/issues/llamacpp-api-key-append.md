---
title: Confirm llama.cpp --api-key append-vs-replace semantics
status: done
type: task
priority: 1
labels: [from-todo, security]
deps: [changelog-extra-args-claim]
aliases: [llama-launcher-llamacpp-api-key-append]
created: 2026-09-24T07:19:48Z
closed: 2026-09-24T19:10:35Z
reason: "Corrective CHANGELOG entry shipped in 1.8.0 (Security: extra_args --api-key does not replace api_key; both keys valid)."
---

llama.cpp appends `--api-key` values rather than replacing, so an `extra_args` override likely makes both keys valid rather than the override strictly winning; confirm against `common/arg.cpp` before the release. The comment asserting that the override wins is at `internal/launcher/backend_llamacpp.go:153`.

Origin: deferred from the security-hardening and key-migration plan.

## Log

- 2026-09-24 beads: settled empirically on llama-server b10851 (commit 67672dc5b) by `TestLlamaServerAPIKey` (`internal/launcher/server_integration_test.go`, `make test-integration`): llama.cpp **appends** — with `LLAMA_API_KEY` from the launcher and `--api-key` from `extra_args`, **both keys are valid** (each answers 200 on `/v1/models`, a wrong key 401). An override therefore never revokes the configured key; this is the fact the unconditional log redaction guards against. The same run found neither key echoed into llama-server's own log file. The `backend_llamacpp.go` comment, README, and TDD §4.2 are corrected; the released 1.7.0 CHANGELOG entry still carries the old claim.
- 2026-09-24 beads: api-key-residue-doc-fixes plan (bee1a08) landed the confirmation and the code/README/TDD corrections; left open until the corrective CHANGELOG entry for `CHANGELOG.md:17` is applied.
