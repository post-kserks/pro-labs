package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestRevokedTokensSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	secret := "roundtrip-test-secret"
	os.Setenv("VAULTDB_AUTH_SECRET", secret)
	defer os.Unsetenv("VAULTDB_AUTH_SECRET")

	// Create manager and revoke a token
	m1, err := New(true, map[string]string{"tok1": "ci"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m1.SetDataDir(dir)
	m1.RevokeToken("tok1")

	// Verify file exists
	if _, err := os.Stat(filepath.Join(dir, "revoked_tokens.json")); err != nil {
		t.Fatalf("revoked_tokens.json not created: %v", err)
	}

	// Create a new manager with the same secret and load revoked tokens
	m2, err := New(true, map[string]string{"tok1": "ci"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m2.SetDataDir(dir)

	// Token should be revoked in the new manager
	if !m2.IsRevoked("tok1") {
		t.Fatal("revoked token not loaded from disk")
	}
	if m2.ValidateToken("tok1") {
		t.Fatal("revoked token still validates after loading")
	}
}

func TestLoadRevokedMissingFile(t *testing.T) {
	dir := t.TempDir()

	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetDataDir(dir)

	// Should not error on missing file
	m.LoadRevoked()

	// Revoked map should still be empty
	m.mu.RLock()
	n := len(m.revoked)
	m.mu.RUnlock()
	if n != 0 {
		t.Fatalf("revoked map not empty after loading missing file: %d entries", n)
	}
}

func TestLoadRevokedCorruptFile(t *testing.T) {
	dir := t.TempDir()

	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetDataDir(dir)

	// Write corrupt data
	os.WriteFile(filepath.Join(dir, "revoked_tokens.json"), []byte("not valid json"), 0o600)

	// Should not panic
	m.LoadRevoked()
}

func TestRevokedTokensConcurrentAccess(t *testing.T) {
	dir := t.TempDir()

	secret := "concurrent-test-secret"
	os.Setenv("VAULTDB_AUTH_SECRET", secret)
	defer os.Unsetenv("VAULTDB_AUTH_SECRET")

	m, err := New(true, map[string]string{"tok1": "c1", "tok2": "c2"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetDataDir(dir)

	// Run concurrent revocations
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := "tok1"
			if i%2 == 0 {
				token = "tok2"
			}
			m.RevokeToken(token)
		}(i)
	}
	wg.Wait()

	// Verify the file is valid JSON and both tokens are present
	data, err := os.ReadFile(filepath.Join(dir, "revoked_tokens.json"))
	if err != nil {
		t.Fatalf("failed to read revoked file: %v", err)
	}

	var entries []revokedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("revoked file is not valid JSON: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 revoked entries, got %d", len(entries))
	}
}

func TestSaveRevokedNoDataDir(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// No data dir set — should not panic
	m.RevokeToken("anything")
}

func TestCleanupRevokedTokensPersists(t *testing.T) {
	dir := t.TempDir()

	secret := "cleanup-test-secret"
	os.Setenv("VAULTDB_AUTH_SECRET", secret)
	defer os.Unsetenv("VAULTDB_AUTH_SECRET")

	m, err := New(true, map[string]string{"tok1": "c1"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.SetDataDir(dir)

	// Manually add an expired revoked token
	m.mu.Lock()
	m.revoked["expired-hash"] = time.Now().Add(-25 * time.Hour)
	m.revoked["fresh-hash"] = time.Now()
	m.mu.Unlock()

	// Trigger cleanup
	m.purgeExpiredRevokedTokens()

	// Verify expired entry was removed
	m.mu.RLock()
	_, hasExpired := m.revoked["expired-hash"]
	_, hasFresh := m.revoked["fresh-hash"]
	m.mu.RUnlock()

	if hasExpired {
		t.Fatal("expired revoked token not cleaned up")
	}
	if !hasFresh {
		t.Fatal("fresh revoked token incorrectly removed")
	}

	// Verify the file was updated
	data, err := os.ReadFile(filepath.Join(dir, "revoked_tokens.json"))
	if err != nil {
		t.Fatalf("failed to read revoked file after cleanup: %v", err)
	}
	var entries []revokedEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("revoked file not valid JSON after cleanup: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after cleanup, got %d", len(entries))
	}
}
