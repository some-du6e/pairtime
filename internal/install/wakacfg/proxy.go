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
// pairtime's line through it too. getenv reads the environment.
//
// It decides as httpproxy v0.61.0's proxyForURL does, as read from its
// source: the pair of the URL's scheme, the first of it with a value, and
// that only if it parses as a proxy; none for an http URL under CGI; none
// for localhost or a loopback address; and none when NO_PROXY matches the
// host, by "*", an IP, a CIDR range holding an IP, or a domain, which with
// or without a leading "." or "*." matches it and its subdomains, each with
// an optional port. A host or a domain entry that isn't ASCII, which
// httpproxy converts with IDNA first, counts as matching nothing, so that a
// proxy is taken to apply when it is unclear, and install refuses rather
// than adding a line the proxy would carry.
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
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "localhost" {
		return ""
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.IsLoopback() {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	no := getenv("no_proxy")
	if no == "" {
		no = getenv("NO_PROXY")
	}
	if isASCII(host) && noProxy(no, host, port) {
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
// host, in lower case, on port from the proxy, as httpproxy's init and
// useProxy match them.
func noProxy(list, host, port string) bool {
	addr, err := netip.ParseAddr(host)
	isIP := err == nil
	var ip net.IP
	if isIP {
		ip = net.IP(addr.AsSlice())
	}
	for _, p := range strings.Split(list, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		switch {
		case p == "":
			continue
		case p == "*":
			return true
		}
		if _, cidr, err := net.ParseCIDR(p); err == nil {
			if isIP && cidr.Contains(ip) {
				return true
			}
			continue
		}
		phost, pport, err := net.SplitHostPort(p)
		if err == nil {
			if phost == "" {
				continue
			}
			if phost[0] == '[' && phost[len(phost)-1] == ']' {
				phost = phost[1 : len(phost)-1]
			}
		} else {
			phost, pport = p, ""
		}
		portOK := pport == "" || pport == port
		if pip := net.ParseIP(phost); pip != nil {
			if isIP && pip.Equal(ip) && portOK {
				return true
			}
			continue
		}
		if isIP || !isASCII(phost) {
			continue // a domain entry never matches an IP
		}
		if strings.HasPrefix(phost, "*.") {
			phost = phost[1:]
		}
		whole := false
		if !strings.HasPrefix(phost, ".") {
			whole, phost = true, "."+phost
		}
		if (strings.HasSuffix(host, phost) || (whole && host == phost[1:])) && portOK {
			return true
		}
	}
	return false
}

// isASCII reports whether s holds only ASCII.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
