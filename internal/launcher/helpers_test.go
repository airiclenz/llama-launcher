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
