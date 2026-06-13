package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// allowEntry matches a client either by exact IP or by CIDR membership.
type allowEntry struct {
	ip  net.IP     // set for a bare-IP entry
	net *net.IPNet // set for a CIDR entry
	raw string
}

func (e allowEntry) matches(ip net.IP) bool {
	if e.net != nil {
		return e.net.Contains(ip)
	}
	return e.ip != nil && e.ip.Equal(ip)
}

// lookupIP resolves a hostname to its addresses. It is a package var so tests
// can stub DNS.
var lookupIP = net.LookupIP

// interfaceAddrs returns the addresses bound to a named network interface. It is
// a package var so tests can stub the host's interface table.
var interfaceAddrs = func(name string) ([]net.Addr, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	return iface.Addrs()
}

// resolveAllowlist builds the matcher set from the --allow specs and the
// --allow-interface names. When neither is given the allowlist defaults to
// loopback only, so an accidentally-started adapter is never exposed; once
// either is given that implicit loopback is dropped — the operator is in
// control of the surface.
func resolveAllowlist(specs, ifaces []string) ([]allowEntry, error) {
	if len(specs) == 0 && len(ifaces) == 0 {
		specs = []string{"127.0.0.1", "::1"}
	}
	entries, err := parseAllow(specs)
	if err != nil {
		return nil, err
	}
	for _, name := range ifaces {
		e, err := parseAllowInterface(name)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e...)
	}
	return entries, nil
}

// parseAllowInterface resolves a network interface name (e.g. bridge100) to the
// CIDR of every address bound to it, each becoming a subnet matcher. This lets
// the operator allow "whatever is on my container-facing bridge" without
// knowing — or pinning — the container's IP: any address the bridge hands out
// is covered, and a private bridge subnet can never collide with a public
// hostname the way --allow <name> can.
func parseAllowInterface(name string) ([]allowEntry, error) {
	addrs, err := interfaceAddrs(name)
	if err != nil {
		return nil, fmt.Errorf("invalid --allow-interface %q: %w", name, err)
	}
	entries := make([]allowEntry, 0, len(addrs))
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		network := &net.IPNet{IP: ipnet.IP.Mask(ipnet.Mask), Mask: ipnet.Mask}
		entries = append(entries, allowEntry{net: network, raw: fmt.Sprintf("%s (%s)", name, network)})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("--allow-interface %q has no usable addresses", name)
	}
	return entries, nil
}

// parseAllow turns --allow specs into matchers. An entry may be an IP, a CIDR,
// or a hostname; a hostname is resolved to its addresses once, here at startup,
// and each address becomes an exact-IP matcher (the request-time check then
// stays a numeric comparison). If the container's IP later changes, restart the
// adapter — or allow its subnet as a CIDR (or its bridge via --allow-interface)
// to avoid re-resolution entirely.
func parseAllow(specs []string) ([]allowEntry, error) {
	entries := make([]allowEntry, 0, len(specs))
	for _, s := range specs {
		if strings.ContainsRune(s, '/') {
			_, ipnet, err := net.ParseCIDR(s)
			if err != nil {
				return nil, fmt.Errorf("invalid --allow CIDR %q: %w", s, err)
			}
			entries = append(entries, allowEntry{net: ipnet, raw: s})
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			entries = append(entries, allowEntry{ip: ip, raw: s})
			continue
		}
		// Not an IP or CIDR — treat it as a hostname and resolve it.
		ips, err := lookupIP(s)
		if err != nil {
			return nil, fmt.Errorf("invalid --allow %q: not an IP or CIDR, and hostname lookup failed: %w", s, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("--allow hostname %q resolved to no addresses", s)
		}
		for _, ip := range ips {
			entries = append(entries, allowEntry{ip: ip, raw: fmt.Sprintf("%s (%s)", s, ip)})
		}
	}
	return entries, nil
}

func describeAllow(entries []allowEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.raw
	}
	return strings.Join(parts, ", ")
}

// allowlistMiddleware rejects any request whose source IP is not in the
// allowlist with 403. This is defense-in-depth on top of binding the listener
// to the container-facing interface; it is not a substitute for that bind.
func allowlistMiddleware(allow []allowEntry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !anyMatch(allow, ip) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func anyMatch(allow []allowEntry, ip net.IP) bool {
	for _, e := range allow {
		if e.matches(ip) {
			return true
		}
	}
	return false
}
