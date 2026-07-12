package executor

import (
	"testing"

	"vaultdb/internal/auth"
	"vaultdb/internal/parser"
)

func TestParseCreateRole(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		wantErr bool
		role    string
		passwd  string
	}{
		{"simple", "CREATE ROLE analyst;", false, "analyst", ""},
		{"with password", "CREATE ROLE dev WITH PASSWORD 'secret123';", false, "dev", "secret123"},
		{"missing name", "CREATE ROLE;", true, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr = %v", tt.sql, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			cr, ok := stmt.(*parser.CreateRoleStatement)
			if !ok {
				t.Fatalf("expected CreateRoleStatement, got %T", stmt)
			}
			if cr.Name != tt.role {
				t.Errorf("Name = %q, want %q", cr.Name, tt.role)
			}
			if cr.Password != tt.passwd {
				t.Errorf("Password = %q, want %q", cr.Password, tt.passwd)
			}
			if cr.StatementType() != "CREATE_ROLE" {
				t.Errorf("StatementType() = %q, want CREATE_ROLE", cr.StatementType())
			}
		})
	}
}

func TestParseDropRole(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		wantErr  bool
		role     string
		ifExists bool
	}{
		{"simple", "DROP ROLE analyst;", false, "analyst", false},
		{"if exists", "DROP ROLE IF EXISTS analyst;", false, "analyst", true},
		{"missing name", "DROP ROLE;", true, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr = %v", tt.sql, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			dr, ok := stmt.(*parser.DropRoleStatement)
			if !ok {
				t.Fatalf("expected DropRoleStatement, got %T", stmt)
			}
			if dr.Name != tt.role {
				t.Errorf("Name = %q, want %q", dr.Name, tt.role)
			}
			if dr.IfExists != tt.ifExists {
				t.Errorf("IfExists = %v, want %v", dr.IfExists, tt.ifExists)
			}
			if dr.StatementType() != "DROP_ROLE" {
				t.Errorf("StatementType() = %q, want DROP_ROLE", dr.StatementType())
			}
		})
	}
}

func TestParseGrant(t *testing.T) {
	tests := []struct {
		name       string
		sql        string
		wantErr    bool
		privileges []string
		on         string
		to         string
	}{
		{"single privilege", "GRANT SELECT ON users TO reader;", false, []string{"SELECT"}, "users", "reader"},
		{"multiple privileges", "GRANT SELECT, INSERT, UPDATE ON orders TO writer;", false, []string{"SELECT", "INSERT", "UPDATE"}, "orders", "writer"},
		{"all privileges", "GRANT ALL ON * TO admin;", false, []string{"ALL"}, "*", "admin"},
		{"missing ON", "GRANT SELECT TO reader;", true, nil, "", ""},
		{"missing TO", "GRANT SELECT ON users;", true, nil, "", ""},
		{"missing privileges", "GRANT ON users TO reader;", true, nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr = %v", tt.sql, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			g, ok := stmt.(*parser.GrantStatement)
			if !ok {
				t.Fatalf("expected GrantStatement, got %T", stmt)
			}
			if len(g.Privileges) != len(tt.privileges) {
				t.Fatalf("Privileges = %v, want %v", g.Privileges, tt.privileges)
			}
			for i, p := range g.Privileges {
				if p != tt.privileges[i] {
					t.Errorf("Privileges[%d] = %q, want %q", i, p, tt.privileges[i])
				}
			}
			if g.On != tt.on {
				t.Errorf("On = %q, want %q", g.On, tt.on)
			}
			if g.To != tt.to {
				t.Errorf("To = %q, want %q", g.To, tt.to)
			}
			if g.StatementType() != "GRANT" {
				t.Errorf("StatementType() = %q, want GRANT", g.StatementType())
			}
		})
	}
}

func TestParseRevoke(t *testing.T) {
	tests := []struct {
		name       string
		sql        string
		wantErr    bool
		privileges []string
		on         string
		from       string
	}{
		{"single privilege", "REVOKE SELECT ON users FROM reader;", false, []string{"SELECT"}, "users", "reader"},
		{"multiple privileges", "REVOKE SELECT, DELETE ON orders FROM writer;", false, []string{"SELECT", "DELETE"}, "orders", "writer"},
		{"missing FROM", "REVOKE SELECT ON users;", true, nil, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr = %v", tt.sql, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			r, ok := stmt.(*parser.RevokeStatement)
			if !ok {
				t.Fatalf("expected RevokeStatement, got %T", stmt)
			}
			if len(r.Privileges) != len(tt.privileges) {
				t.Fatalf("Privileges = %v, want %v", r.Privileges, tt.privileges)
			}
			for i, p := range r.Privileges {
				if p != tt.privileges[i] {
					t.Errorf("Privileges[%d] = %q, want %q", i, p, tt.privileges[i])
				}
			}
			if r.On != tt.on {
				t.Errorf("On = %q, want %q", r.On, tt.on)
			}
			if r.From != tt.from {
				t.Errorf("From = %q, want %q", r.From, tt.from)
			}
			if r.StatementType() != "REVOKE" {
				t.Errorf("StatementType() = %q, want REVOKE", r.StatementType())
			}
		})
	}
}

func TestCreateRoleExec(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)
	ctx := &ExecutionContext{Storage: store, Session: session}

	// Create a role.
	cmd := &CreateRoleCommand{stmt: &parser.CreateRoleStatement{Name: "analyst", Password: ""}}
	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if result.Message != "Role 'analyst' created." {
		t.Errorf("message = %q", result.Message)
	}

	// Duplicate should fail.
	_, err = cmd.Execute(ctx)
	if err == nil {
		t.Fatal("expected duplicate role error")
	}
}

func TestDropRoleExec(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)
	ctx := &ExecutionContext{Storage: store, Session: session}

	// Create then drop.
	_, _ = (&CreateRoleCommand{stmt: &parser.CreateRoleStatement{Name: "dev"}}).Execute(ctx)

	cmd := &DropRoleCommand{stmt: &parser.DropRoleStatement{Name: "dev"}}
	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatalf("DropRole: %v", err)
	}
	if result.Message != "Role 'dev' dropped." {
		t.Errorf("message = %q", result.Message)
	}

	// Drop non-existent should fail.
	_, err = (&DropRoleCommand{stmt: &parser.DropRoleStatement{Name: "nonexistent"}}).Execute(ctx)
	if err == nil {
		t.Fatal("expected error for non-existent role")
	}

	// Drop IF EXISTS should not fail.
	result, err = (&DropRoleCommand{stmt: &parser.DropRoleStatement{Name: "nonexistent", IfExists: true}}).Execute(ctx)
	if err != nil {
		t.Fatalf("DropRole IF EXISTS: %v", err)
	}
	if result.Message == "" {
		t.Error("expected non-empty message")
	}
}

func TestGrantAndRevokeExec(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)
	ctx := &ExecutionContext{Storage: store, Session: session}

	// Create role first.
	_, _ = (&CreateRoleCommand{stmt: &parser.CreateRoleStatement{Name: "reader"}}).Execute(ctx)

	// Grant privileges.
	cmd := &GrantCommand{stmt: &parser.GrantStatement{
		Privileges: []string{"SELECT", "INSERT"},
		On:         "users",
		To:         "reader",
	}}
	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if result.Message == "" {
		t.Error("expected non-empty message")
	}

	// Duplicate grant should be silently ignored.
	result, err = cmd.Execute(ctx)
	if err != nil {
		t.Fatalf("Grant duplicate: %v", err)
	}
	if result.Message == "" {
		t.Error("expected non-empty message for duplicate grant")
	}

	// Revoke one privilege.
	cmd2 := &RevokeCommand{stmt: &parser.RevokeStatement{
		Privileges: []string{"INSERT"},
		On:         "users",
		From:       "reader",
	}}
	result, err = cmd2.Execute(ctx)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if result.Message == "" {
		t.Error("expected non-empty message")
	}

	// Verify grants are stored correctly.
	grants, err := GetRoleGrants(store, "reader")
	if err != nil {
		t.Fatalf("GetRoleGrants: %v", err)
	}
	if len(grants["users"]) != 1 {
		t.Fatalf("expected 1 grant on users, got %d", len(grants["users"]))
	}
	if grants["users"][0] != "SELECT" {
		t.Errorf("expected SELECT grant, got %q", grants["users"][0])
	}
}

func TestGrantNonExistentRole(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)
	ctx := &ExecutionContext{Storage: store, Session: session}

	cmd := &GrantCommand{stmt: &parser.GrantStatement{
		Privileges: []string{"SELECT"},
		On:         "users",
		To:         "nonexistent",
	}}
	_, err := cmd.Execute(ctx)
	if err == nil {
		t.Fatal("expected error for grant to non-existent role")
	}
}

func TestDropRoleCleansGrants(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)
	ctx := &ExecutionContext{Storage: store, Session: session}

	// Create role and grant privileges.
	_, _ = (&CreateRoleCommand{stmt: &parser.CreateRoleStatement{Name: "temp"}}).Execute(ctx)
	_, _ = (&GrantCommand{stmt: &parser.GrantStatement{
		Privileges: []string{"SELECT"},
		On:         "users",
		To:         "temp",
	}}).Execute(ctx)

	// Drop role.
	_, err := (&DropRoleCommand{stmt: &parser.DropRoleStatement{Name: "temp"}}).Execute(ctx)
	if err != nil {
		t.Fatalf("DropRole: %v", err)
	}

	// Verify grants are cleaned up.
	grants, err := GetRoleGrants(store, "temp")
	if err != nil {
		t.Fatalf("GetRoleGrants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("expected 0 grants after role drop, got %d", len(grants))
	}
}

func TestParseRevokeToken(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		wantErr bool
		token   string
	}{
		{"simple", "REVOKE TOKEN 'abc123';", false, "abc123"},
		{"missing token literal", "REVOKE TOKEN;", true, ""},
		{"extra content", "REVOKE TOKEN 'x' extra;", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr = %v", tt.sql, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			rt, ok := stmt.(*parser.RevokeTokenStatement)
			if !ok {
				t.Fatalf("expected RevokeTokenStatement, got %T", stmt)
			}
			if rt.Token != tt.token {
				t.Errorf("Token = %q, want %q", rt.Token, tt.token)
			}
			if rt.StatementType() != "REVOKE_TOKEN" {
				t.Errorf("StatementType() = %q, want REVOKE_TOKEN", rt.StatementType())
			}
		})
	}
}

func TestRevokeTokenCommand(t *testing.T) {
	store := NewMockStorage()
	mgr, err := auth.New(true, map[string]string{"test-token-123": "test-label"}, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	sess := NewSession(store, nil, nil, nil)
	sess.SetAuthManager(mgr)

	// Verify token is valid before revocation.
	if !mgr.ValidateToken("test-token-123") {
		t.Fatal("token should be valid before revocation")
	}

	// Execute REVOKE TOKEN.
	stmt, err := parser.Parse("REVOKE TOKEN 'test-token-123';")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := sess.Execute(stmt)
	if err != nil {
		t.Fatalf("Execute REVOKE TOKEN: %v", err)
	}
	if result.Message != "Token revoked." {
		t.Errorf("Message = %q, want %q", result.Message, "Token revoked.")
	}

	// Verify token is now revoked.
	if mgr.ValidateToken("test-token-123") {
		t.Error("token should be invalid after revocation")
	}
	if !mgr.IsRevoked("test-token-123") {
		t.Error("token should be in revoked set")
	}
}

func TestRevokeTokenCommandNoAuthManager(t *testing.T) {
	store := NewMockStorage()
	sess := NewSession(store, nil, nil, nil)
	// No auth manager set.

	stmt, err := parser.Parse("REVOKE TOKEN 'some-token';")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = sess.Execute(stmt)
	if err == nil {
		t.Fatal("expected error when auth manager is nil")
	}
	if err.Error() != "REVOKE TOKEN: auth manager not configured" {
		t.Errorf("error = %q, want %q", err.Error(), "REVOKE TOKEN: auth manager not configured")
	}
}

func TestRevokeTokenCommandEmptyToken(t *testing.T) {
	store := NewMockStorage()
	mgr, err := auth.New(true, nil, nil, 60, 10, 300)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	sess := NewSession(store, nil, nil, nil)
	sess.SetAuthManager(mgr)

	stmt, err := parser.Parse("REVOKE TOKEN '';")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	_, err = sess.Execute(stmt)
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if err.Error() != "REVOKE TOKEN: token string is required" {
		t.Errorf("error = %q, want %q", err.Error(), "REVOKE TOKEN: token string is required")
	}
}
