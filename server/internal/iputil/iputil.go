package iputil

import (
	"net"
	"net/http"
	"strings"
)

// ExtractClientIP extracts the real client IP from the request.
// trustedProxies is a list of CIDR ranges of trusted reverse proxies.
// If the request comes from a trusted proxy, X-Forwarded-For is used.
// Otherwise, RemoteAddr is used directly — spoofed headers are ignored.
func ExtractClientIP(r *http.Request, trustedProxies []net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	if !isTrusted(net.ParseIP(host), trustedProxies) {
		return host
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Entries left of the last trusted hop are client-controlled, so walk
		// right to left and return the first address not owned by a trusted proxy.
		for xff != "" {
			entry := xff
			if i := strings.LastIndexByte(xff, ','); i >= 0 {
				entry, xff = xff[i+1:], xff[:i]
			} else {
				xff = ""
			}
			entry = strings.TrimSpace(entry)
			ip := net.ParseIP(entry)
			if ip == nil {
				// Malformed hop: nothing to its left can be trusted.
				return host
			}
			if !isTrusted(ip, trustedProxies) {
				return entry
			}
		}
		return host
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	return host
}

func isTrusted(ip net.IP, trustedProxies []net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, cidr := range trustedProxies {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}
