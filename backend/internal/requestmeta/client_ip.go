// Package requestmeta resolves metadata about inbound HTTP requests without
// discarding the original transport-level connection details.
package requestmeta

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the normalized client IP for a request. Forwarding headers
// are trusted only when the direct TCP peer is loopback, which is the boundary
// used by the local frontend reverse proxy.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	remote := parseRemoteIP(r.RemoteAddr)
	if remote.IsValid() && remote.IsLoopback() {
		if ip := rightmostForwardedIP(r.Header.Get("X-Forwarded-For")); ip != "" {
			return ip
		}
		if ip := parseIP(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
	}
	if remote.IsValid() {
		return remote.String()
	}
	return ""
}

func parseRemoteIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil {
		if ip, errParse := netip.ParseAddr(strings.TrimSpace(host)); errParse == nil {
			return ip.Unmap()
		}
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(remoteAddr))
	if err != nil {
		return netip.Addr{}
	}
	return ip.Unmap()
}

func rightmostForwardedIP(header string) string {
	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if ip := parseIP(parts[i]); ip != "" {
			return ip
		}
	}
	return ""
}

func parseIP(value string) string {
	ip, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return ip.Unmap().String()
}
