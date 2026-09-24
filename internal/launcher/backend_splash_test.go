package launcher

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// splashWildcardModel is the model the Host-checking Splash fake serves.
const splashWildcardModel = "mlx-community/wildcard-test"

// splashHostCheckingServer mimics Splash's Host validation: it answers 403
// unless the request's Host names a loopback (127.0.0.1, localhost or ::1),
// so a probe sending `Host: 0.0.0.0:<port>` is rejected as Splash rejects it.
// Otherwise it serves /ready with readyStatus and /v1/models with
// splashWildcardModel, sending `Server: Splash` on every reply. It returns
// the listening port.
func splashHostCheckingServer(t *testing.T, readyStatus int) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Splash/0.1")
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/ready":
			w.WriteHeader(readyStatus)
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": splashWildcardModel}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	_, portStr, err := net.SplitHostPort(addrFromURL(t, srv.URL))
	if err != nil {
		t.Fatalf("splitting test server address: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parsing test server port: %v", err)
	}
	return port
}

// TestSplashWildcardProbe covers a Splash configured on a wildcard host: its
// probes must dial loopback, since Splash 403s a Host naming the wildcard.
func TestSplashWildcardProbe(t *testing.T) {
	t.Parallel()

	t.Run("fake rejects a wildcard Host", func(t *testing.T) {
		t.Parallel()
		// Guards the tests below against a vacuous fake: a request that
		// names the wildcard in its Host must be refused.
		port := splashHostCheckingServer(t, http.StatusOK)
		req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/ready", nil)
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		req.Host = "0.0.0.0:" + strconv.Itoa(port)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want 403", resp.StatusCode)
		}
	})

	for _, host := range []string{"0.0.0.0", ""} {
		t.Run("host "+strconv.Quote(host), func(t *testing.T) {
			t.Parallel()

			ready := splashHostCheckingServer(t, http.StatusOK)
			readyAddr := host + ":" + strconv.Itoa(ready)
			if err := (&Splash{}).HealthCheck(readyAddr); err != nil {
				t.Errorf("HealthCheck(%q) = %v, want healthy", readyAddr, err)
			}
			models, err := (&Splash{}).ListRunningModels(readyAddr)
			if err != nil {
				t.Fatalf("ListRunningModels(%q) error: %v", readyAddr, err)
			}
			if len(models) != 1 || models[0].Name != splashWildcardModel {
				t.Errorf("ListRunningModels(%q) = %+v, want [%s]", readyAddr, models, splashWildcardModel)
			}

			loading := splashHostCheckingServer(t, http.StatusServiceUnavailable)
			loadingAddr := host + ":" + strconv.Itoa(loading)
			if !(&Splash{}).StartingUp(loadingAddr) {
				t.Errorf("StartingUp(%q) = false, want true", loadingAddr)
			}
		})
	}
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

// splashTestRev is a 40-hex snapshot revision for installed-model fixtures.
const splashTestRev = "0123456789abcdef0123456789abcdef01234567"

// setSplashHubEnv sets all four variables the Hugging Face hub lookup reads;
// "" leaves a variable effectively unset.
func setSplashHubEnv(t *testing.T, hfHubCache, hfHome, xdgCacheHome, home string) {
	t.Helper()
	t.Setenv("HF_HUB_CACHE", hfHubCache)
	t.Setenv("HF_HOME", hfHome)
	t.Setenv("XDG_CACHE_HOME", xdgCacheHome)
	t.Setenv("HOME", home)
}

// writeSplashSnapshot creates snapshots/<rev>/ for repoID in hub, with a
// manifest.json when withManifest is set, and pins it with
// refs/splash/<installation>/<rev> when withRef is set.
func writeSplashSnapshot(t *testing.T, hub, repoID string, withManifest, withRef bool) {
	t.Helper()
	repoDir := filepath.Join(hub, "models--"+strings.ReplaceAll(repoID, "/", "--"))
	snapshot := filepath.Join(repoDir, "snapshots", splashTestRev)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if withManifest {
		if err := os.WriteFile(filepath.Join(snapshot, "manifest.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if withRef {
		refDir := filepath.Join(repoDir, "refs", "splash", "installation")
		if err := os.MkdirAll(refDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(refDir, splashTestRev), []byte(splashTestRev), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// installSplashModel writes a fully installed repoID into hub.
func installSplashModel(t *testing.T, hub, repoID string) {
	t.Helper()
	writeSplashSnapshot(t, hub, repoID, true, true)
}

func TestSplashResolveModelInstallCheck(t *testing.T) {
	const ref = "mlx-community/Qwen3-8B-4bit"

	resolves := func(t *testing.T) {
		t.Helper()
		got, err := (&Splash{}).ResolveModel(&Config{}, ref)
		if err != nil || got != ref {
			t.Fatalf("ResolveModel(%q) = %q, %v; want %q, nil", ref, got, err, ref)
		}
	}
	refusesWithInstallHint := func(t *testing.T) {
		t.Helper()
		_, err := (&Splash{}).ResolveModel(&Config{}, ref)
		if err == nil {
			t.Fatalf("ResolveModel(%q) succeeded; want a not-installed error", ref)
		}
		if want := "splash serve --model " + ref; !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}

	t.Run("HF_HUB_CACHE hit", func(t *testing.T) {
		hub := t.TempDir()
		installSplashModel(t, hub, ref)
		setSplashHubEnv(t, hub, t.TempDir(), t.TempDir(), t.TempDir())
		resolves(t)
	})

	t.Run("HF_HOME fallback", func(t *testing.T) {
		hfHome := t.TempDir()
		installSplashModel(t, filepath.Join(hfHome, "hub"), ref)
		setSplashHubEnv(t, "", hfHome, t.TempDir(), t.TempDir())
		resolves(t)
	})

	t.Run("XDG_CACHE_HOME fallback", func(t *testing.T) {
		xdg := t.TempDir()
		installSplashModel(t, filepath.Join(xdg, "huggingface", "hub"), ref)
		setSplashHubEnv(t, "", "", xdg, t.TempDir())
		resolves(t)
	})

	t.Run("home default", func(t *testing.T) {
		home := t.TempDir()
		installSplashModel(t, filepath.Join(home, ".cache", "huggingface", "hub"), ref)
		setSplashHubEnv(t, "", "", "", home)
		resolves(t)
	})

	t.Run("empty HF_HUB_CACHE falls back to HF_HOME", func(t *testing.T) {
		hfHome := t.TempDir()
		installSplashModel(t, filepath.Join(hfHome, "hub"), ref)
		setSplashHubEnv(t, "", hfHome, "", "")
		resolves(t)
	})

	t.Run("model absent from the hub", func(t *testing.T) {
		setSplashHubEnv(t, t.TempDir(), "", "", "")
		refusesWithInstallHint(t)
	})

	t.Run("snapshot without manifest.json", func(t *testing.T) {
		hub := t.TempDir()
		writeSplashSnapshot(t, hub, ref, false, true)
		setSplashHubEnv(t, hub, "", "", "")
		refusesWithInstallHint(t)
	})

	t.Run("manifest.json without a refs/splash pin", func(t *testing.T) {
		hub := t.TempDir()
		writeSplashSnapshot(t, hub, ref, true, false)
		setSplashHubEnv(t, hub, "", "", "")
		refusesWithInstallHint(t)
	})

	t.Run("empty ref resolves without a lookup", func(t *testing.T) {
		// No variable is set and HOME is empty, so any hub lookup would fail.
		setSplashHubEnv(t, "", "", "", "")
		got, err := (&Splash{}).ResolveModel(&Config{}, "")
		if err != nil || got != "" {
			t.Fatalf("ResolveModel(\"\") = %q, %v; want \"\", nil", got, err)
		}
	})
}

func TestSplashResolveModel(t *testing.T) {
	valid := []string{
		"",
		"mlx-community/Qwen3-8B-4bit",
		"owner/repo",
		"a/b",
		"Owner_1/repo.name-v2",
		"_x/y_",
		"o/" + strings.Repeat("r", 96),
	}
	hub := t.TempDir()
	for _, ref := range valid {
		if ref != "" {
			installSplashModel(t, hub, ref)
		}
	}
	setSplashHubEnv(t, hub, "", "", "")
	for _, ref := range valid {
		t.Run("valid "+ref, func(t *testing.T) {
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

// splashLoadingArgs is the command line of each form a launcher-started
// Splash passes through while it loads, captured from a real Splash
// (`splash serve` through the ~/.local/bin wrapper, a source checkout):
// every form exec's the next in the same process, which binds the port
// only once the model has loaded.
var splashLoadingArgs = map[string][]string{
	"wrapper script": strings.Fields("/bin/sh /Users/u/.local/bin/splash serve --model incoai/Qwen3.8-27B-Splash " +
		"--host 127.0.0.1 --port 18731"),
	"checkout script": strings.Fields("/bin/sh /Users/u/Repos/splash/splash serve --model incoai/Qwen3.8-27B-Splash " +
		"--host 127.0.0.1 --port 18731"),
	"launcher.py": strings.Fields("/opt/homebrew/bin/python3.14 -u /Users/u/Repos/splash/install/launcher.py serve " +
		"--model incoai/Qwen3.8-27B-Splash --host 127.0.0.1 --port 18731"),
	"server.py": strings.Fields("/opt/homebrew/Cellar/python@3.14/3.14.7/Frameworks/Python.framework/Versions/3.14/" +
		"Resources/Python.app/Contents/MacOS/Python -u /Users/u/Repos/splash/server/server.py " +
		"/Users/u/Repos/splash/install/models/incoai/Qwen3.8-27B-Splash/target " +
		"/Users/u/Repos/splash/install/models/incoai/Qwen3.8-27B-Splash/draft " +
		"--tokenizer /Users/u/Repos/splash/install/models/incoai/Qwen3.8-27B-Splash/tokenizer " +
		"--model incoai/Qwen3.8-27B-Splash --binary /Users/u/Repos/splash/build/splash " +
		"--host 127.0.0.1 --port 18731 --max-memory auto --max-context auto"),
}

func TestSplashLoadingPID(t *testing.T) {
	t.Parallel()

	const addr = "127.0.0.1:18731"
	leader := func(args string) []processEntry {
		return []processEntry{{PID: 4242, PGID: 4242, Args: strings.Fields(args)}}
	}

	for name, args := range splashLoadingArgs {
		t.Run("matches the "+name+" form", func(t *testing.T) {
			t.Parallel()
			procs := []processEntry{{PID: 4242, PGID: 4242, Args: args}}
			if got := splashLoadingPID(procs, addr, "127.0.0.1:8000"); got != 4242 {
				t.Errorf("splashLoadingPID = %d, want 4242", got)
			}
		})
	}

	tests := []struct {
		name  string
		procs []processEntry
		addr  string
		want  int
	}{
		{
			name:  "a process that is not its session's leader is not the launcher's fork",
			procs: []processEntry{{PID: 4243, PGID: 4242, Args: splashLoadingArgs["server.py"]}},
			addr:  addr,
		},
		{
			name: "the serve-native child of a loading Splash is not matched",
			procs: []processEntry{{PID: 4243, PGID: 4243, Args: strings.Fields(
				"/Users/u/Repos/splash/build/splash serve-native /m/target /m/draft auto auto")}},
			addr: addr,
		},
		{
			name:  "another port",
			procs: []processEntry{{PID: 4242, PGID: 4242, Args: splashLoadingArgs["wrapper script"]}},
			addr:  "127.0.0.1:18732",
		},
		{
			name:  "another host",
			procs: []processEntry{{PID: 4242, PGID: 4242, Args: splashLoadingArgs["wrapper script"]}},
			addr:  "0.0.0.0:18731",
		},
		{
			name:  "the last --port wins, as an extra_args override does",
			procs: leader("splash serve --model o/r --host 127.0.0.1 --port 8000 --port 18731"),
			addr:  addr,
			want:  4242,
		},
		{
			name:  "the --flag=value form",
			procs: leader("splash serve --model=o/r --host=127.0.0.1 --port=18731"),
			addr:  addr,
			want:  4242,
		},
		{
			name:  "absent --host and --port fall back to Splash's default address",
			procs: leader("splash serve --model o/r"),
			addr:  "127.0.0.1:8000",
			want:  4242,
		},
		{
			name:  "another server's serve subcommand is foreign",
			procs: leader("/usr/local/bin/vllm serve --model o/r --host 127.0.0.1 --port 18731"),
			addr:  addr,
		},
		{
			name:  "a splash serve without --model is not a server launch",
			procs: leader("splash serve --host 127.0.0.1 --port 18731"),
			addr:  addr,
		},
		{
			name: "the matching leader is picked among others",
			procs: []processEntry{
				{PID: 1, PGID: 1, Args: []string{"/sbin/launchd"}},
				{PID: 4242, PGID: 4242, Args: splashLoadingArgs["server.py"]},
			},
			addr: addr,
			want: 4242,
		},
		{
			name:  "an unparseable address",
			procs: leader("splash serve --model o/r --port 18731"),
			addr:  "no-port",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := splashLoadingPID(tt.procs, tt.addr, "127.0.0.1:8000"); got != tt.want {
				t.Errorf("splashLoadingPID = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseProcessTable(t *testing.T) {
	t.Parallel()

	out := "    1     1 /sbin/launchd\n" +
		"85311 85311 /usr/bin/python3 -u /s/server/server.py --port 18731\n" +
		"garbage line here\n" +
		"  42\n" +
		"\n"
	got := parseProcessTable(out)
	want := []processEntry{
		{PID: 1, PGID: 1, Args: []string{"/sbin/launchd"}},
		{PID: 85311, PGID: 85311, Args: []string{"/usr/bin/python3", "-u", "/s/server/server.py", "--port", "18731"}},
	}
	if len(got) != len(want) {
		t.Fatalf("parseProcessTable = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].PID != want[i].PID || got[i].PGID != want[i].PGID || !slices.Equal(got[i].Args, want[i].Args) {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
