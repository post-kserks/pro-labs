package auth

import (
	"strings"
	"testing"
)

func TestDASTAuthBypass(t *testing.T) {
	mgr, err := New(true, map[string]string{"valid_token_abc123": "admin"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"empty", "", false},
		{"malformed", "not-a-real-token", false},
		{"sql-injection", "' OR '1'='1", false},
		{"null-byte", "vdb_sk_\x00\x00\x00", false},
		{"long-token", strings.Repeat("a", 1<<20), false},
		{"valid-prefix-wrong", "vdb_sk_" + strings.Repeat("0", 32), false},
		{"double-quote-injection", `" OR ""="`, false},
		{"unicode-bypass", "\admin\u0000", false},
		{"empty-after-trim", "   ", false},
		{"valid-token", "valid_token_abc123", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mgr.ValidateToken(c.token)
			if got != c.want {
				t.Errorf("ValidateToken(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestDASTAuthBypassDisabled(t *testing.T) {
	mgr, err := New(false, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// When auth is disabled, all tokens should be accepted
	if !mgr.ValidateToken("anything") {
		t.Error("disabled auth should accept any token")
	}
	if !mgr.ValidateToken("") {
		t.Error("disabled auth should accept empty token")
	}
	if !mgr.ValidateToken("' OR '1'='1") {
		t.Error("disabled auth should accept SQL injection token")
	}
}
