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
