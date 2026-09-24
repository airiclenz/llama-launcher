//go:build integration

// Real-llama-server API-key probe (Layer 2; see integration_test.go for the
// suite conventions). It settles two questions against the llama-server on
// PATH instead of trusting a reading of llama.cpp's common/arg.cpp:
//
//	(a) does the server's own log file echo the key — the LLAMA_API_KEY value
//	    or an --api-key argv entry?
//	(b) when extra_args supplies --api-key alongside the launcher's
//	    LLAMA_API_KEY, are both keys valid (append) or only the flag's
//	    (replace)?
//
// The server is started through StartServer, so the launcher's own
// BuildServerArgs/BuildServerEnv composition is what reaches llama-server.
// The skip and build-detection helpers live untagged in
// server_integration_unit_test.go so the unit pass covers them.

package launcher

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// apiKeyProbeTimeout bounds one authenticated request to the probed server.
const apiKeyProbeTimeout = 5 * time.Second

// startLlamaServerWithKey spawns a real llama-server through the managed
// start path with envKey as the configured llamacpp api_key and extraArgs as
// the profile's extra_args, waits until it is healthy, and registers
// teardown on t.
func startLlamaServerWithKey(t *testing.T, model, envKey string, extraArgs []string) *RunningInstance {
	t.Helper()

	host := loopbackHost
	port := freePort(t)
	cfg := &Config{
		LogDir:  t.TempDir(),
		Servers: map[string]ServerConfig{"llamacpp": {Enabled: true, APIKey: envKey}},
	}
	profile := &ResolvedProfile{
		Name:          "integration-llamacpp-apikey",
		Backend:       "llamacpp",
		ModelPath:     model,
		ExtraArgs:     extraArgs,
		ProfileParams: ProfileParams{Host: &host, Port: &port},
	}

	inst, err := StartServer(cfg, profile)
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	killServerOnCleanup(t, inst)
	waitForHealthy(t, llamaCppBackend(t), inst.Addr(), llamaCppHealthyTimeout)
	return inst
}

// authStatus returns the HTTP status llama-server answers for an
// authenticated route when presented with key as the Bearer token. /health
// is exempt from auth, so /v1/models is the probe.
func authStatus(t *testing.T, addr, key string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/models", nil)
	if err != nil {
		t.Fatalf("building probe request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: apiKeyProbeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("probing %s with a key: %v", addr, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// logEchoes reports which of keys appear verbatim in the server log at path.
func logEchoes(t *testing.T, path string, keys ...string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading server log %s: %v", path, err)
	}
	var echoed []string
	for _, key := range keys {
		if strings.Contains(string(data), key) {
			echoed = append(echoed, key)
		}
	}
	return echoed
}

// TestLlamaServerAPIKey probes the llama-server on PATH for the two API-key
// behaviours the launcher's docs describe. The answers are reported with
// t.Logf (run with -v to see them); the assertions are the invariants the
// launcher relies on whatever the answers are: the configured key
// authenticates, an extra_args key authenticates, a wrong key is rejected.
func TestLlamaServerAPIKey(t *testing.T) {
	model := requireLlamaServer(t)
	build := detectLlamaServerBuild(t)
	t.Logf("llama-server build under test: %s", build)

	stamp := time.Now().UnixNano()
	envKey := fmt.Sprintf("llsentinel-env-%d", stamp)
	flagKey := fmt.Sprintf("llsentinel-flag-%d", stamp)
	wrongKey := fmt.Sprintf("llsentinel-wrong-%d", stamp)

	t.Run("configured key only", func(t *testing.T) {
		inst := startLlamaServerWithKey(t, model, envKey, nil)
		addr := inst.Addr()

		if got := authStatus(t, addr, envKey); got != http.StatusOK {
			t.Errorf("configured key: status %d, want %d", got, http.StatusOK)
		}
		if got := authStatus(t, addr, wrongKey); got != http.StatusUnauthorized {
			t.Errorf("wrong key: status %d, want %d", got, http.StatusUnauthorized)
		}

		echoed := logEchoes(t, inst.LogFile, envKey)
		t.Logf("ANSWER (a) [%s] LLAMA_API_KEY echoed into the server log: %v", build, len(echoed) > 0)
	})

	t.Run("extra_args override", func(t *testing.T) {
		inst := startLlamaServerWithKey(t, model, envKey, []string{"--api-key", flagKey})
		addr := inst.Addr()

		if got := authStatus(t, addr, flagKey); got != http.StatusOK {
			t.Errorf("extra_args key: status %d, want %d", got, http.StatusOK)
		}
		if got := authStatus(t, addr, wrongKey); got != http.StatusUnauthorized {
			t.Errorf("wrong key: status %d, want %d", got, http.StatusUnauthorized)
		}

		switch got := authStatus(t, addr, envKey); got {
		case http.StatusOK:
			t.Logf("ANSWER (b) [%s] append: the configured LLAMA_API_KEY stays valid alongside the extra_args --api-key", build)
		case http.StatusUnauthorized:
			t.Logf("ANSWER (b) [%s] replace: only the extra_args --api-key is valid", build)
		default:
			t.Errorf("configured key beside an override: status %d, want %d (append) or %d (replace)",
				got, http.StatusOK, http.StatusUnauthorized)
		}

		echoed := logEchoes(t, inst.LogFile, envKey, flagKey)
		t.Logf("ANSWER (a) [%s] keys echoed into the server log with an --api-key argv entry: %v", build, echoed)
	})
}
