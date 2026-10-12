---
title: "ADR-0014 why: Splash's download runs before it binds, so the launcher could neither see nor stop it"
---

Splash downloads a missing Model when `splash serve` starts, and that download can take around twenty minutes. It runs before the server binds its address, so the launcher cannot see it: there is no port to probe, discovery reports nothing, and neither the Starting state (ADR-0010) nor `llml stop` reaches it. A `load` would sit in its ~30 s health wait, time out, and leave an invisible, unstoppable download behind. Refusing up front and naming the install command keeps the long step visible, in a terminal the user controls.

Two subordinate choices, made deliberately:

- **The Hugging Face cache, not Splash's install root.** Splash also records installs under its own root, but the launcher cannot find that root reliably: the `splash` command on PATH may be a wrapper script (Splash's installer writes one, and a source checkout needs one because its script derives its root from `dirname $0`, which a plain symlink breaks), so deriving the root from the binary's location is wrong. Every installed Splash package keeps a pinned snapshot in the Hugging Face cache in both source and packaged layouts, so the cache answers the question without knowing the root.
- **A manifest alone is not enough.** Splash fetches `manifest.json` before the weights and writes the `refs/splash/*/<rev>` pin only after the download is verified, so a manifest without a pin can be an interrupted download. Requiring both refuses a half-installed Model instead of starting a server that would resume the download invisibly.
