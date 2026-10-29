---
title: "ADR-0014 decision: ResolveModel refuses a Splash Model without a pinned, complete snapshot in the Hugging Face cache"
---

A Splash Profile names its Model as a Hugging Face `owner/repo` id. Before a Splash server is started, `ResolveModel` checks that the Model is installed, and refuses one that is not with an error naming the one-time manual install command (`splash serve --model <owner/repo>`, run once in a terminal). The launcher never runs that install itself.

- **Installed means a pinned, complete snapshot in the Hugging Face cache.** For the Model's cache directory `<hub>/models--<owner>--<repo>/`, some `refs/splash/<installation>/<rev>` ref must exist and `snapshots/<rev>/` must hold a regular `config.json` (an MLX snapshot, what Splash installs today) or `manifest.json` (the old Splash package format). `<hub>` is resolved the way Splash resolves it through `huggingface_hub`: `$HF_HUB_CACHE`, else `$HF_HOME/hub`, else `$XDG_CACHE_HOME/huggingface/hub`, else `~/.cache/huggingface/hub`, with an empty variable counting as unset. *(Amended 2026-10-29: installed originally required manifest.json.)*
- **The check reads only the cache.** It never looks up `splash` on PATH, so config validation, discovery, unload and `show-config` work on a machine where the `splash` command is not reachable. Only starting a Splash server needs `splash` on PATH.
- **The Model ref is validated first**, mirroring Splash's own repo-id rule, so a ref like `../..` cannot escape the cache path.
