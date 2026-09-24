package launcher

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// splashTestServer answers /ready with the given status, adding the
// `Server: Splash` header when withHeader is set. Every other path is a 404.
func splashTestServer(t *testing.T, status int, withHeader bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if withHeader {
			w.Header().Set("Server", "Splash/0.1")
		}
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return addrFromURL(t, srv.URL)
}

func TestSplashRegistered(t *testing.T) {
	t.Parallel()

	srv, err := GetLLMServer("splash")
	if err != nil {
		t.Fatalf("GetLLMServer(splash) error: %v", err)
	}
	if _, ok := srv.(ManagedLLMServer); !ok {
		t.Error("splash does not implement ManagedLLMServer")
	}
	if _, ok := srv.(StartupProber); !ok {
		t.Error("splash does not implement StartupProber")
	}
	if _, ok := srv.(ModelLister); !ok {
		t.Error("splash does not implement ModelLister")
	}
	if _, ok := srv.(binaryInstallHinter); !ok {
		t.Error("splash does not implement binaryInstallHinter")
	}
	if srv.DisplayName() != "Splash" || srv.DefaultAddr() != "127.0.0.1:8000" {
		t.Errorf("DisplayName, DefaultAddr = %q, %q; want Splash, 127.0.0.1:8000", srv.DisplayName(), srv.DefaultAddr())
	}
}

func TestSplashHealthCheck(t *testing.T) {
	t.Parallel()

	llamaShaped := func(t *testing.T) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
		}))
		t.Cleanup(srv.Close)
		return addrFromURL(t, srv.URL)
	}

	tests := []struct {
		name    string
		addr    func(t *testing.T) string
		wantErr string
	}{
		{"ready with Splash header", func(t *testing.T) string { return splashTestServer(t, http.StatusOK, true) }, ""},
		{"ready without Splash header", func(t *testing.T) string { return splashTestServer(t, http.StatusOK, false) }, "not splash"},
		{"still loading", func(t *testing.T) string { return splashTestServer(t, http.StatusServiceUnavailable, true) }, "unhealthy"},
		{"llama-server shaped /health only", llamaShaped, "unhealthy"},
		{"unreachable", func(t *testing.T) string { return deadAddr(t) }, "connect"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := (&Splash{}).HealthCheck(tt.addr(t))
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
}

func TestSplashStartingUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr func(t *testing.T) string
		want bool
	}{
		{"503 with Splash header", func(t *testing.T) string { return splashTestServer(t, http.StatusServiceUnavailable, true) }, true},
		{"503 without Splash header", func(t *testing.T) string { return splashTestServer(t, http.StatusServiceUnavailable, false) }, false},
		{"ready", func(t *testing.T) string { return splashTestServer(t, http.StatusOK, true) }, false},
		{"dead address", func(t *testing.T) string { return deadAddr(t) }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (&Splash{}).StartingUp(tt.addr(t)); got != tt.want {
				t.Errorf("StartingUp = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplashBuildServerArgs(t *testing.T) {
	t.Parallel()

	intPtr := func(v int) *int { return &v }
	strPtr := func(v string) *string { return &v }

	tests := []struct {
		name    string
		profile ResolvedProfile
		want    []string
	}{
		{
			name:    "model only",
			profile: ResolvedProfile{ModelPath: "owner/repo"},
			want:    []string{"serve", "--model", "owner/repo"},
		},
		{
			name: "host, port and context",
			profile: ResolvedProfile{
				ModelPath:     "owner/repo",
				ProfileParams: ProfileParams{Host: strPtr("0.0.0.0"), Port: intPtr(8000), ContextSize: intPtr(32768)},
			},
			want: []string{"serve", "--model", "owner/repo", "--host", "0.0.0.0", "--port", "8000", "--max-context", "32768"},
		},
		{
			name: "extra_args come last",
			profile: ResolvedProfile{
				ModelPath:     "owner/repo",
				ExtraArgs:     []string{"--reasoning-effort", "high", "--allowed-host", "box.lan"},
				ProfileParams: ProfileParams{Port: intPtr(8000), ContextSize: intPtr(4096)},
			},
			want: []string{"serve", "--model", "owner/repo", "--port", "8000", "--max-context", "4096",
				"--reasoning-effort", "high", "--allowed-host", "box.lan"},
		},
		{
			name: "sampling params are ignored",
			profile: ResolvedProfile{
				ModelPath:     "owner/repo",
				ProfileParams: ProfileParams{TopK: intPtr(40), GPULayers: intPtr(99)},
			},
			want: []string{"serve", "--model", "owner/repo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := (&Splash{}).BuildServerArgs(&Config{}, &tt.profile)
			if !slices.Equal(got, tt.want) {
				t.Errorf("BuildServerArgs = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplashBuildServerEnv(t *testing.T) {
	t.Parallel()

	b := &Splash{}

	withKey := &Config{Servers: map[string]ServerConfig{"splash": {Enabled: true, APIKey: "secret"}}}
	if env := b.BuildServerEnv(withKey, &ResolvedProfile{}); !slices.Equal(env, []string{"SPLASH_API_KEY=secret"}) {
		t.Errorf("BuildServerEnv with key = %v, want [SPLASH_API_KEY=secret]", env)
	}

	noKey := &Config{Servers: map[string]ServerConfig{"splash": {Enabled: true}}}
	if env := b.BuildServerEnv(noKey, &ResolvedProfile{}); env != nil {
		t.Errorf("BuildServerEnv without key = %v, want nil", env)
	}
}

func TestSplashResolveModel(t *testing.T) {
	t.Parallel()

	valid := []string{
		"",
		"mlx-community/Qwen3-8B-4bit",
		"owner/repo",
		"a/b",
		"Owner_1/repo.name-v2",
		"_x/y_",
		"o/" + strings.Repeat("r", 96),
	}
	for _, ref := range valid {
		t.Run("valid "+ref, func(t *testing.T) {
			t.Parallel()
			got, err := (&Splash{}).ResolveModel(&Config{}, ref)
			if err != nil || got != ref {
				t.Errorf("ResolveModel(%q) = %q, %v; want %q, nil", ref, got, err, ref)
			}
		})
	}

	invalid := []string{
		"../..",
		"a/..",
		"a--b/c",
		"x/y.git",
		"repo",
		"a/b/c",
		"/repo",
		"owner/",
		".owner/repo",
		"owner./repo",
		"-owner/repo",
		"owner/repo-",
		"owner/.repo",
		"own er/repo",
		"o/" + strings.Repeat("r", 97),
	}
	for _, ref := range invalid {
		t.Run("invalid "+ref, func(t *testing.T) {
			t.Parallel()
			if got, err := (&Splash{}).ResolveModel(&Config{}, ref); err == nil {
				t.Errorf("ResolveModel(%q) = %q, nil; want an error", ref, got)
			}
		})
	}
}

func TestSplashParamSpecs(t *testing.T) {
	t.Parallel()

	var labels []string
	for _, spec := range (&Splash{}).ParamSpecs() {
		labels = append(labels, spec.Label)
	}
	if want := []string{"Context size"}; !slices.Equal(labels, want) {
		t.Errorf("ParamSpecs labels = %q, want %q", labels, want)
	}
}

func TestSplashServerBinary(t *testing.T) {
	t.Parallel()

	if got := (&Splash{}).ServerBinary(&Config{}); got != "splash" {
		t.Errorf("ServerBinary = %q, want splash", got)
	}
}
