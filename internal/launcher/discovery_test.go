package launcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLlamaCppListRunningModels(t *testing.T) {
	t.Parallel()
	b := &LlamaCpp{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "/models/test-7b.gguf", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	models, err := b.ListRunningModels(addrFromURL(t, srv.URL))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 || models[0].Name != "/models/test-7b.gguf" {
		t.Errorf("got %+v, want one entry with id /models/test-7b.gguf", models)
	}
}

func TestLlamaCppQueryLiveParams(t *testing.T) {
	t.Parallel()
	b := &LlamaCpp{}

	t.Run("populated from /props", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/props" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.Write([]byte(`{
				"model_path": "/models/x.gguf",
				"total_slots": 2,
				"default_generation_settings": {
					"n_ctx": 8192,
					"temperature": 0.7,
					"top_k": 40
				}
			}`))
		}))
		defer srv.Close()

		params, err := b.QueryLiveParams(addrFromURL(t, srv.URL))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if params == nil {
			t.Fatal("expected non-nil params")
		}
		if params.ContextSize == nil || *params.ContextSize != 8192 {
			t.Errorf("ContextSize = %v, want 8192", params.ContextSize)
		}
		if params.Parallel == nil || *params.Parallel != 2 {
			t.Errorf("Parallel = %v, want 2", params.Parallel)
		}
		if params.Temperature == nil || *params.Temperature != 0.7 {
			t.Errorf("Temperature = %v, want 0.7", params.Temperature)
		}
		if params.TopK == nil || *params.TopK != 40 {
			t.Errorf("TopK = %v, want 40", params.TopK)
		}
	})

	t.Run("404 yields nil, nil", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		params, err := b.QueryLiveParams(addrFromURL(t, srv.URL))
		if err != nil {
			t.Errorf("expected no error for 404, got %v", err)
		}
		if params != nil {
			t.Errorf("expected nil params for 404, got %+v", params)
		}
	})
}

func TestDiscoverRunningInstances_NoBackendsReachable(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	port := 1 // a port nothing listens on
	host := "127.0.0.1"
	cfg.Defaults = ProfileParams{
		Server: strPtrLocal("llamacpp"),
		Host:   &host,
		Port:   &port,
	}

	instances := DiscoverRunningInstances(cfg)
	if len(instances) != 0 {
		t.Errorf("expected 0 instances reachable, got %d: %+v", len(instances), instances)
	}
}

func TestDiscoverRunningInstances_FindsReachable(t *testing.T) {
	t.Parallel()
	// Start a fake llama.cpp that satisfies health + /v1/models + /props.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{"id": "/models/x.gguf"}},
			})
		case "/props":
			w.Write([]byte(`{"default_generation_settings":{"n_ctx":4096}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	host, portInt := hostPort(t, srv.URL)
	cfg := &Config{
		Servers:  map[string]ServerConfig{"llamacpp": {Enabled: true}},
		LogDir:   t.TempDir(),
		Profiles: map[string]Profile{},
	}
	cfg.Defaults = ProfileParams{
		Server: strPtrLocal("llamacpp"),
		Host:   &host,
		Port:   &portInt,
	}

	instances := DiscoverRunningInstances(cfg)
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d: %+v", len(instances), instances)
	}
	inst := instances[0]
	if inst.Backend != "llamacpp" {
		t.Errorf("Backend = %q, want llamacpp", inst.Backend)
	}
	if inst.ActiveModel != "/models/x.gguf" {
		t.Errorf("ActiveModel = %q, want /models/x.gguf", inst.ActiveModel)
	}
	if inst.ResolvedParams.ContextSize == nil || *inst.ResolvedParams.ContextSize != 4096 {
		t.Errorf("ContextSize = %v, want 4096", inst.ResolvedParams.ContextSize)
	}
}

func TestMatchProfileName(t *testing.T) {
	t.Parallel()
	modelsDir := t.TempDir()
	modelFile := filepath.Join(modelsDir, "gpt-oss-20b-MXFP4.gguf")
	if err := writeEmpty(modelFile); err != nil {
		t.Fatal(err)
	}

	host := "127.0.0.1"
	port := 9090
	cfg := &Config{
		Servers:   map[string]ServerConfig{"llamacpp": {Enabled: true}},
		ModelsDir: modelsDir,
		Profiles: map[string]Profile{
			"oss": {Model: "gpt-oss-20b-MXFP4.gguf"},
		},
	}
	cfg.Defaults = ProfileParams{
		Server: strPtrLocal("llamacpp"),
		Host:   &host,
		Port:   &port,
	}

	inst := &RunningInstance{Backend: "llamacpp", Host: host, Port: port}

	// A server started outside the launcher reports only the file basename.
	inst.ActiveModel = "gpt-oss-20b-MXFP4.gguf"
	if got := matchProfileName(cfg, inst); got != "oss" {
		t.Errorf("basename match = %q, want oss", got)
	}

	inst.ActiveModel = modelFile
	if got := matchProfileName(cfg, inst); got != "oss" {
		t.Errorf("exact path match = %q, want oss", got)
	}

	inst.ActiveModel = "other-model.gguf"
	if got := matchProfileName(cfg, inst); got != "" {
		t.Errorf("mismatched model = %q, want empty", got)
	}

	// A second profile whose model shares the basename: the exact path
	// match must win, while a bare basename becomes ambiguous.
	altDir := filepath.Join(modelsDir, "alt")
	if err := os.MkdirAll(altDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeEmpty(filepath.Join(altDir, "gpt-oss-20b-MXFP4.gguf")); err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["oss-alt"] = Profile{Model: "alt/gpt-oss-20b-MXFP4.gguf"}

	inst.ActiveModel = modelFile
	if got := matchProfileName(cfg, inst); got != "oss" {
		t.Errorf("exact match with loose sibling = %q, want oss", got)
	}

	inst.ActiveModel = "gpt-oss-20b-MXFP4.gguf"
	if got := matchProfileName(cfg, inst); got != "" {
		t.Errorf("ambiguous basename = %q, want empty", got)
	}
}

func TestInstancesSignature(t *testing.T) {
	t.Parallel()
	idle := []*RunningInstance{{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080}}
	loaded := []*RunningInstance{{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, ActiveModel: "/models/x.gguf"}}
	loadedAgain := []*RunningInstance{{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, ActiveModel: "/models/x.gguf"}}

	if got := instancesSignature(nil); got != "" {
		t.Errorf("signature of no instances = %q, want empty", got)
	}
	if instancesSignature(idle) == instancesSignature(loaded) {
		t.Error("loading a model must change the signature")
	}
	if instancesSignature(nil) == instancesSignature(idle) {
		t.Error("a server appearing must change the signature")
	}
	if instancesSignature(loaded) != instancesSignature(loadedAgain) {
		t.Error("identical state must produce identical signatures")
	}
}

func TestFindManagedLogFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create two log files; the later timestamp should be returned.
	older := dir + "/llamacpp-20240101-000000.log"
	newer := dir + "/llamacpp-20260101-000000.log"
	other := dir + "/ollama-20260101-000000.log"
	for _, p := range []string{older, newer, other} {
		if err := writeEmpty(p); err != nil {
			t.Fatal(err)
		}
	}

	if got := findManagedLogFile(dir, "llamacpp"); got != newer {
		t.Errorf("findManagedLogFile = %q, want %q", got, newer)
	}
	if got := findManagedLogFile(dir, "ollama"); got != other {
		t.Errorf("findManagedLogFile (ollama) = %q, want %q", got, other)
	}
	if got := findManagedLogFile(dir, "lmstudio"); got != "" {
		t.Errorf("findManagedLogFile (lmstudio) = %q, want empty", got)
	}
}

// strPtrLocal is a tiny helper used by tests in this file to avoid colliding
// with similar helpers elsewhere in the package.
func strPtrLocal(s string) *string { return &s }

func hostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	addr := addrFromURL(t, rawURL)
	host, portStr, ok := strings.Cut(addr, ":")
	if !ok {
		t.Fatalf("bad addr: %q", addr)
	}
	var port int
	for _, c := range portStr {
		if c < '0' || c > '9' {
			t.Fatalf("bad port: %q", portStr)
		}
		port = port*10 + int(c-'0')
	}
	return host, port
}

func writeEmpty(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}
