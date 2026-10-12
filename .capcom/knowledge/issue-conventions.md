---
title: "Before filing or changing an issue: how issues are named, labelled, planned and closed"
labels: [issues]
---

Open defects, loose ends and parked feature ideas are capcom issues; follow these rules when you
file or change one.

- **Every new issue gets a spoken id:** `capcom create --id <slug> --title "<title>"`. The slug is
  two to four kebab-case words naming the defect or the work, never the fix (`api-key-cmd-reruns`,
  not `cache-api-key-cmd`). Run `capcom show <slug>` first when it might collide.
- **Pick the slug deliberately.** A rename goes through `capcom mv <id> <new-id>`; the old id stays
  an alias, so citations of it keep resolving.
- **An issue a plan has designed is planned, never re-statused:** `capcom update <id> --plan
  "<plan basename>"`. That makes it Planned (`capcom ready --planned` lists them for
  `/implement-plan`; `--unplanned` lists what still needs design). A plan's own *Out of scope*
  list disowns issues it merely mentions: read ownership from the plan's header and item sections.
- **Labels:** issues migrated from `TODO.md` carry `from-todo`; area labels (`docs`, `tests`,
  `release`, `security`, `menu`, `cli`, `config`, `keystore`, `windows`, `performance`) are
  free-form, but reuse an existing one before inventing another.
- **`CHANGELOG.md` is still the closed trail.** `capcom close` writes no changelog entry: do both.
