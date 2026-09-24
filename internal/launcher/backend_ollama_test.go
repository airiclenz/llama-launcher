package launcher

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

	t.Run("maps 401 to ErrAuthFailed", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		err := b.HealthCheck(addrFromURL(t, srv.URL))
		if !errors.Is(err, ErrAuthFailed) {
			t.Errorf("error = %v, want ErrAuthFailed", err)
		}
		if err == nil || !strings.Contains(err.Error(), "check api_key") {
			t.Errorf("error = %v, want actionable check api_key message", err)
		}
	})

	t.Run("rejects a body whose Ollama marker sits past the read cap", func(t *testing.T) {
		t.Parallel()
		// The bounded read only sees the padding; an unbounded read would
		// find the marker and accept the response as healthy.
		body := strings.Repeat("x", maxResponseBytes) + "Ollama is running"
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

func TestOllamaTryStop_IsNoOpAndNeverErrors(t *testing.T) {
	t.Parallel()

	// TryStop used to run `ollama stop` (which errors without a model argument)
	// and then SIGTERM every `ollama serve` on the host. It is now a pure no-op:
	// the launcher stops the specific instance via the address-scoped PID path.
	b := &Ollama{}
	if err := b.TryStop("127.0.0.1:11434"); err != nil {
		t.Errorf("TryStop = %v, want nil", err)
	}
}

// TestOllamaTryStart_LastStartedFieldsAreRaceFree drives TryStart against a
// stub `ollama` on PATH while other goroutines read the PIDTracker accessors;
// under -race it fails if the last-started fields are written or read without
// the backend's mutex. Not parallel: it replaces PATH.
func TestOllamaTryStart_LastStartedFieldsAreRaceFree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TryStart refuses to spawn without process control on windows")
	}

	binDir := t.TempDir()
	stub := filepath.Join(binDir, "ollama")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing stub ollama: %v", err)
	}
	t.Setenv("PATH", binDir)

	cfg := &Config{LogDir: t.TempDir()}
	b := &Ollama{}
	const starts = 5

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = b.LastStartedPID()
					_ = b.LastStartedLogFile()
				}
			}
		}()
	}

	for i := range starts {
		if err := b.TryStart(cfg, "127.0.0.1:11434"); err != nil {
			close(stop)
			readers.Wait()
			t.Fatalf("TryStart #%d: %v", i+1, err)
		}
	}
	close(stop)
	readers.Wait()

	if pid := b.LastStartedPID(); pid <= 0 {
		t.Errorf("LastStartedPID = %d, want the stub's PID", pid)
	}
	if logFile := b.LastStartedLogFile(); filepath.Dir(logFile) != cfg.LogDir {
		t.Errorf("LastStartedLogFile = %q, want a file in %q", logFile, cfg.LogDir)
	}
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

	b := &Ollama{}
	b.setAPIKey("k")
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
