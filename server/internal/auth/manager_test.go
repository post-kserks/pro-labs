package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoDefaultTokenInjected(t *testing.T) {
	m := New(true, nil)
	if m.ValidateToken("vdb_sk_local_dev") {
		t.Fatal("legacy hardcoded token still accepted")
	}
	if m.ValidateToken("") {
		t.Fatal("empty token accepted with auth enabled")
	}
}

func TestValidateToken(t *testing.T) {
	m := New(true, map[string]string{"sekret": "ci"})
	if !m.ValidateToken("sekret") {
		t.Fatal("configured token rejected")
	}
	if m.ValidateToken("wrong") {
		t.Fatal("unknown token accepted")
	}

	disabled := New(false, nil)
	if !disabled.ValidateToken("anything") {
		t.Fatal("disabled auth should accept any token")
	}
}

func TestMiddlewareAcceptsQueryParamToken(t *testing.T) {
	m := New(true, map[string]string{"sekret": "ci"})
	handler := m.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/live?token=sekret", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("query param token rejected: status %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/live", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token allowed: status %d", rec.Code)
	}
}

func TestTokensStoredHashed(t *testing.T) {
	m := New(true, map[string]string{"plain-secret": "ci"})
	if _, ok := m.tokens["plain-secret"]; ok {
		t.Fatal("plaintext token stored in manager")
	}
	if !m.ValidateToken("plain-secret") {
		t.Fatal("original token must validate after hashing")
	}
	if m.GetLabel("plain-secret") != "ci" {
		t.Fatal("label lookup by original token failed")
	}

	m.AddToken("another-token", "ops")
	if _, ok := m.tokens["another-token"]; ok {
		t.Fatal("AddToken stored plaintext")
	}
	if !m.ValidateToken("another-token") {
		t.Fatal("added token must validate")
	}
}
