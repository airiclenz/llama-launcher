---
title: Splash install check refuses MLX models
status: open
type: bug
priority: 1
created: 2026-10-29T18:26:01Z
---

A Splash Profile whose model is an MLX model (e.g. `mlx-community/Qwen3.8-27B-4bit`) is refused by `load` and `config validate` with "splash model ... is not installed in ~/.cache/huggingface/hub", although Splash has installed and pinned it.

`splashModelInstalled` (internal/launcher/backend_splash.go) only accepts a pin under `refs/splash/<installation>/<commit>` whose snapshot holds a `manifest.json`. That file belongs to the old Splash package format (e.g. `incoai/Qwen3.8-27B-Splash`). Splash no longer loads those packages (Splash commit 539eae95 "Stop loading Splash packages"): it refuses them and points to the MLX model of the family. An MLX snapshot carries `config.json` and safetensors, no `manifest.json`, so every current Splash model fails the check.

Observed 2026-10-29 with llama-launcher 1.9.0: the model is pinned at `refs/splash/<installation>/10c35caafbb80f7dc6a7a432cdd11af10a6d4818`, the snapshot has `config.json` and `model-0000N-of-00003.safetensors`, and `splash serve --model mlx-community/Qwen3.8-27B-4bit` serves it.

## Why

Splash Profiles cannot be loaded through the launcher at all since Splash dropped its package format; the only workaround is starting `splash serve` by hand.

## Acceptance

- A Splash pin whose snapshot holds `config.json` counts as installed; a snapshot with `manifest.json` still counts, so older Splash installs keep working
- A repo with no `refs/splash` pin, or a pin whose snapshot is missing or holds neither file, is still refused with the one-time install command
- Tests in backend_splash_test.go cover the MLX case and the refusals; `make check` passes
- `llama-launcher config validate` passes for a Profile with `model: mlx-community/Qwen3.8-27B-4bit` on a machine where Splash installed it
