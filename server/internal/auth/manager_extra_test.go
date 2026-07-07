package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMiddlewareEnabledValidToken(t *testing.T) {
	m, err := New(true, map[string]string{"mytoken": "user"}, nil, 60, 10, 300)
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
	m, err := New(true, map[string]string{"mytoken": "user"}, nil, 60, 10, 300)
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
	m, err := New(true, map[string]string{"mytoken": "user"}, nil, 60, 10, 300)
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
	m, err := New(true, map[string]string{"token1": "admin", "token2": "readonly"}, nil, 60, 10, 300)
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
	m, err := New(true, map[string]string{"existing": "old"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if m.ValidateToken("newtoken") {
		t.Fatal("newtoken should not validate before AddToken")
	}

	m.AddToken("newtoken", "newlabel", "reader")

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
	m, err := New(false, nil, nil, 60, 10, 300)
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

// TestRateLimiterSweepBoundsMemory проверяет, что карты attempts/blocked не
// растут неограниченно: устаревшие записи с большого числа разных IP вычищаются
// фоновым sweep'ом по истечении окна.
func TestRateLimiterSweepBoundsMemory(t *testing.T) {
	rl := newAuthRateLimiter(1, 100, 1) // окно 1с, блок 1с

	// Заполняем тысячей разных IP с одной (недостаточной для блокировки) ошибкой.
	for i := 0; i < 1000; i++ {
		rl.recordFailure(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	if got := len(rl.attempts); got < 900 {
		t.Fatalf("expected ~1000 attempt entries before sweep, got %d", got)
	}

	// Ждём, пока окно истечёт, и провоцируем sweep ещё одним вызовом.
	time.Sleep(1100 * time.Millisecond)
	rl.recordFailure("172.16.0.1")

	// Все старые записи (последняя попытка вне окна, не заблокированы) должны уйти.
	if got := len(rl.attempts); got > 5 {
		t.Fatalf("expected stale attempt entries to be swept, got %d", got)
	}
}

// --- RBAC Tests ---

func TestGenerateToken(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	token := m.GenerateToken("testuser", "reader")
	if token == "" {
		t.Fatal("GenerateToken returned empty token")
	}
	if !m.ValidateToken(token) {
		t.Fatal("generated token should validate")
	}
	if label := m.GetLabel(token); label != "testuser" {
		t.Fatalf("GetLabel = %q, want %q", label, "testuser")
	}
	if role := m.GetTokenRole(token); role != "reader" {
		t.Fatalf("GetTokenRole = %q, want %q", role, "reader")
	}
}

func TestCheckPermissionAdmin(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token := m.GenerateToken("admin", "admin")

	for _, op := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE TABLE", "DROP TABLE", "ALTER TABLE"} {
		if !m.CheckPermission(token, op) {
			t.Fatalf("admin should be allowed %s", op)
		}
	}
}

func TestCheckPermissionReader(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token := m.GenerateToken("reader", "reader")

	if !m.CheckPermission(token, "SELECT") {
		t.Fatal("reader should be allowed SELECT")
	}
	if !m.CheckPermission(token, "EXPLAIN") {
		t.Fatal("reader should be allowed EXPLAIN")
	}
	if m.CheckPermission(token, "INSERT") {
		t.Fatal("reader should NOT be allowed INSERT")
	}
	if m.CheckPermission(token, "DELETE") {
		t.Fatal("reader should NOT be allowed DELETE")
	}
	if m.CheckPermission(token, "CREATE TABLE") {
		t.Fatal("reader should NOT be allowed CREATE TABLE")
	}
}

func TestCheckPermissionWriter(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token := m.GenerateToken("writer", "writer")

	for _, op := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE TABLE", "DROP TABLE"} {
		if !m.CheckPermission(token, op) {
			t.Fatalf("writer should be allowed %s", op)
		}
	}
}

func TestCheckPermissionUnknownRole(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Manually inject a token with an invalid role
	hash := m.hashToken("badrole")
	m.mu.Lock()
	m.tokens[hash] = &TokenInfo{Hash: hash, Label: "bad", Role: "superuser", CreatedAt: time.Now()}
	m.mu.Unlock()

	if m.CheckPermission("badrole", "SELECT") {
		t.Fatal("unknown role should not have any permissions")
	}
}

func TestCheckPermissionDisabledAuth(t *testing.T) {
	m, err := New(false, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// When auth is disabled, everything should be allowed
	if !m.CheckPermission("anything", "DROP TABLE") {
		t.Fatal("disabled auth should allow all operations")
	}
}

func TestGetTokenRoleUnknown(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if role := m.GetTokenRole("nonexistent"); role != "" {
		t.Fatalf("GetTokenRole for unknown token = %q, want empty", role)
	}
}

func TestDefaultRoleIsAdmin(t *testing.T) {
	m, err := New(true, map[string]string{"mytoken": "admin"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Pre-configured tokens should default to admin role
	if role := m.GetTokenRole("mytoken"); role != "admin" {
		t.Fatalf("default role = %q, want %q", role, "admin")
	}
	if !m.CheckPermission("mytoken", "DELETE") {
		t.Fatal("default admin should be allowed DELETE")
	}
}

// mockGrantsProvider implements GrantsProvider for testing.
type mockGrantsProvider struct {
	grants map[string]map[string][]string // role -> object -> []privilege
}

func (p *mockGrantsProvider) GetRoleGrants(roleName string) (map[string][]string, error) {
	if p.grants == nil {
		return nil, nil
	}
	return p.grants[roleName], nil
}

func TestCheckPermissionDynamicGrants(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Create a token with a custom role (not in hardcoded map).
	token := m.GenerateToken("custom-user", "custom")

	// Without grants provider, unknown role should be denied.
	if m.CheckPermission(token, "SELECT") {
		t.Fatal("custom role without grants should be denied")
	}

	// Set up dynamic grants provider.
	m.SetGrantsProvider(&mockGrantsProvider{
		grants: map[string]map[string][]string{
			"custom": {
				"*": {"SELECT", "INSERT"},
			},
		},
	})

	// Now custom role should have SELECT and INSERT.
	if !m.CheckPermission(token, "SELECT") {
		t.Fatal("custom role should be allowed SELECT via dynamic grants")
	}
	if !m.CheckPermission(token, "INSERT") {
		t.Fatal("custom role should be allowed INSERT via dynamic grants")
	}
	if m.CheckPermission(token, "DELETE") {
		t.Fatal("custom role should NOT be allowed DELETE via dynamic grants")
	}
}

func TestCheckPermissionDynamicGrantsFallback(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	token := m.GenerateToken("reader-user", "reader")

	// Set up dynamic grants that restrict reader beyond defaults.
	m.SetGrantsProvider(&mockGrantsProvider{
		grants: map[string]map[string][]string{
			"reader": {}, // empty grants — no dynamic permissions
		},
	})

	// Fallback to hardcoded: reader has SELECT.
	if !m.CheckPermission(token, "SELECT") {
		t.Fatal("reader should still have SELECT from hardcoded fallback")
	}
	// Fallback to hardcoded: reader doesn't have INSERT.
	if m.CheckPermission(token, "INSERT") {
		t.Fatal("reader should NOT have INSERT (neither dynamic nor hardcoded)")
	}
}
