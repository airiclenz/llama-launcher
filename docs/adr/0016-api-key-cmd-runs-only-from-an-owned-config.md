# api_key_cmd runs only from a config the user owns

`api_key_cmd` is handed whole to the platform shell (`sh -c`; `cmd /C` on Windows) at config load, so every line in that key is a command run as the user. On unix, the launcher runs those lines only when the config file is **owned by the current uid and not writable by its group or by others** (no bit of `0o022` set). A file that fails the check stops the load with an error naming the file and the fix (`chmod 600 <path>`, after making the file yours when someone else owns it), and no command runs. Windows is unchanged: its access control is ACLs, which neither a uid nor a mode word describes.

- **The config is trusted input, and this is the gate that makes it so.** The config file is the user's own: the launcher creates it at mode 0600 and its writers keep that mode. A line in it is the user's instruction, exactly like a line in their shell profile.
- **The file judged is the one read.** The check `os.Stat`s the path `parseConfig` read, so a symlinked config is judged by its target, never by the link.
- **Only a config that would run something is judged.** A config with no enabled `api_key_cmd` runs no command, so its mode is not the launcher's business and it loads as before.
- **Enforced at load, not reported by validation.** `config validate` runs no command and does not report the gate; a file that fails it validates clean and is refused by the next load.

## Why

The 2026-09-24 code audit flagged `api_key_cmd` as executed whole through a shell. Splitting the line into an argv in Go was considered and rejected: a command resolver without a shell still runs whatever program the line names, so it would not stop a hostile config — anyone who can write the file can name any binary. It would also force every pipeline user (`pass show llamacpp | head -1`) into a wrapper script. The actual premise of running the line is that only the user could have written it; the mode 0600 the launcher creates the file with stated that premise, but nothing checked it. A config left group- or world-writable, or one owned by another account, is exactly the case where that premise is false, and the gate refuses it.

## Consequences

- A user whose config has drifted to a looser mode (for example `0664` after an editor or a sync tool rewrote it) and who uses `api_key_cmd` gets a refusal naming `chmod 600 <path>` instead of a started launcher. Configs without `api_key_cmd` are unaffected at any mode.
- A config shared through a group-writable location, or owned by another account (a config under another user's home, a root-owned file), cannot use `api_key_cmd`; such a setup keeps a literal `api_key` or moves the file.
- The directory holding the config is not checked: an attacker who can replace the file in a writable directory produces a file they own, which the uid check refuses.
- `config validate` stays a pure parse-and-check pass; the gate surfaces only on load.
