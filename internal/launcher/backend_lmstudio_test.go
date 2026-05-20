package launcher

import (
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
			w.WriteHeader(http.StatusNotFound)
		})
		mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		if err := b.HealthCheck(addrFromURL(t, srv.URL)); err != nil {
			t.Errorf("expected healthy, got: %v", err)
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

	t.Run("detects llamacpp via /health", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
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

	t.Run("detects Ollama via /api/tags", func(t *testing.T) {
		t.Parallel()
		mux := http.NewServeMux()
		mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
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
