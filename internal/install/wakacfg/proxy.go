// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacfg

import (
	"net"
	"net/netip"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ProxyVars are the variables of wakatime-cli's environment that its proxy
// for its main api_url comes from, as golang.org/x/net/http/httpproxy
// v0.61.0's FromEnvironment reads them for loadProxyURL: each pair with
// the lower-case name first, NO_PROXY's pair, and REQUEST_METHOD, under
// which an http api_url takes no proxy. A proxy's value can hold a
// password, so only these names are ever shown.
var ProxyVars = []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY", "no_proxy", "NO_PROXY", "REQUEST_METHOD"}

// ProxyFor returns the variable whose proxy wakatime-cli v2.28.4 would send
// every destination's requests through for server, its main api_url, and
// "" when none would. loadProxyURL works that proxy out once, for the main
// api_url, with httpproxy's ProxyFunc, and newClient's WithProxy then sends
// pairtime's line through it too. getenv reads the environment. Of a pair,
// the name is the first with a value, the lower-case one when both have
// one, as httpproxy reads them, and so on Windows, where a variable's name
// matches in any case, the lower-case one whichever spelling was set.
//
// It decides as httpproxy v0.61.0's proxyForURL does, as read from its
// source: the pair of the URL's scheme, the first of it with a value, and
// that only if it parses as a proxy; none for an http URL under CGI; none
// for the host "localhost", compared as written, so that LOCALHOST gets
// the proxy, or for a loopback address; and none when NO_PROXY matches the
// host in lower case, by "*", an IP, a CIDR range holding an IP, or a
// domain, which matches itself and its subdomains, or with a leading "."
// or "*." its subdomains alone, each with an optional port. httpproxy
// matches a host that isn't ASCII, and such a domain entry, in their IDNA
// form, which ProxyFor doesn't work out: it takes such a host to get the
// proxy, and such an entry to match nothing, so that install refuses when
// it is unclear rather than adding a line the proxy would carry.
//
// An editor's plugin can pass wakatime-cli a proxy of its own with
// --proxy, which comes before the config's and the environment's (as
// JetBrains IDEs' WakaTime plugin passes the IDE's), or another api_url
// with --api-url. Neither install nor doctor can see those.
func ProxyFor(server string, getenv func(string) string) string {
	u, err := url.Parse(server)
	if err != nil {
		return "" // wakatime-cli refuses an api_url it can't parse, and sends nothing
	}
	var pair []string
	switch u.Scheme {
	case "https":
		pair = []string{"https_proxy", "HTTPS_PROXY"}
	case "http":
		if getenv("REQUEST_METHOD") != "" {
			return "" // httpproxy refuses HTTP_PROXY under CGI, and wakatime-cli takes no proxy
		}
		pair = []string{"http_proxy", "HTTP_PROXY"}
	default:
		return ""
	}
	name := ""
	for _, n := range pair {
		if v := getenv(n); v != "" {
			if !parsesAsProxy(v) {
				return ""
			}
			name = n
			break
		}
	}
	if name == "" {
		return ""
	}
	// useProxy's checks, on the host as canonicalAddr gives it to them.
	host := u.Hostname()
	if !isASCII(host) {
		return name
	}
	if host == "localhost" {
		return ""
	}
	var ip net.IP
	if a, err := netip.ParseAddr(host); err == nil {
		if ip = net.IP(a.AsSlice()); ip.IsLoopback() {
			return ""
		}
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	no := getenv("no_proxy")
	if no == "" {
		no = getenv("NO_PROXY")
	}
	if noProxy(no, strings.ToLower(strings.TrimSpace(host)), port, ip) {
		return ""
	}
	return name
}

// parsesAsProxy reports whether v is a proxy httpproxy's parseProxy takes:
// a URL, or one once "http://" goes before it.
func parsesAsProxy(v string) bool {
	if _, err := url.Parse(v); err == nil {
		return true
	}
	_, err := url.Parse("http://" + v)
	return err == nil
}

// noProxy reports whether list, NO_PROXY's comma-separated entries, keeps
// host from the proxy on port, as httpproxy's init reads the entries and
// its useProxy matches them: "*" keeps every host; a CIDR range keeps the
// IP hosts in it; an IP, with or without a port, keeps that IP host; and a
// domain, with or without a port, keeps itself and its subdomains, or,
// with "." or "*." before it, its subdomains alone. host is in lower case,
// and ip is its address when it is an IP address, as useProxy parses it,
// and nil otherwise.
func noProxy(list, host, port string, ip net.IP) bool {
	for _, entry := range strings.Split(list, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "*" {
			return true
		}
		if entry == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		name, entryPort := entry, ""
		if h, p, err := net.SplitHostPort(entry); err == nil {
			if h == "" {
				continue // a port alone keeps no host
			}
			name, entryPort = h, p
		}
		if entryPort != "" && entryPort != port {
			continue
		}
		if eip := net.ParseIP(name); eip != nil {
			if ip != nil && eip.Equal(ip) {
				return true
			}
			continue
		}
		// A domain keeps no IP host, and one that isn't ASCII, which
		// httpproxy matches in its IDNA form, is taken to keep nothing.
		if ip != nil || !isASCII(name) {
			continue
		}
		suffix, itself := "."+name, true
		switch {
		case strings.HasPrefix(name, "*."):
			suffix, itself = name[1:], false
		case strings.HasPrefix(name, "."):
			suffix, itself = name, false
		}
		if strings.HasSuffix(host, suffix) || itself && host == name {
			return true
		}
	}
	return false
}

// isASCII reports whether s holds nothing outside ASCII.
func isASCII(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r >= utf8.RuneSelf }) < 0
}
