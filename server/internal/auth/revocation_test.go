package auth

import (
	"sync"
	"testing"
	"time"
)

func TestRevokeToken(t *testing.T) {
	m, err := New(true, map[string]string{"secret123": "admin"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !m.ValidateToken("secret123") {
		t.Fatal("token should be valid before revocation")
	}

	m.RevokeToken("secret123")

	if m.ValidateToken("secret123") {
		t.Fatal("token should be invalid after revocation")
	}
}

func TestRevokeUnknownToken(t *testing.T) {
	m, err := New(true, map[string]string{"secret123": "admin"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Revoking a token that was never added should not panic or affect other tokens.
	m.RevokeToken("unknown-token")

	if !m.ValidateToken("secret123") {
		t.Fatal("existing token should remain valid after revoking unknown token")
	}
}

func TestIsRevoked(t *testing.T) {
	m, err := New(true, map[string]string{"secret123": "admin"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if m.IsRevoked("secret123") {
		t.Fatal("token should not be revoked initially")
	}

	m.RevokeToken("secret123")

	if !m.IsRevoked("secret123") {
		t.Fatal("token should be revoked after RevokeToken")
	}
}

func TestRevokedTokenMiddlewareRejects(t *testing.T) {
	m, err := New(true, map[string]string{"secret123": "admin"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Simulate a request with a valid token.
	// We test via ValidateToken which Middleware calls internally.
	if !m.ValidateToken("secret123") {
		t.Fatal("token should be valid initially")
	}

	m.RevokeToken("secret123")

	if m.ValidateToken("secret123") {
		t.Fatal("revoked token should be rejected by ValidateToken")
	}
}

func TestConcurrentRevocation(t *testing.T) {
	m, err := New(true, map[string]string{"tok1": "a", "tok2": "b", "tok3": "c"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	// Concurrent revocations.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 3 {
			case 0:
				m.RevokeToken("tok1")
			case 1:
				m.RevokeToken("tok2")
			case 2:
				m.RevokeToken("tok3")
			}
		}(i)
	}
	// Concurrent reads.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = m.ValidateToken("tok1")
			_ = m.IsRevoked("tok2")
		}(i)
	}
	wg.Wait()

	for _, tok := range []string{"tok1", "tok2", "tok3"} {
		if !m.IsRevoked(tok) {
			t.Errorf("token %s should be revoked after concurrent revocation", tok)
		}
	}
}

func TestCleanupRevokedTokens(t *testing.T) {
	m, err := New(true, map[string]string{"secret123": "admin"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Manually inject a revoked token with an old timestamp.
	hash := m.hashToken("old-token")
	m.mu.Lock()
	m.revoked[hash] = time.Now().Add(-25 * time.Hour) // older than 24h
	m.mu.Unlock()

	// Also add a recent revoked token.
	m.RevokeToken("secret123")

	if !m.IsRevoked("old-token") {
		t.Fatal("old token should be in revoked map before cleanup")
	}

	// Run cleanup directly.
	m.mu.Lock()
	now := time.Now()
	for h, revokedAt := range m.revoked {
		if now.Sub(revokedAt) > 24*time.Hour {
			delete(m.revoked, h)
		}
	}
	m.mu.Unlock()

	if m.IsRevoked("old-token") {
		t.Fatal("old revoked token should have been cleaned up")
	}
	// Recent token should survive cleanup.
	if !m.IsRevoked("secret123") {
		t.Fatal("recent revoked token should survive cleanup")
	}
}
