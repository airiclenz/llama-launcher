package launcher

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLMStudioHealthCheck(t *testing.T) {
	t.Parallel()

	b := &LMStudio{}

	t.Run("healthy when exclusions fail", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"Unexpected endpoint"}`))
		})
		mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"Unexpected endpoint"}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		if err := b.HealthCheck(addrFromURL(t, srv.URL)); err != nil {
			t.Errorf("expected healthy, got: %v", err)
		}
	})

	t.Run("healthy when LM Studio returns 200 with error body for all paths", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"test","object":"model"}]}`))
		})
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"Unexpected endpoint or method. (GET /health)"}`))
		})
		mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"Unexpected endpoint or method. (GET /api/tags)"}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		if err := b.HealthCheck(addrFromURL(t, srv.URL)); err != nil {
			t.Errorf("expected healthy (all error bodies lack llamacpp/Ollama signatures), got: %v", err)
		}
	})

	t.Run("unhealthy status", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error for unhealthy status")
		}
		if !strings.Contains(err.Error(), "unhealthy") {
			t.Errorf("error = %q, want it to contain 'unhealthy'", err)
		}
	})

	t.Run("unreachable server", func(t *testing.T) {
		t.Parallel()
		if err := b.HealthCheck("127.0.0.1:1"); err == nil {
			t.Fatal("expected error for unreachable server")
		}
	})

	t.Run("detects llamacpp via /health body", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error when llamacpp is detected")
		}
		if !strings.Contains(err.Error(), "llamacpp") {
			t.Errorf("error = %q, want it to mention llamacpp", err)
		}
	})

	t.Run("detects Ollama via /api/tags body", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"error":"not llamacpp"}`))
		})
		mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"models":[]}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error when Ollama is detected")
		}
		if !strings.Contains(err.Error(), "Ollama") {
			t.Errorf("error = %q, want it to mention Ollama", err)
		}
	})
}

func TestLMStudioLoadModel(t *testing.T) {
	t.Parallel()

	b := &LMStudio{}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/models/load" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(body, &payload)
			if payload["model"] != "test-model" {
				t.Errorf("model = %v, want test-model", payload["model"])
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		profile := &ResolvedProfile{ModelPath: "test-model"}
		if err := b.LoadModel(addrFromURL(t, srv.URL), profile); err != nil {
			t.Errorf("expected success, got: %v", err)
		}
	})

	t.Run("includes context_length when set", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(body, &payload)
			if payload["context_length"] == nil {
				t.Error("expected context_length in payload")
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctxSize := 8192
		profile := &ResolvedProfile{
			ModelPath:     "test-model",
			ProfileParams: ProfileParams{ContextSize: &ctxSize},
		}
		if err := b.LoadModel(addrFromURL(t, srv.URL), profile); err != nil {
			t.Errorf("expected success, got: %v", err)
		}
	})

	t.Run("maps batch_size and flash_attn to the REST field names", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(body, &payload)
			if payload["eval_batch_size"] != float64(512) {
				t.Errorf("eval_batch_size = %v, want 512", payload["eval_batch_size"])
			}
			if payload["flash_attention"] != true {
				t.Errorf("flash_attention = %v, want true", payload["flash_attention"])
			}
			if _, ok := payload["batch_size"]; ok {
				t.Error("payload carries raw batch_size; want it mapped to eval_batch_size")
			}
			if _, ok := payload["flash_attn"]; ok {
				t.Error("payload carries raw flash_attn; want it mapped to flash_attention")
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		batchSize := 512
		flashAttn := true
		profile := &ResolvedProfile{
			ModelPath: "test-model",
			ProfileParams: ProfileParams{
				BatchSize: &batchSize,
				FlashAttn: &flashAttn,
			},
		}
		if err := b.LoadModel(addrFromURL(t, srv.URL), profile); err != nil {
			t.Errorf("expected success, got: %v", err)
		}
	})

	t.Run("omits unset params and never sends gpu_layers", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(body, &payload)
			// The load endpoint accepts no GPU-offload field, so gpu_layers
			// must not leak into the payload under any name.
			for _, key := range []string{"gpu_layers", "gpu", "context_length", "eval_batch_size", "flash_attention"} {
				if _, ok := payload[key]; ok {
					t.Errorf("payload unexpectedly carries %q", key)
				}
			}
			if payload["model"] != "test-model" {
				t.Errorf("model = %v, want test-model", payload["model"])
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		gpuLayers := 99
		profile := &ResolvedProfile{
			ModelPath:     "test-model",
			ProfileParams: ProfileParams{GPULayers: &gpuLayers},
		}
		if err := b.LoadModel(addrFromURL(t, srv.URL), profile); err != nil {
			t.Errorf("expected success, got: %v", err)
		}
	})

	t.Run("error with message", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{"message": "model not found"},
			})
		}))
		defer srv.Close()

		profile := &ResolvedProfile{ModelPath: "bad-model"}
		err := b.LoadModel(addrFromURL(t, srv.URL), profile)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "model not found") {
			t.Errorf("error = %q, want it to contain 'model not found'", err)
		}
	})

	t.Run("error without message", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		profile := &ResolvedProfile{ModelPath: "bad-model"}
		err := b.LoadModel(addrFromURL(t, srv.URL), profile)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "status 500") {
			t.Errorf("error = %q, want it to contain 'status 500'", err)
		}
	})
}

func TestLMStudioUnloadModel(t *testing.T) {
	t.Parallel()

	b := &LMStudio{}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/models/unload" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := b.UnloadModel(addrFromURL(t, srv.URL), "test-model"); err != nil {
			t.Errorf("expected success, got: %v", err)
		}
	})

	t.Run("non-200 with error message", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{"message": "model not loaded"},
			})
		}))
		defer srv.Close()

		err := b.UnloadModel(addrFromURL(t, srv.URL), "test-model")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "model not loaded") {
			t.Errorf("error = %q, want it to contain 'model not loaded'", err)
		}
	})

	t.Run("non-200 with empty body returns error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		err := b.UnloadModel(addrFromURL(t, srv.URL), "test-model")
		if err == nil {
			t.Fatal("expected error for non-200 with empty body")
		}
		if !strings.Contains(err.Error(), "status 500") {
			t.Errorf("error = %q, want it to contain 'status 500'", err)
		}
	})
}

// TestLMStudioListRunningModels_OversizedBody asserts the model-list read is
// bounded: a response larger than the cap fails to parse instead of being
// consumed in full (an unbounded decode would accept it).
func TestLMStudioListRunningModels_OversizedBody(t *testing.T) {
	t.Parallel()

	b := &LMStudio{}
	var body strings.Builder
	body.WriteString(`{"data":[`)
	for body.Len() < maxJSONBodyBytes+1024 {
		body.WriteString(`{"id":"model"},`)
	}
	body.WriteString(`{"id":"last"}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body.String()))
	}))
	defer srv.Close()

	_, err := b.ListRunningModels(addrFromURL(t, srv.URL))

	if err == nil {
		t.Fatal("expected a parse error for a model list larger than the read cap")
	}
	if !strings.Contains(err.Error(), "parsing /v1/models") {
		t.Errorf("error = %q, want it to contain 'parsing /v1/models'", err)
	}
}

func TestExtractLMStudioError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"valid error JSON", `{"error":{"message":"something went wrong"}}`, "something went wrong"},
		{"empty body", "", ""},
		{"malformed JSON", "{invalid", ""},
		{"JSON without error message", `{"status":"error"}`, ""},
		{"empty error message", `{"error":{"message":""}}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractLMStudioError([]byte(tt.body))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLMStudioAuthHeaders(t *testing.T) {
	t.Parallel()

	srv, authFor := recordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"m"}]}`))
		default:
			// LM Studio answers unknown paths with an error body; the
			// discrimination probes rely on exactly that shape.
			w.Write([]byte(`{"error":"Unexpected endpoint or method."}`))
		}
	})
	addr := addrFromURL(t, srv.URL)

	b := &LMStudio{apiKey: "k"}
	if err := b.HealthCheck(addr); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if err := b.LoadModel(addr, &ResolvedProfile{ModelPath: "m"}); err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if err := b.UnloadModel(addr, "m"); err != nil {
		t.Fatalf("UnloadModel: %v", err)
	}
	if _, err := b.ListRunningModels(addr); err != nil {
		t.Fatalf("ListRunningModels: %v", err)
	}
	for _, path := range []string{"/v1/models", "/health", "/api/tags", "/api/v1/models/load", "/api/v1/models/unload"} {
		if got := authFor(path); got != "Bearer k" {
			t.Errorf("Authorization on %s = %q, want %q", path, got, "Bearer k")
		}
	}

	noKey := &LMStudio{}
	if err := noKey.HealthCheck(addr); err != nil {
		t.Fatalf("HealthCheck without key: %v", err)
	}
	if got := authFor("/v1/models"); got != "" {
		t.Errorf("Authorization without key = %q, want empty", got)
	}
}

func TestLMStudioAuthFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	b := &LMStudio{}
	err := b.HealthCheck(addrFromURL(t, srv.URL))
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error = %v, want actionable api_key message", err)
	}
}
