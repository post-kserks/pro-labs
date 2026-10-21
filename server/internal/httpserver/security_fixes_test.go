package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRespectsConfiguredLocalhostBypass(t *testing.T) {
	m := mustAuth(t, true, map[string]string{"tok": "ci"})
	t.Cleanup(m.Close)
	m.SetLocalhostBypass(false)
	srv, _ := newTestServerWithDB(t, m)

	if m.LocalhostBypass() {
		t.Fatal("httpserver.New reset the configured localhost bypass")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(`{"database":"testdb","query":"SELECT * FROM items;"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:5555"
	srv.apiMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("localhost without token, bypass disabled: status %d, want 401", rec.Code)
	}
}

func TestJSONEndpointsRejectNonJSONContentType(t *testing.T) {
	m := mustAuth(t, true, map[string]string{"tok": "ci"})
	t.Cleanup(m.Close) // bypass stays at its default (true)
	srv, _ := newTestServerWithDB(t, m)

	paths := []string{"/api/query", "/api/query/stream", "/api/transaction", "/api/batch", "/api/v2/handshake", "/admin/revoke-token", "/api/databases/testdb/tables/items/data"}
	for _, ct := range []string{"", "text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonp"} {
		for _, p := range paths {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, p, strings.NewReader(`{"database":"testdb","query":"DROP TABLE items;"}`))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			req.Header.Set("Origin", "https://evil.example")
			req.RemoteAddr = "127.0.0.1:5555" // localhost bypass: the CSRF scenario
			srv.apiMux().ServeHTTP(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%s with Content-Type %q: status %d, want 415", p, ct, rec.Code)
			}
		}
	}

	for _, ct := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(`{"database":"testdb","query":"SELECT * FROM items;"}`))
		req.Header.Set("Content-Type", ct)
		req.RemoteAddr = "127.0.0.1:5555"
		srv.apiMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Content-Type %q: status %d, want 200: %s", ct, rec.Code, rec.Body.String())
		}
	}
}

func TestLiveQueryRejectsCrossOrigin(t *testing.T) {
	m := mustAuth(t, true, map[string]string{"tok": "ci"})
	t.Cleanup(m.Close)
	srv, _ := newTestServerWithDB(t, m)
	srv.cfg.AllowedOrigins = []string{"https://app.example"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // SSE loop exits right after the initial result

	cases := []struct {
		origin string
		want   int
	}{
		{"", http.StatusOK},
		{"http://example.com", http.StatusOK}, // httptest Host is example.com
		{"https://app.example", http.StatusOK},
		{"https://evil.example", http.StatusForbidden},
		{"null", http.StatusForbidden},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/live?database=testdb&query=SELECT+*+FROM+items%3B", nil).WithContext(ctx)
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		req.RemoteAddr = "127.0.0.1:5555"
		srv.apiMux().ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Fatalf("Origin %q: status %d, want %d: %s", c.origin, rec.Code, c.want, rec.Body.String())
		}
	}
}

func TestHTTPEnforcesRBAC(t *testing.T) {
	m := mustAuth(t, true, nil)
	t.Cleanup(m.Close)
	m.AddToken("readtok", "ro", "reader")
	m.AddToken("admintok", "rw", "admin")
	srv, _ := newTestServerWithDB(t, m)

	do := func(method, path, token, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		srv.apiMux().ServeHTTP(rec, req)
		return rec
	}
	insert := `{"database":"testdb","query":"INSERT INTO items (id, name, value) VALUES (9, 'x', 1.0);"}`

	if rec := do(http.MethodPost, "/api/query", "readtok", `{"database":"testdb","query":"SELECT * FROM items;"}`); rec.Code != http.StatusOK {
		t.Fatalf("reader SELECT: status %d: %s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/api/query", "/api/query/stream"} {
		rec := do(http.MethodPost, p, "readtok", insert)
		if !strings.Contains(rec.Body.String(), "permission denied") {
			t.Fatalf("reader INSERT via %s not denied: status %d: %s", p, rec.Code, rec.Body.String())
		}
	}
	rec := do(http.MethodPost, "/api/batch", "readtok", `{"database":"testdb","queries":[{"query":"DELETE FROM items;"}]}`)
	if !strings.Contains(rec.Body.String(), "permission denied") {
		t.Fatalf("reader DELETE via /api/batch not denied: %s", rec.Body.String())
	}
	rec = do(http.MethodPost, "/api/databases/testdb/tables/items/data", "readtok", `[{"id":10,"name":"y","value":2.0}]`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reader POST table data: status %d, want 403: %s", rec.Code, rec.Body.String())
	}

	if rec := do(http.MethodPost, "/api/query", "admintok", insert); rec.Code != http.StatusOK {
		t.Fatalf("admin INSERT: status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBindAuthClearsPreviousIdentity(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))
	sess := srv.newSession()
	defer sess.Close()
	sess.SetToken("stale")
	sess.SetRole("admin")

	bindAuth(httptest.NewRequest(http.MethodPost, "/", nil), sess)
	if sess.GetToken() != "" || sess.GetRole() != "" {
		t.Fatalf("bindAuth kept stale identity: token=%q role=%q", sess.GetToken(), sess.GetRole())
	}
}

func TestIsJSONContentType(t *testing.T) {
	for ct, want := range map[string]bool{
		"application/json":                  true,
		" application/json ; charset=utf-8": true,
		"APPLICATION/JSON":                  true,
		"":                                  false,
		"text/plain":                        false,
		"application/json-patch+json":       false,
	} {
		if got := isJSONContentType(ct); got != want {
			t.Errorf("isJSONContentType(%q) = %v, want %v", ct, got, want)
		}
	}
}
