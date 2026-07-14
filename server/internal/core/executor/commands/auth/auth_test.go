package auth_test

import (
	"testing"

	"vaultdb/internal/auth"
	"vaultdb/internal/core/executor"
)

func TestRBACCommands(t *testing.T) {
	session := executor.SetupSessionWithDB(t, "rbacdb")

	// Create role
	resCreate := executor.ExecuteSQL(t, session, "CREATE ROLE testuser WITH PASSWORD 'secret123';")
	if resCreate.Type != "message" {
		t.Fatalf("expected message result for CREATE ROLE, got %s: %s", resCreate.Type, resCreate.Message)
	}

	// Grant privilege
	executor.ExecuteSQL(t, session, "CREATE TABLE data_t (id INT, val TEXT);")
	resGrant := executor.ExecuteSQL(t, session, "GRANT SELECT ON data_t TO testuser;")
	if resGrant.Type != "message" {
		t.Fatalf("expected message result for GRANT, got %s: %s", resGrant.Type, resGrant.Message)
	}

	// Revoke privilege
	resRevoke := executor.ExecuteSQL(t, session, "REVOKE SELECT ON data_t FROM testuser;")
	if resRevoke.Type != "message" {
		t.Fatalf("expected message result for REVOKE, got %s: %s", resRevoke.Type, resRevoke.Message)
	}

	// Drop role
	resDrop := executor.ExecuteSQL(t, session, "DROP ROLE testuser;")
	if resDrop.Type != "message" {
		t.Fatalf("expected message result for DROP ROLE, got %s: %s", resDrop.Type, resDrop.Message)
	}
}

func TestRevokeToken(t *testing.T) {
	session := executor.SetupSessionWithDB(t, "authdb")
	mgr, _ := auth.New(true, nil, nil, 0, 0, 0)
	session.SetAuthManager(mgr)
	res := executor.ExecuteSQL(t, session, "REVOKE TOKEN 'secret-token-123';")
	if res.Type != "message" && res.Type != "error" {
		t.Fatalf("expected message or error for REVOKE TOKEN, got %s: %s", res.Type, res.Message)
	}
}
