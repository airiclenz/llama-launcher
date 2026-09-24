# A Splash Model must already be installed; the launcher never downloads one

A Splash Profile names its Model as a Hugging Face `owner/repo` id. Before a Splash server is started, `ResolveModel` checks that the Model is installed, and refuses one that is not with an error naming the one-time manual install command (`splash serve --model <owner/repo>`, run once in a terminal). The launcher never runs that install itself.

- **Installed means a pinned, complete snapshot in the Hugging Face cache.** For the Model's cache directory `<hub>/models--<owner>--<repo>/`, some `refs/splash/<installation>/<rev>` ref must exist and `snapshots/<rev>/manifest.json` must be a regular file. `<hub>` is resolved the way Splash resolves it through `huggingface_hub`: `$HF_HUB_CACHE`, else `$HF_HOME/hub`, else `$XDG_CACHE_HOME/huggingface/hub`, else `~/.cache/huggingface/hub`, with an empty variable counting as unset.
- **The check reads only the cache.** It never looks up `splash` on PATH, so config validation, discovery, unload and `show-config` work on a machine where the `splash` command is not reachable. Only starting a Splash server needs `splash` on PATH.
- **The Model ref is validated first**, mirroring Splash's own repo-id rule, so a ref like `../..` cannot escape the cache path.

## Why

Splash downloads a missing Model when `splash serve` starts, and that download can take around twenty minutes. It runs before the server binds its address, so the launcher cannot see it: there is no port to probe, discovery reports nothing, and neither the Starting state ([ADR-0010](0010-starting-instances-are-visible-and-stoppable.md)) nor `llml stop` reaches it. A `load` would sit in its ~30 s health wait, time out, and leave an invisible, unstoppable download behind. Refusing up front and naming the install command keeps the long step visible, in a terminal the user controls.

Two subordinate choices, made deliberately:

- **The Hugging Face cache, not Splash's install root.** Splash also records installs under its own root, but the launcher cannot find that root reliably: the `splash` command on PATH may be a wrapper script (Splash's installer writes one, and a source checkout needs one because its script derives its root from `dirname $0`, which a plain symlink breaks), so deriving the root from the binary's location is wrong. Every installed Splash package keeps a pinned snapshot in the Hugging Face cache in both source and packaged layouts, so the cache answers the question without knowing the root.
- **A manifest alone is not enough.** Splash fetches `manifest.json` before the weights and writes the `refs/splash/*/<rev>` pin only after the download is verified, so a manifest without a pin can be an interrupted download. Requiring both refuses a half-installed Model instead of starting a server that would resume the download invisibly.

## Consequences

- A Splash Profile whose Model is not installed fails at resolution with the install command in the error, on every path that resolves Profiles, not only at start.
- The check follows Splash's cache layout. If Splash changes where or how it pins installed snapshots, this check must change with it.
- A Model installed into a different hub than the launcher's environment resolves (for example a launchd or MCP-adapter process with a different `HF_HOME`) is reported as not installed.
- The launcher never drives Splash's download or install; that stays out of scope.
