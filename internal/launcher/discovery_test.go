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

// TestListRunningModelsBoundedBody covers the response-size cap on the JSON
// decode path: a hostile process squatting the port must not be able to make
// the launcher allocate an unbounded model list — the read stops at
// maxResponseBytes and the truncated JSON surfaces as a parse error.
func TestListRunningModelsBoundedBody(t *testing.T) {
	t.Parallel()

	b := &LlamaCpp{}
	oversized := `{"data":[{"id":"` + strings.Repeat("A", maxResponseBytes) + `"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(oversized))
	}))
	defer srv.Close()

	_, err := b.ListRunningModels(addrFromURL(t, srv.URL))

	if err == nil {
		t.Fatal("expected error for oversized /v1/models body")
	}
	if !strings.Contains(err.Error(), "parsing /v1/models response") {
		t.Errorf("error = %q, want a parse error from the truncated body", err)
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
					"params": {"temperature": 0.7, "top_k": 40}
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
		// /props reports n_ctx per slot; the total is n_ctx * total_slots.
		if params.ContextSize == nil || *params.ContextSize != 16384 {
			t.Errorf("ContextSize = %v, want 16384", params.ContextSize)
		}
		if params.Parallel == nil || *params.Parallel != 2 {
			t.Errorf("Parallel = %v, want 2", params.Parallel)
		}
		// Sampling parameters are deliberately not read: the launch flags
		// only set request defaults that API clients override per call, and
		// /props floats need not round-trip the configured values exactly,
		// so diffing them would raise spurious drift notices.
		if params.Temperature != nil {
			t.Errorf("Temperature = %v, want nil (not read)", params.Temperature)
		}
		if params.TopK != nil {
			t.Errorf("TopK = %v, want nil (not read)", params.TopK)
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
	// Start a fake llama.cpp that satisfies health + /v1/models. Discovery
	// must not probe /props — live params are queried on demand by
	// liveParamDrift, not collected per instance on every discovery pass.
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
			t.Error("discovery probed /props; live params must not be collected during discovery")
			w.WriteHeader(http.StatusNotFound)
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
}

// TestDiscoverRunningInstances_SanitizesModelName covers the hostile-server
// case: the model name is reported by whatever answers on the configured
// port and is printed raw by every display site, so ANSI/OSC escapes (here
// an OSC title-spoof sequence) must be stripped when the name enters
// RunningInstance.
func TestDiscoverRunningInstances_SanitizesModelName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{"id": "\x1b]0;pwn\x07evil.gguf"}},
			})
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
	if got := instances[0].ActiveModel; got != "]0;pwnevil.gguf" {
		t.Errorf("ActiveModel = %q, want %q (escape bytes stripped)", got, "]0;pwnevil.gguf")
	}
}

// TestDiscoverRunningInstances_ReportsStarting covers ADR-0010: a
// llama-server answering /health with 503 while it loads its model is a
// Starting instance, not an absent one. Discovery must report it without
// querying the model list — the loading server cannot answer /v1/models —
// so ActiveModel and ActiveProfile stay empty.
func TestDiscoverRunningInstances_ReportsStarting(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/v1/models":
			t.Error("discovery queried /v1/models on a Starting instance; a loading server cannot answer")
			w.WriteHeader(http.StatusServiceUnavailable)
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
		t.Fatalf("expected 1 Starting instance, got %d: %+v", len(instances), instances)
	}
	inst := instances[0]
	if !inst.Starting {
		t.Error("Starting = false, want true for a 503 /health answer")
	}
	if inst.Backend != "llamacpp" {
		t.Errorf("Backend = %q, want llamacpp", inst.Backend)
	}
	if inst.ActiveModel != "" || inst.ActiveProfile != "" {
		t.Errorf(
			"ActiveModel/ActiveProfile = %q/%q, want both empty on a Starting instance",
			inst.ActiveModel,
			inst.ActiveProfile,
		)
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
	starting := []*RunningInstance{{Backend: "llamacpp", Host: "127.0.0.1", Port: 8080, Starting: true}}
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
	if instancesSignature(starting) == instancesSignature(idle) {
		t.Error("the Starting→healthy transition must change the signature (ADR-0010)")
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
