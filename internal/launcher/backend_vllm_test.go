package launcher

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// vllmTestServer starts an httptest server driven by handler and returns its
// host:port.
func vllmTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return addrFromURL(t, srv.URL)
}

// vllmShaped answers like vLLM: /health 200 with an empty body, /version with
// {"version":...}, /v1/models with an OpenAI model list; every other path is a
// 404.
func vllmShaped(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/health":
		w.WriteHeader(http.StatusOK)
	case "/version":
		w.Write([]byte(`{"version":"0.11.0"}`))
	case "/v1/models":
		w.Write([]byte(`{"object":"list","data":[{"id":"owner/repo"}]}`))
	default:
		http.NotFound(w, r)
	}
}

func TestVLLMRegistered(t *testing.T) {
	t.Parallel()

	srv, err := GetLLMServer("vllm")
	if err != nil {
		t.Fatalf("GetLLMServer(vllm) error: %v", err)
	}
	if _, ok := srv.(ManagedLLMServer); !ok {
		t.Error("vllm does not implement ManagedLLMServer")
	}
	if _, ok := srv.(ModelLister); !ok {
		t.Error("vllm does not implement ModelLister")
	}
	if _, ok := srv.(binaryInstallHinter); !ok {
		t.Error("vllm does not implement binaryInstallHinter")
	}
	if _, ok := srv.(apiKeyConfigurable); !ok {
		t.Error("vllm does not implement apiKeyConfigurable")
	}
	if srv.DisplayName() != "vLLM" || srv.DefaultAddr() != "127.0.0.1:8000" {
		t.Errorf("DisplayName, DefaultAddr = %q, %q; want vLLM, 127.0.0.1:8000", srv.DisplayName(), srv.DefaultAddr())
	}
}

func TestVLLMHealthCheck(t *testing.T) {
	t.Parallel()

	llamaShaped := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	}
	lmStudioShaped := func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"Unexpected endpoint or method."}`))
	}
	ollamaShaped := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte("Ollama is running"))
		case "/api/version":
			w.Write([]byte(`{"version":"0.12.0"}`))
		default:
			http.NotFound(w, r)
		}
	}
	splashHeader := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Splash/0.1")
		vllmShaped(w, r)
	}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{"vLLM shaped", vllmShaped, ""},
		{"llama-server shaped /health JSON", llamaShaped, "not vllm"},
		{"LM Studio shaped /version without version", lmStudioShaped, "missing version"},
		{"Ollama shaped", ollamaShaped, "unhealthy"},
		{"Splash header", splashHeader, "Server: Splash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addr := vllmTestServer(t, tt.handler)

			err := (&VLLM{}).HealthCheck(addr)

			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("HealthCheck = %v, want healthy", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("HealthCheck = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}

	t.Run("dead address", func(t *testing.T) {
		t.Parallel()

		err := (&VLLM{}).HealthCheck(deadAddr(t))

		if err == nil || !strings.Contains(err.Error(), "connect") {
			t.Errorf("HealthCheck = %v, want a connection error", err)
		}
	})

	t.Run("401 on /v1 only wraps ErrAuthFailed", func(t *testing.T) {
		t.Parallel()
		addr := vllmTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v1/") && r.Header.Get("Authorization") != "Bearer right" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			vllmShaped(w, r)
		})
		b := &VLLM{}
		b.setAPIKey("wrong")

		err := b.HealthCheck(addr)

		if !errors.Is(err, ErrAuthFailed) {
			t.Errorf("HealthCheck = %v, want an error wrapping ErrAuthFailed", err)
		}
	})
}

func TestVLLMBuildServerArgs(t *testing.T) {
	t.Parallel()
	intPtr := func(v int) *int { return &v }
	strPtr := func(v string) *string { return &v }

	tests := []struct {
		name    string
		profile ResolvedProfile
		want    []string
	}{
		{
			name: "model, host and port",
			profile: ResolvedProfile{
				ModelPath:     "owner/repo",
				ProfileParams: ProfileParams{Host: strPtr("127.0.0.1"), Port: intPtr(8000)},
			},
			want: []string{"serve", "owner/repo", "--host", "127.0.0.1", "--port", "8000"},
		},
		{
			name: "context size and parallel",
			profile: ResolvedProfile{
				ModelPath: "/models/qwen",
				ProfileParams: ProfileParams{
					Host: strPtr("0.0.0.0"), Port: intPtr(8001), ContextSize: intPtr(32768), Parallel: intPtr(4),
				},
			},
			want: []string{
				"serve", "/models/qwen", "--host", "0.0.0.0", "--port", "8001",
				"--max-model-len", "32768", "--max-num-seqs", "4",
			},
		},
		{
			name: "extra args come last",
			profile: ResolvedProfile{
				ModelPath:     "owner/repo",
				ExtraArgs:     []string{"--gpu-memory-utilization", "0.8", "--max-model-len", "8192"},
				ProfileParams: ProfileParams{Host: strPtr("127.0.0.1"), Port: intPtr(8000), ContextSize: intPtr(4096)},
			},
			want: []string{
				"serve", "owner/repo", "--host", "127.0.0.1", "--port", "8000", "--max-model-len", "4096",
				"--gpu-memory-utilization", "0.8", "--max-model-len", "8192",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := (&VLLM{}).BuildServerArgs(&Config{}, &tt.profile)

			if !slices.Equal(got, tt.want) {
				t.Errorf("BuildServerArgs = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVLLMBuildServerEnv(t *testing.T) {
	t.Parallel()
	b := &VLLM{}
	port := 8000
	profile := &ResolvedProfile{ModelPath: "owner/repo", ProfileParams: ProfileParams{Port: &port}}

	withKey := &Config{Servers: map[string]ServerConfig{"vllm": {Enabled: true, APIKey: "secret"}}}
	if env := b.BuildServerEnv(withKey, profile); !slices.Equal(env, []string{"VLLM_API_KEY=secret"}) {
		t.Errorf("BuildServerEnv with key = %v, want [VLLM_API_KEY=secret]", env)
	}
	for _, arg := range b.BuildServerArgs(withKey, profile) {
		if strings.Contains(arg, "secret") {
			t.Errorf("BuildServerArgs carries the api_key in %q", arg)
		}
	}

	noKey := &Config{Servers: map[string]ServerConfig{"vllm": {Enabled: true}}}
	if env := b.BuildServerEnv(noKey, profile); env != nil {
		t.Errorf("BuildServerEnv without key = %v, want nil", env)
	}
}

func TestVLLMResolveModel(t *testing.T) {
	t.Parallel()
	localModel := t.TempDir()
	missing := filepath.Join(t.TempDir(), "absent")

	valid := []string{"", "Qwen/Qwen3-8B", "owner/repo.name-v2", localModel}
	for _, ref := range valid {
		t.Run("valid "+ref, func(t *testing.T) {
			t.Parallel()

			got, err := (&VLLM{}).ResolveModel(&Config{}, ref)

			if err != nil || got != ref {
				t.Errorf("ResolveModel(%q) = %q, %v; want %q, nil", ref, got, err, ref)
			}
		})
	}

	t.Run("missing absolute path", func(t *testing.T) {
		t.Parallel()

		_, err := (&VLLM{}).ResolveModel(&Config{}, missing)

		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("ResolveModel(%q) = %v, want an error wrapping os.ErrNotExist", missing, err)
		}
	})

	invalid := []string{"./models/qwen", "../models/qwen", "models/qwen/7b", "repo", "a--b/c"}
	for _, ref := range invalid {
		t.Run("invalid "+ref, func(t *testing.T) {
			t.Parallel()

			_, err := (&VLLM{}).ResolveModel(&Config{}, ref)

			if err == nil || !strings.Contains(err.Error(), "invalid vllm model") {
				t.Errorf("ResolveModel(%q) = %v, want an invalid vllm model error", ref, err)
			}
		})
	}
}

func TestVLLMParamSpecs(t *testing.T) {
	t.Parallel()

	var labels []string
	for _, spec := range (&VLLM{}).ParamSpecs() {
		labels = append(labels, spec.Label)
	}

	if want := []string{"Context size", "Parallel"}; !slices.Equal(labels, want) {
		t.Errorf("ParamSpecs labels = %q, want %q", labels, want)
	}
}
