package launcher

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaHealthCheck(t *testing.T) {
	t.Parallel()

	b := &Ollama{}

	t.Run("healthy with Ollama body", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("Ollama is running"))
		}))
		defer srv.Close()

		if err := b.HealthCheck(addrFromURL(t, srv.URL)); err != nil {
			t.Errorf("expected healthy, got: %v", err)
		}
	})

	t.Run("rejects empty body", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := b.HealthCheck(addrFromURL(t, srv.URL)); err == nil {
			t.Fatal("expected error for empty body")
		}
	})

	t.Run("rejects non-Ollama body", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("some other server"))
		}))
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error for non-Ollama body")
		}
		if !strings.Contains(err.Error(), "unexpected response") {
			t.Errorf("error = %q, want it to contain 'unexpected response'", err)
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

	t.Run("rejects a body whose Ollama marker sits past the read cap", func(t *testing.T) {
		t.Parallel()
		// The bounded read only sees the padding; an unbounded read would
		// find the marker and accept the response as healthy.
		body := strings.Repeat("x", maxStatusBodyBytes) + "Ollama is running"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error when the marker sits past the read cap")
		}
		if !strings.Contains(err.Error(), "unexpected response") {
			t.Errorf("error = %q, want it to contain 'unexpected response'", err)
		}
	})
}

func TestOllamaLoadModel(t *testing.T) {
	t.Parallel()

	b := &Ollama{}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/generate" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(body, &payload)
			if payload["model"] != "llama3" {
				t.Errorf("model = %v, want llama3", payload["model"])
			}
			if payload["keep_alive"] != "24h" {
				t.Errorf("keep_alive = %v, want 24h", payload["keep_alive"])
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		profile := &ResolvedProfile{ModelPath: "llama3"}
		if err := b.LoadModel(addrFromURL(t, srv.URL), profile); err != nil {
			t.Errorf("expected success, got: %v", err)
		}
	})

	t.Run("error status", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		profile := &ResolvedProfile{ModelPath: "bad-model"}
		err := b.LoadModel(addrFromURL(t, srv.URL), profile)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "status 404") {
			t.Errorf("error = %q, want it to contain 'status 404'", err)
		}
	})
}

func TestOllamaUnloadModel(t *testing.T) {
	t.Parallel()

	b := &Ollama{}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/generate" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			json.Unmarshal(body, &payload)
			if payload["keep_alive"] != float64(0) {
				t.Errorf("keep_alive = %v, want 0", payload["keep_alive"])
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := b.UnloadModel(addrFromURL(t, srv.URL), "llama3"); err != nil {
			t.Errorf("expected success, got: %v", err)
		}
	})

	t.Run("error status", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		err := b.UnloadModel(addrFromURL(t, srv.URL), "llama3")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "status 500") {
			t.Errorf("error = %q, want it to contain 'status 500'", err)
		}
	})
}

func TestOllamaListRunningModels(t *testing.T) {
	t.Parallel()

	b := &Ollama{}

	t.Run("success with models", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/ps" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{
					{"name": "llama3:latest", "size": 4000000000},
					{"name": "mistral:7b", "size": 3500000000},
				},
			})
		}))
		defer srv.Close()

		models, err := b.ListRunningModels(addrFromURL(t, srv.URL))
		if err != nil {
			t.Fatalf("expected success, got: %v", err)
		}
		if len(models) != 2 {
			t.Fatalf("len = %d, want 2", len(models))
		}
		if models[0].Name != "llama3:latest" {
			t.Errorf("models[0].Name = %q, want llama3:latest", models[0].Name)
		}
	})

	t.Run("empty model list", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"models": []map[string]interface{}{},
			})
		}))
		defer srv.Close()

		models, err := b.ListRunningModels(addrFromURL(t, srv.URL))
		if err != nil {
			t.Fatalf("expected success, got: %v", err)
		}
		if len(models) != 0 {
			t.Errorf("len = %d, want 0", len(models))
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("{invalid"))
		}))
		defer srv.Close()

		_, err := b.ListRunningModels(addrFromURL(t, srv.URL))
		if err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

func TestOllamaAuthHeaders(t *testing.T) {
	t.Parallel()

	srv, authFor := recordingServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte("Ollama is running"))
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"models":[]}`))
		default:
			w.Write([]byte(`{}`))
		}
	})
	addr := addrFromURL(t, srv.URL)

	b := &Ollama{apiKey: "k"}
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
	for _, path := range []string{"/", "/api/generate", "/api/ps"} {
		if got := authFor(path); got != "Bearer k" {
			t.Errorf("Authorization on %s = %q, want %q", path, got, "Bearer k")
		}
	}

	noKey := &Ollama{}
	if err := noKey.HealthCheck(addr); err != nil {
		t.Fatalf("HealthCheck without key: %v", err)
	}
	if got := authFor("/"); got != "" {
		t.Errorf("Authorization without key = %q, want empty", got)
	}
}
