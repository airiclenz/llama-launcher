# Plan: Backend Test Coverage — httptest + Real Integration Tests

> **Status (2026-07-19):** Layer 1 is shipped and kept below as historical record.
> The Layer 2 spec is superseded — see
> [Part B (Items 10–15) of the 2026-07-19 plan](docs/plans/2026-07-19-starting-stop-and-integration-tests.md).

## Context

The current test suite only covers config parsing, arg building, and UI rendering. All actual backend behavior — HTTP health checks, model load/unload API calls, and process start/stop — is completely untested. This plan adds two layers of tests:

1. **httptest-based tests** — mock HTTP servers that verify request/response handling; run with `go test ./...`
2. **Real integration tests** — start/stop actual backends; gated behind `//go:build integration`

---

## Layer 1: httptest Tests (no build tag)

Fast, deterministic tests using `net/http/httptest`. They verify that each backend's HTTP methods send correct requests and handle various responses. Run as part of normal `go test ./...`.

**How `addr` works:** Backend methods take `addr` as `host:port` and build URLs as `"http://" + addr + "/path"`. We parse `httptest.NewServer(...).URL` to extract `host:port`.

### New Files

#### `internal/launcher/backend_ollama_test.go`
Test cases (table-driven, `t.Parallel()`):
- **`TestOllamaHealthCheck`** — 200 with "Ollama" body → success; 200 without "Ollama" → error; 503 → error; unreachable → error
- **`TestOllamaLoadModel`** — 200 → success, verify POST to `/api/generate` with `"model"` and `"keep_alive":"24h"` in body; 500 → error
- **`TestOllamaUnloadModel`** — 200 → success, verify `"keep_alive":0` in body; 500 → error
- **`TestOllamaListRunningModels`** — valid JSON with models → returns slice; empty list → empty slice; invalid JSON → error

#### `internal/launcher/backend_lmstudio_test.go`
- **`TestLMStudioHealthCheck`** — 200 on `/v1/models` → success; 500 → error; unreachable → error
- **`TestLMStudioLoadModel`** — 200 → success, verify POST to `/api/v1/models/load` with `"model"` in body; with `ContextSize` set → body includes `"context_length"`; 400 with `{"error":{"message":"..."}}` → extracted error message; 500 without error body → generic status error
- **`TestLMStudioUnloadModel`** — 200 → success, verify `"identifier"` in body at `/api/v1/models/unload`; 400 with error JSON → extracted message

#### `internal/launcher/backend_llamacpp_test.go` (append to existing)
- **`TestLlamaCppHealthCheck`** — 200 on `/health` → success; 503 → error; unreachable → error

### Shared Helper
`addrFromURL(t, rawURL) string` — parses httptest URL, returns `host:port`. Lives in a `helpers_test.go` or inline in each file.

---

## Layer 2: Real Integration Tests (`//go:build integration`)

> **Superseded (2026-07-19).** The Layer-2 spec that lived here (written 2026-05-20)
> predates the `Backend` → `LLMServer` rename, ADR-0009 (the activation-operations
> seam), and the unified `Stop`/`Unload` entry points. The validated spec is now
> **Part B, Items 10–15** of
> [`docs/plans/2026-07-19-starting-stop-and-integration-tests.md`](docs/plans/2026-07-19-starting-stop-and-integration-tests.md):
> shared helpers, llamacpp/Ollama/LM Studio lifecycle suites, Makefile
> `test`/`test-integration`/`test-all` targets, and the docs pass. Notable delta:
> the llamacpp suite drives the real in-package start path (`StartServer`) instead
> of re-implementing `exec.Command`, and adds the ADR-0010 stop-while-Starting
> scenario. The superseded text also covered Makefile changes, a files table, and
> verification steps — all owned by that plan now.
