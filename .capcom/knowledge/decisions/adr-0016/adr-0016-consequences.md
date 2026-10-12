---
title: "ADR-0016 consequences: a loosened config with api_key_cmd is refused naming chmod 600, and validate does not report the gate"
---

- A user whose config has drifted to a looser mode (for example `0664` after an editor or a sync tool rewrote it) and who uses `api_key_cmd` gets a refusal naming `chmod 600 <path>` instead of a started launcher. Configs without `api_key_cmd` are unaffected at any mode.
- A config shared through a group-writable location, or owned by another account (a config under another user's home, a root-owned file), cannot use `api_key_cmd`; such a setup keeps a literal `api_key` or moves the file.
- The directory holding the config is not checked: an attacker who can replace the file in a writable directory produces a file they own, which the uid check refuses.
- `config validate` stays a pure parse-and-check pass; the gate surfaces only on load.
