# Plan: Backend Test Coverage — httptest + Real Integration Tests

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

Actually start/stop real backends. Each test auto-skips if the backend binary isn't in PATH. Model operations are optional, controlled by env vars.

### Run Command
```bash
go test -tags=integration -timeout 5m -v ./internal/launcher/
```

### New Files

#### `internal/launcher/integration_test.go`
Shared helpers (all with `t.Helper()`):
- **`mustFindBinary(t, name)`** — `exec.LookPath`; calls `t.Skip` if not found
- **`freePort(t)`** — binds `:0`, reads assigned port, closes listener
- **`waitForHealthy(t, backend, addr, timeout)`** — polls `HealthCheck` until success or timeout
- **`waitForUnhealthy(t, backend, addr, timeout)`** — polls until `HealthCheck` fails (confirms stop)

#### `internal/launcher/integration_ollama_test.go`
**`TestIntegrationOllama`** (sequential subtests, not parallel):
1. `mustFindBinary(t, "ollama")`
2. Get free port, construct addr
3. `t.Cleanup` → `TryStop` + kill by PID
4. Subtests: `TryStart` → `HealthCheck` → `LoadModel` (if `INTEGRATION_MODEL_OLLAMA` set) → `ListRunningModels` → `UnloadModel` → `TryStop` → verify unhealthy

#### `internal/launcher/integration_lmstudio_test.go`
**`TestIntegrationLMStudio`** — same structure:
1. `mustFindBinary(t, "lms")`
2. Subtests: `TryStart` → `HealthCheck` → `LoadModel` (if `INTEGRATION_MODEL_LMSTUDIO`) → `UnloadModel` → `TryStop`

#### `internal/launcher/integration_llamacpp_test.go`
**`TestIntegrationLlamaCpp`** — different because it's a managed backend:
1. `mustFindBinary(t, "llama-server")`
2. Requires `INTEGRATION_MODEL_LLAMACPP` (path to .gguf) — skip entirely if unset (llama-server needs a model arg to start)
3. Build `ResolvedProfile` with model path, host, port
4. Start process directly via `exec.Command` using `ServerBinary` + `BuildServerArgs`
5. `t.Cleanup` → kill process by PID
6. Subtests: Start → `HealthCheck` → Stop (SIGTERM) → verify unhealthy

### Environment Variables for Model Tests

| Variable | Example | Purpose |
|---|---|---|
| `INTEGRATION_MODEL_OLLAMA` | `llama3.2:1b` | Ollama model name (must be pre-pulled) |
| `INTEGRATION_MODEL_LMSTUDIO` | `lmstudio-community/...` | LM Studio model identifier |
| `INTEGRATION_MODEL_LLAMACPP` | `/path/to/model.gguf` | Absolute path to GGUF file |

When unset → model subtests skip; start/stop/health still run.

### Cleanup Strategy
- `t.Cleanup` registered immediately after deciding to start a process
- Attempts `TryStop(addr)` first
- Falls back to `SIGTERM` by PID
- `t.TempDir()` for log dirs (auto-cleaned)

### Port Conflicts
`freePort` uses OS-assigned ephemeral ports. No `t.Parallel()` on integration tests avoids races.

---

## Makefile Changes

Add to existing Makefile:

```makefile
test:
	go test ./...

test-integration:
	go test -tags=integration -timeout 5m -v ./internal/launcher/

test-all: test test-integration
```

---

## Files to Create/Modify

| File | Action |
|---|---|
| `internal/launcher/backend_ollama_test.go` | **Create** — httptest tests for Ollama HTTP methods |
| `internal/launcher/backend_lmstudio_test.go` | **Create** — httptest tests for LM Studio HTTP methods |
| `internal/launcher/backend_llamacpp_test.go` | **Modify** — add `TestLlamaCppHealthCheck` with httptest |
| `internal/launcher/helpers_test.go` | **Create** — `addrFromURL` shared helper |
| `internal/launcher/integration_test.go` | **Create** — shared integration helpers (build tag) |
| `internal/launcher/integration_ollama_test.go` | **Create** — real Ollama lifecycle tests (build tag) |
| `internal/launcher/integration_lmstudio_test.go` | **Create** — real LM Studio lifecycle tests (build tag) |
| `internal/launcher/integration_llamacpp_test.go` | **Create** — real LlamaCpp lifecycle tests (build tag) |
| `Makefile` | **Modify** — add `test`, `test-integration`, `test-all` targets |

---

## Verification

1. `go test ./...` — all existing + new httptest tests pass, integration tests excluded
2. `go test -tags=integration -timeout 5m -v ./internal/launcher/` — integration tests run, skipping backends not in PATH
3. `INTEGRATION_MODEL_OLLAMA=llama3.2:1b go test -tags=integration -timeout 5m -v -run TestIntegrationOllama ./internal/launcher/` — full Ollama lifecycle including model load/unload
4. `make test-integration` — same as #2 via Makefile
