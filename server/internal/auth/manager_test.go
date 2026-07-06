package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoDefaultTokenInjected(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.ValidateToken("vdb_sk_local_dev") {
		t.Fatal("legacy hardcoded token still accepted")
	}
	if m.ValidateToken("") {
		t.Fatal("empty token accepted with auth enabled")
	}
}

func TestValidateToken(t *testing.T) {
	m, err := New(true, map[string]string{"sekret": "ci"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !m.ValidateToken("sekret") {
		t.Fatal("configured token rejected")
	}
	if m.ValidateToken("wrong") {
		t.Fatal("unknown token accepted")
	}

	disabled, err := New(false, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !disabled.ValidateToken("anything") {
		t.Fatal("disabled auth should accept any token")
	}
}

func TestMiddlewareAcceptsQueryParamToken(t *testing.T) {
	m, err := New(true, map[string]string{"sekret": "ci"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	handler := m.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Query param token rejected for SSE endpoints (security: tokens must not appear in URLs)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/live?token=sekret", nil)
	req.Header.Set("Accept", "text/event-stream")
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("query param token should be rejected for SSE: status %d", rec.Code)
	}

	// Query param token rejected for non-SSE endpoints
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/live?token=sekret", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("query param token accepted for non-SSE: status %d", rec.Code)
	}

	// Missing token still rejected
	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/live", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token allowed: status %d", rec.Code)
	}
}

func TestTokensStoredHashed(t *testing.T) {
	m, err := New(true, map[string]string{"plain-secret": "ci"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := m.tokens["plain-secret"]; ok {
		t.Fatal("plaintext token stored in manager")
	}
	if !m.ValidateToken("plain-secret") {
		t.Fatal("original token must validate after hashing")
	}
	if m.GetLabel("plain-secret") != "ci" {
		t.Fatal("label lookup by original token failed")
	}

	m.AddToken("another-token", "ops", "admin")
	if _, ok := m.tokens["another-token"]; ok {
		t.Fatal("AddToken stored plaintext")
	}
	if !m.ValidateToken("another-token") {
		t.Fatal("added token must validate")
	}
}
