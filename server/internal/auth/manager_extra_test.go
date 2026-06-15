package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareEnabledValidToken(t *testing.T) {
	m, err := New(true, map[string]string{"mytoken": "user"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	called := false
	handler := m.Middleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	handler(rec, req)

	if !called {
		t.Fatal("handler was not called with valid token")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareEnabledInvalidToken(t *testing.T) {
	m, err := New(true, map[string]string{"mytoken": "user"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	handler := m.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestMiddlewareEnabledNoToken(t *testing.T) {
	m, err := New(true, map[string]string{"mytoken": "user"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	handler := m.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestGetLabel(t *testing.T) {
	m, err := New(true, map[string]string{"token1": "admin", "token2": "readonly"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if label := m.GetLabel("token1"); label != "admin" {
		t.Fatalf("GetLabel(token1) = %q, want %q", label, "admin")
	}
	if label := m.GetLabel("token2"); label != "readonly" {
		t.Fatalf("GetLabel(token2) = %q, want %q", label, "readonly")
	}
	if label := m.GetLabel("unknown"); label != "unknown" {
		t.Fatalf("GetLabel(unknown) = %q, want %q", label, "unknown")
	}
}

func TestAddToken(t *testing.T) {
	m, err := New(true, map[string]string{"existing": "old"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if m.ValidateToken("newtoken") {
		t.Fatal("newtoken should not validate before AddToken")
	}

	m.AddToken("newtoken", "newlabel")

	if !m.ValidateToken("newtoken") {
		t.Fatal("newtoken should validate after AddToken")
	}
	if label := m.GetLabel("newtoken"); label != "newlabel" {
		t.Fatalf("GetLabel(newtoken) = %q, want %q", label, "newlabel")
	}

	if !m.ValidateToken("existing") {
		t.Fatal("existing token should still validate")
	}
}

func TestMiddlewareDisabled(t *testing.T) {
	m, err := New(false, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	called := false
	handler := m.Middleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	handler(rec, req)

	if !called {
		t.Fatal("handler was not called with auth disabled")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
