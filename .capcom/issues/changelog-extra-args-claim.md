---
title: "CHANGELOG: correct the 1.7.0 llamacpp extra_args --api-key claim"
status: done
type: chore
priority: 1
labels: [docs, from-todo, release, security]
deps: [changelog-unreleased-heading-missing]
aliases: [llama-launcher-changelog-extra-args-claim]
created: 2026-09-24T07:19:52Z
closed: 2026-09-24T08:04:33Z
reason: "Corrective Security entry added under ## Unreleased; resolved by splash-backend plan (docs/plans/archived/2026-09-24 - 00 - splash-backend-plan.md), 4d8f7fa"
---

`CHANGELOG.md:17` (released 1.7.0 Security entry) still says llama-server reads `LLAMA_API_KEY` only when no `--api-key` flag is present, "so a user's own `extra_args` override still wins". `TestLlamaServerAPIKey` showed the opposite on b10851: the flag's key is appended, both keys authenticate. The corrective entry could not be applied because `CHANGELOG.md` has no `## [Unreleased]` heading (`CHANGELOG.md:3`); the release step must add it under the new version heading:

- **Corrected: an `extra_args` `--api-key` for llamacpp does not replace the configured `api_key` — both keys are accepted.** The 1.7.0 notes said an override would win; the new integration test (`TestLlamaServerAPIKey`, `make test-integration`) found the flag's key appended to the environment key on llama.cpp b10851, so both authenticate. To change the key, change `api_key` rather than adding an override. The same test found neither key echoed into llama-server's own log file. README and TDD now say so.

Origin: deferred from the api-key residue and doc-fixes plan.
