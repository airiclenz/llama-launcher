---
title: "ADR-0016 why: splitting the line into an argv would not stop a hostile config, and the 0600 premise went unchecked"
---

The 2026-09-24 code audit flagged `api_key_cmd` as executed whole through a shell. Splitting the line into an argv in Go was considered and rejected: a command resolver without a shell still runs whatever program the line names, so it would not stop a hostile config — anyone who can write the file can name any binary. It would also force every pipeline user (`pass show llamacpp | head -1`) into a wrapper script. The actual premise of running the line is that only the user could have written it; the mode 0600 the launcher creates the file with stated that premise, but nothing checked it. A config left group- or world-writable, or one owned by another account, is exactly the case where that premise is false, and the gate refuses it.
