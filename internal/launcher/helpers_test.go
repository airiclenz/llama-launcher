package launcher

import (
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
