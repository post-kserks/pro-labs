package iputil

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractClientIP_RemoteAddr(t *testing.T) {
	tests := []struct {
		name     string
		remoteIP string
		want     string
	}{
		{"ipv4 with port", "10.0.0.1:12345", "10.0.0.1"},
		{"ipv6 with port", "[::1]:12345", "::1"},
		{"ipv4 without port", "10.0.0.1", "10.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteIP
			got := ExtractClientIP(req, nil)
			if got != tt.want {
				t.Errorf("ExtractClientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractClientIP_XForwardedFor(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	trustedProxies := []net.IPNet{*cidr}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.99:12345"
	req.Header.Set("X-Forwarded-For", "192.168.1.1, 10.0.0.99")

	ip := ExtractClientIP(req, trustedProxies)
	if ip != "192.168.1.1" {
		t.Errorf("ExtractClientIP() = %q, want %q", ip, "192.168.1.1")
	}
}

func TestExtractClientIP_XRealIP(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.99:12345"
	req.Header.Set("X-Real-IP", "172.16.0.1")

	got := ExtractClientIP(req, []net.IPNet{*cidr})
	if got != "172.16.0.1" {
		t.Errorf("ExtractClientIP() = %q, want %q", got, "172.16.0.1")
	}
}

func TestExtractClientIP_UntrustedProxyIgnoresHeaders(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")

	// Remote address is NOT in the trusted proxy range
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	got := ExtractClientIP(req, []net.IPNet{*cidr})
	if got != "192.168.1.100" {
		t.Errorf("ExtractClientIP() = %q, want %q (should ignore header for untrusted proxy)", got, "192.168.1.100")
	}
}

func TestExtractClientIP_NoTrustedProxiesReturnsRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	got := ExtractClientIP(req, nil)
	if got != "10.0.0.1" {
		t.Errorf("ExtractClientIP() = %q, want %q (no trusted proxies means ignore headers)", got, "10.0.0.1")
	}
}

func TestExtractClientIP_EmptyXForwardedForFallsToRemoteAddr(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.99:12345"
	req.Header.Set("X-Forwarded-For", "")

	got := ExtractClientIP(req, []net.IPNet{*cidr})
	if got != "10.0.0.99" {
		t.Errorf("ExtractClientIP() = %q, want %q", got, "10.0.0.99")
	}
}
