package launcher

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func addrFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing URL %q: %v", rawURL, err)
	}
	return u.Host
}

// deadAddr returns a loopback host:port that nothing is listening on: the
// port is taken from a just-closed httptest listener.
func deadAddr(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := addrFromURL(t, srv.URL)
	srv.Close()
	return addr
}

// pinConfiguredTargets sets the configured-address snapshot a 401/403 stop
// is scoped to from cfg, as a LoadConfig of it would, and restores the
// previous snapshot when the test ends. The snapshot is process-global, so
// every caller is a non-parallel top-level test; an unconfigured leg passes
// &Config{} rather than assuming an earlier test left the set empty.
func pinConfiguredTargets(t *testing.T, cfg *Config) {
	t.Helper()

	configuredTargets.mu.RLock()
	previous := configuredTargets.backendsByAddr
	configuredTargets.mu.RUnlock()
	applyConfiguredTargets(cfg)
	t.Cleanup(func() {
		configuredTargets.mu.Lock()
		defer configuredTargets.mu.Unlock()
		configuredTargets.backendsByAddr = previous
	})
}
