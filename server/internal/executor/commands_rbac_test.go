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
		{"user simple", "CREATE USER alice;", false, "alice", ""},
		{"user with password", "CREATE USER bob WITH PASSWORD 'pass123';", false, "bob", "pass123"},
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
		{"if exists", "DROP ROLE IF EXISTS dev;", false, "dev", true},
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
		{"multiple privileges", "GRANT SELECT, INSERT ON users TO writer;", false, []string{"SELECT", "INSERT"}, "users", "writer"},
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
				t.Errorf("Privileges = %v, want %v", g.Privileges, tt.privileges)
			}
			if g.On != tt.on {
				t.Errorf("On = %q, want %q", g.On, tt.on)
			}
			if g.To != tt.to {
				t.Errorf("To = %q, want %q", g.To, tt.to)
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
		{"multiple privileges", "REVOKE SELECT, INSERT ON users FROM writer;", false, []string{"SELECT", "INSERT"}, "users", "writer"},
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
				t.Errorf("Privileges = %v, want %v", r.Privileges, tt.privileges)
			}
			if r.On != tt.on {
				t.Errorf("On = %q, want %q", r.On, tt.on)
			}
			if r.From != tt.from {
				t.Errorf("From = %q, want %q", r.From, tt.from)
			}
		})
	}
}

func TestCreateRoleExec(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)
	ctx := &ExecutionContext{Storage: store, Session: session}

	// Create a role.
	stmt := &parser.CreateRoleStatement{Name: "analyst", Password: ""}
	cmd, err := CommandFactory(stmt)
	if err != nil {
		t.Fatal(err)
	}
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
	createStmt := &parser.CreateRoleStatement{Name: "dev"}
	createCmd, _ := CommandFactory(createStmt)
	_, _ = createCmd.Execute(ctx)

	dropStmt := &parser.DropRoleStatement{Name: "dev"}
	cmd, err := CommandFactory(dropStmt)
	if err != nil {
		t.Fatal(err)
	}
	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatalf("DropRole: %v", err)
	}
	if result.Message != "Role 'dev' dropped." {
		t.Errorf("message = %q", result.Message)
	}

	// Drop non-existent should fail.
	dropStmt2 := &parser.DropRoleStatement{Name: "nonexistent"}
	cmd2, _ := CommandFactory(dropStmt2)
	_, err = cmd2.Execute(ctx)
	if err == nil {
		t.Fatal("expected error for non-existent role")
	}

	// Drop IF EXISTS should not fail.
	dropStmt3 := &parser.DropRoleStatement{Name: "nonexistent", IfExists: true}
	cmd3, _ := CommandFactory(dropStmt3)
	result, err = cmd3.Execute(ctx)
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
	createStmt := &parser.CreateRoleStatement{Name: "reader"}
	createCmd, _ := CommandFactory(createStmt)
	_, _ = createCmd.Execute(ctx)

	// Grant privileges.
	grantStmt := &parser.GrantStatement{
		Privileges: []string{"SELECT", "INSERT"},
		On:         "users",
		To:         "reader",
	}
	cmd, err := CommandFactory(grantStmt)
	if err != nil {
		t.Fatal(err)
	}
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
	revokeStmt := &parser.RevokeStatement{
		Privileges: []string{"INSERT"},
		On:         "users",
		From:       "reader",
	}
	cmd2, err := CommandFactory(revokeStmt)
	if err != nil {
		t.Fatal(err)
	}
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

	grantStmt := &parser.GrantStatement{
		Privileges: []string{"SELECT"},
		On:         "users",
		To:         "nonexistent",
	}
	cmd, err := CommandFactory(grantStmt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cmd.Execute(ctx)
	if err == nil {
		t.Fatal("expected error for grant to non-existent role")
	}
}

func TestDropRoleCleansGrants(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)
	ctx := &ExecutionContext{Storage: store, Session: session}

	// Create role and grant privileges.
	createStmt := &parser.CreateRoleStatement{Name: "temp"}
	createCmd, _ := CommandFactory(createStmt)
	_, _ = createCmd.Execute(ctx)

	grantStmt := &parser.GrantStatement{
		Privileges: []string{"SELECT"},
		On:         "users",
		To:         "temp",
	}
	grantCmd, _ := CommandFactory(grantStmt)
	_, _ = grantCmd.Execute(ctx)

	// Drop role.
	dropStmt := &parser.DropRoleStatement{Name: "temp"}
	cmd, err := CommandFactory(dropStmt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cmd.Execute(ctx)
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
		})
	}
}

func TestRevokeTokenExec(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)
	mgr, _ := auth.New(true, nil, nil, 0, 0, 0)
	session.SetAuthManager(mgr)

	// Issue a token first
	token := mgr.GenerateToken("admin", "admin")

	ctx := &ExecutionContext{Storage: store, Session: session}

	// Revoke the token.
	stmt := &parser.RevokeTokenStatement{Token: token}
	cmd, err := CommandFactory(stmt)
	if err != nil {
		t.Fatal(err)
	}
	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if result.Message != "Token revoked." {
		t.Errorf("message = %q", result.Message)
	}
}

func TestRevokeTokenNoAuthManager(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)

	ctx := &ExecutionContext{Storage: store, Session: session}

	stmt := &parser.RevokeTokenStatement{Token: "some-token"}
	cmd, err := CommandFactory(stmt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cmd.Execute(ctx)
	if err == nil {
		t.Fatal("expected error when auth manager is nil")
	}
}

func TestRevokeTokenEmpty(t *testing.T) {
	store := NewMockStorage()
	session := newTestSession(store)

	ctx := &ExecutionContext{Storage: store, Session: session}

	stmt := &parser.RevokeTokenStatement{Token: ""}
	cmd, err := CommandFactory(stmt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cmd.Execute(ctx)
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}
