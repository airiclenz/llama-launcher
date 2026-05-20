package launcher

import (
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
}
