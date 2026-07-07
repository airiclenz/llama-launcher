package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveAllowlistDefaultsToLoopback(t *testing.T) {
	allow, err := resolveAllowlist(nil, nil)
	if err != nil {
		t.Fatalf("resolveAllowlist(nil, nil): %v", err)
	}
	if !anyMatch(allow, net.ParseIP("127.0.0.1")) {
		t.Error("loopback v4 should be allowed by default")
	}
	if !anyMatch(allow, net.ParseIP("::1")) {
		t.Error("loopback v6 should be allowed by default")
	}
	if anyMatch(allow, net.ParseIP("192.168.64.2")) {
		t.Error("non-loopback should not be allowed by default")
	}
}

// Once any --allow-interface is given, the implicit loopback default is dropped:
// the operator is choosing the surface.
func TestResolveAllowlistInterfaceDropsLoopbackDefault(t *testing.T) {
	stubInterfaceAddrs(t, map[string][]net.Addr{
		"bridge100": {mustIPNet(t, "192.168.64.1/24")},
	})

	allow, err := resolveAllowlist(nil, []string{"bridge100"})
	if err != nil {
		t.Fatalf("resolveAllowlist(iface): %v", err)
	}
	if !anyMatch(allow, net.ParseIP("192.168.64.2")) {
		t.Error("an address on the bridge subnet should be allowed")
	}
	if !anyMatch(allow, net.ParseIP("192.168.64.250")) {
		t.Error("any address on the bridge subnet should be allowed, whatever the container IP")
	}
	if anyMatch(allow, net.ParseIP("127.0.0.1")) {
		t.Error("loopback should not be implied once an interface is given")
	}
	if anyMatch(allow, net.ParseIP("10.0.0.1")) {
		t.Error("an address off the bridge subnet should not be allowed")
	}
}

// --allow and --allow-interface combine; an unknown interface is a startup error.
func TestResolveAllowlistCombinesAndRejectsBadInterface(t *testing.T) {
	stubInterfaceAddrs(t, map[string][]net.Addr{
		"bridge100": {mustIPNet(t, "192.168.64.1/24")},
	})

	allow, err := resolveAllowlist([]string{"10.0.0.5"}, []string{"bridge100"})
	if err != nil {
		t.Fatalf("resolveAllowlist(combine): %v", err)
	}
	if !anyMatch(allow, net.ParseIP("10.0.0.5")) {
		t.Error("explicit --allow IP should still match")
	}
	if !anyMatch(allow, net.ParseIP("192.168.64.7")) {
		t.Error("interface subnet should also match")
	}

	if _, err := resolveAllowlist(nil, []string{"nope0"}); err == nil {
		t.Error("expected error for an interface that does not exist")
	}
}

// mustIPNet parses a CIDR into the *net.IPNet form an interface reports, keeping
// the host bits (interface addresses carry the host IP plus the mask).
func mustIPNet(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", cidr, err)
	}
	return &net.IPNet{IP: ip, Mask: n.Mask}
}

// stubInterfaceAddrs swaps the package interface resolver for the duration of a
// test. A name absent from the table resolves to an error.
func stubInterfaceAddrs(t *testing.T, table map[string][]net.Addr) {
	t.Helper()
	orig := interfaceAddrs
	interfaceAddrs = func(name string) ([]net.Addr, error) {
		if addrs, ok := table[name]; ok {
			return addrs, nil
		}
		return nil, fmt.Errorf("no such network interface")
	}
	t.Cleanup(func() { interfaceAddrs = orig })
}

func TestParseAllowExactAndCIDR(t *testing.T) {
	allow, err := parseAllow([]string{"192.168.64.2", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("parseAllow: %v", err)
	}
	cases := []struct {
		ip   string
		want bool
	}{
		{"192.168.64.2", true},
		{"192.168.64.3", false},
		{"10.5.6.7", true},
		{"11.0.0.1", false},
		{"127.0.0.1", false}, // loopback no longer implied once specs are given
	}
	for _, c := range cases {
		if got := anyMatch(allow, net.ParseIP(c.ip)); got != c.want {
			t.Errorf("anyMatch(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestParseAllowResolvesHostname(t *testing.T) {
	stubLookupIP(t, map[string][]net.IP{
		"devbox.dev": {net.ParseIP("192.168.64.2")},
	})

	allow, err := parseAllow([]string{"devbox.dev"})
	if err != nil {
		t.Fatalf("parseAllow(hostname): %v", err)
	}
	if !anyMatch(allow, net.ParseIP("192.168.64.2")) {
		t.Error("resolved hostname IP should be allowed")
	}
	if anyMatch(allow, net.ParseIP("192.168.64.3")) {
		t.Error("a different IP should not be allowed")
	}
}

func TestParseAllowRejectsGarbage(t *testing.T) {
	// A name that is neither IP nor CIDR is treated as a hostname; an
	// unresolvable one is an error rather than a silent allow-all.
	stubLookupIP(t, nil) // every lookup fails
	if _, err := parseAllow([]string{"not-a-real-host.invalid"}); err == nil {
		t.Error("expected error for unresolvable hostname")
	}
	if _, err := parseAllow([]string{"10.0.0.0/99"}); err == nil {
		t.Error("expected error for invalid CIDR")
	}
}

// stubLookupIP swaps the package DNS resolver for the duration of a test.
// A name absent from table resolves to an error.
func stubLookupIP(t *testing.T, table map[string][]net.IP) {
	t.Helper()
	orig := lookupIP
	lookupIP = func(host string) ([]net.IP, error) {
		if ips, ok := table[host]; ok {
			return ips, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	t.Cleanup(func() { lookupIP = orig })
}

func TestAllowlistMiddleware(t *testing.T) {
	allow, _ := parseAllow([]string{"192.168.64.2"})
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := allowlistMiddleware(allow, ok)

	cases := []struct {
		remote string
		want   int
	}{
		{"192.168.64.2:51000", http.StatusOK},
		{"192.168.64.3:51000", http.StatusForbidden},
		{"garbage", http.StatusForbidden},
		// Bracketed IPv6 RemoteAddr must be parsed (SplitHostPort strips the
		// brackets); loopback v6 is not in this allowlist, so it is denied.
		{"[::1]:51000", http.StatusForbidden},
		// An IPv4-mapped IPv6 form of the allowed v4 address still matches
		// (net.IP.Equal treats ::ffff:a.b.c.d and a.b.c.d as equal), so a
		// client presenting the mapped form cannot be smuggled past the entry.
		{"[::ffff:192.168.64.2]:51000", http.StatusOK},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.RemoteAddr = c.remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("remote %q: status = %d, want %d", c.remote, rec.Code, c.want)
		}
	}

	// The middleware trusts only the transport-level RemoteAddr. A spoofable
	// X-Forwarded-For naming an allowed IP must not grant a denied peer access.
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.RemoteAddr = "192.168.64.3:51000" // not allowed
	req.Header.Set("X-Forwarded-For", "192.168.64.2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("X-Forwarded-For spoof: status = %d, want %d (header must be ignored)", rec.Code, http.StatusForbidden)
	}
}
