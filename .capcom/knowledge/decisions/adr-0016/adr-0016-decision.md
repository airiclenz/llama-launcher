---
title: "ADR-0016 decision: api_key_cmd runs only from a config owned by the current uid with no group or other write bit"
---

`api_key_cmd` is handed whole to the platform shell (`sh -c`; `cmd /C` on Windows) at config load, so every line in that key is a command run as the user. On unix, the launcher runs those lines only when the config file is **owned by the current uid and not writable by its group or by others** (no bit of `0o022` set). A file that fails the check stops the load with an error naming the file and the fix (`chmod 600 <path>`, after making the file yours when someone else owns it), and no command runs. Windows is unchanged: its access control is ACLs, which neither a uid nor a mode word describes.

- **The config is trusted input, and this is the gate that makes it so.** The config file is the user's own: the launcher creates it at mode 0600 and its writers keep that mode. A line in it is the user's instruction, exactly like a line in their shell profile.
- **The file judged is the one read.** The check `os.Stat`s the path `parseConfig` read, so a symlinked config is judged by its target, never by the link.
- **Only a config that would run something is judged.** A config with no enabled `api_key_cmd` runs no command, so its mode is not the launcher's business and it loads as before.
- **Enforced at load, not reported by validation.** `config validate` runs no command and does not report the gate; a file that fails it validates clean and is refused by the next load.
