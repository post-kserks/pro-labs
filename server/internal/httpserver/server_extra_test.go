package httpserver

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vaultdb/internal/iputil"
	"vaultdb/internal/metrics"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func TestHandleHealth(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want ok", body["status"])
	}
}

func TestHandleReady(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "ready" {
		t.Fatalf("status = %v, want ready", body["status"])
	}
}

func TestHandleMetrics(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", ct)
	}
}

func TestCORSMiddleware(t *testing.T) {
	srv := &Server{
		cfg: Config{
			AllowedOrigins: []string{"https://example.com"},
		},
		metrics: metrics.New(),
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.corsMiddleware(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Origin", "https://example.com")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Fatalf("CORS header = %q, want https://example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("Access-Control-Allow-Methods header not set")
	}
}

func TestWithMethod(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	srv := &Server{
		cfg: Config{
			Storage: store,
		},
		metrics: metrics.New(),
	}

	handler := srv.withMethod(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	handler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestExtractClientIPXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	req.RemoteAddr = "9.10.11.12:12345"

	ip := iputil.ExtractClientIP(req, nil)
	if ip != "9.10.11.12" {
		t.Fatalf("X-Forwarded-For without trusted proxy: got %q, want %q", ip, "9.10.11.12")
	}
}

func TestExtractClientIPXForwardedForTrusted(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	req.RemoteAddr = "10.0.0.1:12345"

	_, trusted, _ := net.ParseCIDR("10.0.0.0/8")
	ip := iputil.ExtractClientIP(req, []net.IPNet{*trusted})
	if ip != "1.2.3.4" {
		t.Fatalf("X-Forwarded-For trusted: got %q, want %q", ip, "1.2.3.4")
	}
}

func TestExtractClientIPXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "10.0.0.1")
	req.RemoteAddr = "9.10.11.12:12345"

	ip := iputil.ExtractClientIP(req, nil)
	if ip != "9.10.11.12" {
		t.Fatalf("X-Real-IP without trusted proxy: got %q, want %q", ip, "9.10.11.12")
	}
}

func TestExtractClientIPRemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:5432"

	ip := iputil.ExtractClientIP(req, nil)
	if ip != "192.168.1.1" {
		t.Fatalf("RemoteAddr: got %q, want %q", ip, "192.168.1.1")
	}
}

func TestHealthEndpointAuth(t *testing.T) {
	t.Run("unauthenticated minimal", func(t *testing.T) {
		srv := newTestServer(t, mustAuth(t, true, map[string]string{"sekret": "ci"}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		srv.apiMux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if body["status"] != "ok" {
			t.Fatalf("status = %v, want ok", body["status"])
		}
		for _, key := range []string{"version", "uptime_s", "connections", "checks"} {
			if _, exists := body[key]; exists {
				t.Fatalf("unauthenticated response should not contain %q, got: %s", key, rec.Body.String())
			}
		}
	})

	t.Run("authenticated full", func(t *testing.T) {
		srv := newTestServer(t, mustAuth(t, true, map[string]string{"sekret": "ci"}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("Authorization", "Bearer sekret")
		srv.apiMux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if body["status"] != "ok" {
			t.Fatalf("status = %v, want ok", body["status"])
		}
		for _, key := range []string{"version", "uptime_s", "connections", "checks"} {
			if _, exists := body[key]; !exists {
				t.Fatalf("authenticated response should contain %q", key)
			}
		}
	})

	t.Run("auth disabled full", func(t *testing.T) {
		srv := newTestServer(t, mustAuth(t, false, nil))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		srv.apiMux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		for _, key := range []string{"version", "uptime_s", "connections", "checks"} {
			if _, exists := body[key]; !exists {
				t.Fatalf("auth-disabled response should contain %q", key)
			}
		}
	})
}

func TestExtractClientIPRemoteAddrNoPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1"

	ip := iputil.ExtractClientIP(req, nil)
	if ip != "192.168.1.1" {
		t.Fatalf("RemoteAddr no port: got %q, want %q", ip, "192.168.1.1")
	}
}
