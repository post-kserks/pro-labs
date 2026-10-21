package auth

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRevokedTokenNotReenabledByPurge(t *testing.T) {
	m, err := New(true, map[string]string{"static-tok": "cfg"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()
	gen := m.GenerateToken("gen", "admin")

	m.RevokeToken("static-tok")
	m.RevokeToken(gen)

	// Age every revocation past the purge horizon and purge.
	m.mu.Lock()
	for h := range m.revoked {
		m.revoked[h] = time.Now().Add(-48 * time.Hour)
	}
	m.mu.Unlock()
	m.purgeExpiredRevokedTokens()

	if m.ValidateToken("static-tok") || m.ValidateToken(gen) {
		t.Fatal("revoked token became valid again after purge")
	}
	if m.GetTokenRole(gen) != "" || m.CheckPermission(gen, "SELECT") {
		t.Fatal("revoked token still has a role/permissions")
	}
	// Static (config/env) revocations must survive the purge: those tokens are
	// re-registered on restart and only the persisted entry keeps them rejected.
	if !m.IsRevoked("static-tok") {
		t.Fatal("static token revocation was purged")
	}
	// Generated tokens are never re-registered, so their entries may be purged.
	if m.IsRevoked(gen) {
		t.Fatal("expected stale generated-token revocation to be purged")
	}
}

func TestRevokedStaticTokenStaysRevokedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAULTDB_AUTH_SECRET", "restart-secret")

	m1, err := New(true, map[string]string{"tok": "cfg"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m1.SetDataDir(dir)
	m1.RevokeToken("tok")
	m1.mu.Lock()
	for h := range m1.revoked {
		m1.revoked[h] = time.Now().Add(-48 * time.Hour)
	}
	m1.mu.Unlock()
	m1.purgeExpiredRevokedTokens()
	m1.Close()

	m2, err := New(true, map[string]string{"tok": "cfg"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m2.Close()
	m2.SetDataDir(dir)
	if m2.ValidateToken("tok") {
		t.Fatal("revoked config token accepted after restart")
	}
}

func TestCloseIdempotent(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.Close()
	m.Close()

	d, _ := NewDisabled()
	d.Close()
}

func TestCheckPermissionIgnoresObjectSpecificGrants(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()
	tok := m.GenerateToken("u", "analyst")
	m.SetGrantsProvider(&mockGrantsProvider{grants: map[string]map[string][]string{
		"analyst": {
			"public_table": {"ALL"},
			"*":            {"SELECT"},
		},
	}})

	if !m.CheckPermission(tok, "SELECT") {
		t.Fatal("global SELECT grant should allow SELECT")
	}
	for _, op := range []string{"DELETE", "DROP TABLE", "INSERT"} {
		if m.CheckPermission(tok, op) {
			t.Fatalf("object-specific ALL grant must not grant global %s", op)
		}
	}
}

func TestLocalhostBypassConcurrentToggle(t *testing.T) {
	m, err := New(true, map[string]string{"tok": "l"}, nil, 60, 1000, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()
	h := m.Middleware(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(v bool) {
			defer wg.Done()
			m.SetLocalhostBypass(v)
		}(i%2 == 0)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "127.0.0.1:1"
			h(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()

	m.SetLocalhostBypass(false)
	if m.LocalhostBypass() {
		t.Fatal("LocalhostBypass should be false")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("localhost without token and bypass off: status %d, want 401", rec.Code)
	}
}

func TestMiddlewarePutsIdentityInContext(t *testing.T) {
	m, err := New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()
	m.AddToken("rtok", "reporting", "reader")

	var gotTok, gotRole, gotLabel string
	h := m.Middleware(func(_ http.ResponseWriter, r *http.Request) {
		gotTok = TokenFromContext(r.Context())
		gotRole = RoleFromContext(r.Context())
		gotLabel = LabelFromContext(r.Context())
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer rtok")
	h(httptest.NewRecorder(), req)
	if gotTok != "rtok" || gotRole != "reader" || gotLabel != "reporting" {
		t.Fatalf("context identity = (%q, %q, %q)", gotTok, gotRole, gotLabel)
	}

	// Localhost bypass: no identity, handlers see "".
	gotTok = "x"
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	h(httptest.NewRecorder(), req)
	if gotTok != "" {
		t.Fatalf("bypass request should carry no token, got %q", gotTok)
	}
}
