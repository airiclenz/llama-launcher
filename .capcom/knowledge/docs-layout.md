---
title: "Before reading or changing a design: where the glossary, decisions, plans, handoffs and changelog live"
labels: [docs]
refs: [CONTEXT.md, docs/adr, docs/plans, docs/reviews, llama-launcher.TDD.md, CHANGELOG.md]
---

Read the relevant source before changing what it covers:

- Note `glossary`: the domain glossary, one Note per term. Use its terms in code, docs and
  messages; edit a term's file in place. `CONTEXT.md` is only a pointer to it.
- Note `decisions`: the architectural decision records, one `adr-NNNN` Note each. Read the
  relevant record before changing what it covers; edit records in place. `docs/adr/` is only a
  pointer to it.
- `llama-launcher.TDD.md`: the technical design: architecture, build commands, coding standards
  and contribution workflow.
- `docs/plans/`: implementation plans in the house format (numbered items with What, Tests,
  Acceptance and a commit line); a plan is a design artefact, not a to-do list. Finished ones
  move to `docs/plans/archived/`.
- `docs/handoffs/`: handoff documents for work in flight between sessions (kept out of git).
- `docs/reviews/`: code and security review reports.
- `CHANGELOG.md`: the closed trail of shipped changes.
