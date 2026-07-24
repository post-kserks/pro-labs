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

	clientIP := net.ParseIP(host)
	isTrusted := false
	if clientIP != nil {
		for _, cidr := range trustedProxies {
			if cidr.Contains(clientIP) {
				isTrusted = true
				break
			}
		}
	}

	if isTrusted {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// Trusted proxies append their client's IP to the end of the list.
			// The first element can be spoofed by the client, so we take the last element.
			trimmed := strings.TrimSpace(parts[len(parts)-1])
			if trimmed != "" {
				return trimmed
			}
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}

	return host
}
