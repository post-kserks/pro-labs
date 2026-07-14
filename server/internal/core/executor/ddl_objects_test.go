package executor

import (
	"os"
	"path/filepath"
	"testing"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

func TestObjectPersistence(t *testing.T) {
	t.Run("create_view_persists_in_objects_table", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")
		executeSQL(t, session, "CREATE TABLE users (id INT, name TEXT);")

		executeSQL(t, session, "CREATE VIEW v_users AS SELECT id, name FROM users;")

		rows, err := store.ReadCurrentRows("testdb", "_objects")
		if err != nil {
			t.Fatalf("failed to read _objects: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 object row, got %d", len(rows))
		}
		if rows[0][0] != "v_users" {
			t.Fatalf("expected name 'v_users', got %v", rows[0][0])
		}
		if rows[0][1] != "view" {
			t.Fatalf("expected type 'view', got %v", rows[0][1])
		}
	})

	t.Run("create_trigger_persists_in_objects_table", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")
		executeSQL(t, session, "CREATE TABLE items (id INT, name TEXT);")

		executeSQL(t, session, "CREATE TRIGGER trg_items AFTER INSERT ON items BEGIN INSERT INTO items VALUES (99, 'triggered'); END;")

		rows, err := store.ReadCurrentRows("testdb", "_objects")
		if err != nil {
			t.Fatalf("failed to read _objects: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 object row, got %d", len(rows))
		}
		if rows[0][1] != "trigger" {
			t.Fatalf("expected type 'trigger', got %v", rows[0][1])
		}
	})

	t.Run("create_function_persists_in_objects_table", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")

		executeSQL(t, session, "CREATE FUNCTION double_val(x) RETURNS INT AS 'RETURN x * 2' LANGUAGE PLPGSQL;")

		rows, err := store.ReadCurrentRows("testdb", "_objects")
		if err != nil {
			t.Fatalf("failed to read _objects: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 object row, got %d", len(rows))
		}
		if rows[0][1] != "function" {
			t.Fatalf("expected type 'function', got %v", rows[0][1])
		}
	})

	t.Run("create_procedure_persists_in_objects_table", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")

		executeSQL(t, session, "CREATE PROCEDURE sp_hello() AS 'SELECT 1' LANGUAGE SQL;")

		rows, err := store.ReadCurrentRows("testdb", "_objects")
		if err != nil {
			t.Fatalf("failed to read _objects: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 object row, got %d", len(rows))
		}
		if rows[0][1] != "procedure" {
			t.Fatalf("expected type 'procedure', got %v", rows[0][1])
		}
	})

	t.Run("created_at_is_set", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")

		executeSQL(t, session, "CREATE VIEW v1 AS SELECT 1;")

		rows, err := store.ReadCurrentRows("testdb", "_objects")
		if err != nil {
			t.Fatalf("failed to read _objects: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 object row, got %d", len(rows))
		}
		if len(rows[0]) < 4 {
			t.Fatalf("expected at least 4 columns, got %d", len(rows[0]))
		}
		if rows[0][3] == nil || rows[0][3] == int64(0) {
			t.Fatalf("expected non-zero created_at, got %v", rows[0][3])
		}
	})
}

func TestObjectRecovery(t *testing.T) {
	t.Run("view_metadata_survives_restart", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")
		executeSQL(t, session, "CREATE TABLE users (id INT, name TEXT);")
		executeSQL(t, session, "INSERT INTO users VALUES (1, 'Alice');")
		executeSQL(t, session, "CREATE VIEW v_users AS SELECT id, name FROM users;")

		store.Close()

		store2, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store2.Close()

		session2 := NewSession(store2, nil, txm, nil)
		executeSQL(t, session2, "USE testdb;")

		rows, err := store2.ReadCurrentRows("testdb", "_objects")
		if err != nil {
			t.Fatalf("failed to read _objects: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("expected 1 object after restart, got %d", len(rows))
		}
		if rows[0][0] != "v_users" {
			t.Fatalf("expected view name 'v_users', got %v", rows[0][0])
		}
	})

	t.Run("dropped_view_stays_dropped", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")
		executeSQL(t, session, "CREATE TABLE t (id INT);")
		executeSQL(t, session, "CREATE VIEW v1 AS SELECT id FROM t;")
		executeSQL(t, session, "DROP VIEW v1;")

		store.Close()

		store2, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store2.Close()

		session2 := NewSession(store2, nil, txm, nil)
		executeSQL(t, session2, "USE testdb;")

		executeSQLExpectError(t, session2, "SELECT * FROM v1;")
	})

	t.Run("multiple_object_types_persist", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")
		executeSQL(t, session, "CREATE TABLE items (id INT, name TEXT);")
		executeSQL(t, session, "CREATE VIEW v_items AS SELECT id, name FROM items;")
		executeSQL(t, session, "CREATE FUNCTION get_name(x) RETURNS TEXT AS 'RETURN x' LANGUAGE PLPGSQL;")

		store.Close()

		store2, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store2.Close()
		session2 := NewSession(store2, nil, txm, nil)
		executeSQL(t, session2, "USE testdb;")

		rows, err := store2.ReadCurrentRows("testdb", "_objects")
		if err != nil {
			t.Fatalf("failed to read _objects: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("expected 2 objects after restart, got %d", len(rows))
		}
	})

	t.Run("created_at_preserved_on_update", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")
		executeSQL(t, session, "CREATE TABLE t (id INT);")
		executeSQL(t, session, "CREATE VIEW v1 AS SELECT id FROM t;")

		rows1, _ := store.ReadCurrentRows("testdb", "_objects")
		origCreatedAt := rows1[0][3]

		executeSQL(t, session, "CREATE OR REPLACE VIEW v1 AS SELECT id FROM t;")

		rows2, _ := store.ReadCurrentRows("testdb", "_objects")
		if len(rows2) != 1 {
			t.Fatalf("expected 1 row after replace, got %d", len(rows2))
		}
		if rows2[0][3] != origCreatedAt {
			t.Fatalf("created_at should be preserved on replace: orig=%v new=%v", origCreatedAt, rows2[0][3])
		}
	})
}

func TestObjectPersistenceMockStorage(t *testing.T) {
	t.Run("store_and_load_view", func(t *testing.T) {
		store := NewMockStorage()
		session := newTestSession(store)
		session.SetCurrentDatabase("mydb")
		store.CreateDatabase("mydb")
		ctx := &ExecutionContext{Storage: store, Session: session}

		stmt := &parser.CreateViewStatement{
			Name:  "v1",
			Query: &parser.SelectStatement{TableName: "t"},
		}
		cmd, err := CommandFactory(stmt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, err := cmd.Execute(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Type != "message" {
			t.Fatalf("expected message, got %s", result.Type)
		}

		def, err := loadObject(ctx, "mydb", objTypeView, "v1")
		if err != nil {
			t.Fatalf("loadObject error: %v", err)
		}
		if def == nil {
			t.Fatal("expected non-nil definition")
		}
		if def["name"] != "v1" {
			t.Fatalf("expected name 'v1', got %v", def["name"])
		}
	})

	t.Run("store_and_delete_view", func(t *testing.T) {
		store := NewMockStorage()
		session := newTestSession(store)
		session.SetCurrentDatabase("mydb")
		store.CreateDatabase("mydb")
		ctx := &ExecutionContext{Storage: store, Session: session}

		stmt := &parser.CreateViewStatement{
			Name:  "v1",
			Query: &parser.SelectStatement{TableName: "t"},
		}
		cmd, err := CommandFactory(stmt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = cmd.Execute(ctx)
		if err != nil {
			t.Fatalf("create error: %v", err)
		}

		dropStmt := &parser.DropViewStatement{Name: "v1"}
		dropCmd, err := CommandFactory(dropStmt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = dropCmd.Execute(ctx)
		if err != nil {
			t.Fatalf("drop error: %v", err)
		}

		def, err := loadObject(ctx, "mydb", objTypeView, "v1")
		if err != nil {
			t.Fatalf("loadObject error: %v", err)
		}
		if def != nil {
			t.Fatal("expected nil after drop")
		}
	})

	t.Run("store_and_load_trigger", func(t *testing.T) {
		store := NewMockStorage()
		session := newTestSession(store)
		session.SetCurrentDatabase("mydb")
		store.CreateDatabase("mydb")
		ctx := &ExecutionContext{Storage: store, Session: session}

		stmt := &parser.CreateTriggerStatement{
			Name:      "tr1",
			TableName: "items",
			Timing:    "AFTER",
			Event:     "INSERT",
			Body:      "INSERT INTO log VALUES (1);",
		}
		cmd, err := CommandFactory(stmt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err = cmd.Execute(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		def, err := loadObject(ctx, "mydb", objTypeTrigger, "tr1")
		if err != nil {
			t.Fatalf("loadObject error: %v", err)
		}
		if def == nil {
			t.Fatal("expected non-nil definition")
		}
		if def["name"] != "tr1" {
			t.Fatalf("expected name 'tr1', got %v", def["name"])
		}
	})

	t.Run("load_all_objects_by_type", func(t *testing.T) {
		store := NewMockStorage()
		session := newTestSession(store)
		session.SetCurrentDatabase("mydb")
		store.CreateDatabase("mydb")
		ctx := &ExecutionContext{Storage: store, Session: session}

		cmd1, _ := CommandFactory(&parser.CreateViewStatement{
			Name: "v1", Query: &parser.SelectStatement{TableName: "t"},
		})
		_, _ = cmd1.Execute(ctx)
		cmd2, _ := CommandFactory(&parser.CreateViewStatement{
			Name: "v2", Query: &parser.SelectStatement{TableName: "t"},
		})
		_, _ = cmd2.Execute(ctx)
		cmd3, _ := CommandFactory(&parser.CreateTriggerStatement{
			Name: "tr1", TableName: "t", Timing: "AFTER", Event: "INSERT", Body: "SELECT 1;",
		})
		_, _ = cmd3.Execute(ctx)

		views, err := loadAllObjectsByType(ctx, "mydb", objTypeView)
		if err != nil {
			t.Fatalf("loadAllObjectsByType error: %v", err)
		}
		if len(views) != 2 {
			t.Fatalf("expected 2 views, got %d", len(views))
		}

		triggers, err := loadAllObjectsByType(ctx, "mydb", objTypeTrigger)
		if err != nil {
			t.Fatalf("loadAllObjectsByType error: %v", err)
		}
		if len(triggers) != 1 {
			t.Fatalf("expected 1 trigger, got %d", len(triggers))
		}
	})

	t.Run("show_tables_excludes_objects", func(t *testing.T) {
		store := NewMockStorage()
		session := newTestSession(store)
		session.SetCurrentDatabase("mydb")
		store.CreateDatabase("mydb")
		ctx := &ExecutionContext{Storage: store, Session: session}

		_ = store.CreateTable("mydb", storage.TableSchema{
			Name: "users",
			Columns: []storage.ColumnSchema{
				{Name: "id", Type: "INT"},
			},
		})
		_ = store.CreateTable("mydb", storage.TableSchema{
			Name: systemTableName,
			Columns: []storage.ColumnSchema{
				{Name: "name", Type: "TEXT"},
				{Name: "type", Type: "TEXT"},
				{Name: "definition", Type: "TEXT"},
				{Name: "created_at", Type: "INT"},
			},
		})

		stmt := &parser.ShowTablesStatement{}
		cmd, err := CommandFactory(stmt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, err := cmd.Execute(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Rows) != 1 {
			t.Fatalf("expected 1 table (excluding _objects), got %d", len(result.Rows))
		}
		if result.Rows[0][0] != "users" {
			t.Fatalf("expected 'users', got %v", result.Rows[0][0])
		}
	})
}

func TestCreateFunctionWASMValidation(t *testing.T) {
	t.Run("WASM function validates file exists", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")

		// WASM file doesn't exist -> should fail
		_, err = session.Execute(&parser.CreateFunctionStatement{
			Name:       "hash_pii",
			Params:     []string{"value"},
			ReturnType: "TEXT",
			Body:       "file:///nonexistent/hash_pii.wasm",
			Language:   "WASM",
		})
		if err == nil {
			t.Fatal("expected error for missing WASM file")
		}
	})

	t.Run("WASM function with valid file path", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")

		// Create a fake WASM file under the data directory (DataDir() = dir/pagedb)
		wasmDir := filepath.Join(dir, "pagedb")
		wasmPath := filepath.Join(wasmDir, "test.wasm")
		if err := os.WriteFile(wasmPath, []byte("fake wasm"), 0644); err != nil {
			t.Fatal(err)
		}

		// Use a relative path (file://test.wasm) — absolute paths are rejected
		// because WASM modules must live under the data directory.
		result, err := session.Execute(&parser.CreateFunctionStatement{
			Name:       "test_wasm",
			Params:     []string{"x"},
			ReturnType: "INT",
			Body:       "file://test.wasm",
			Language:   "WASM",
			Options:    map[string]string{"memory_limit": "32MB"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Message != "Function 'test_wasm' created." {
			t.Fatalf("unexpected message: %s", result.Message)
		}
	})

	t.Run("WASM function rejects unknown options", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")

		wasmPath := dir + "/test.wasm"
		if err := os.WriteFile(wasmPath, []byte("fake wasm"), 0644); err != nil {
			t.Fatal(err)
		}

		_, err = session.Execute(&parser.CreateFunctionStatement{
			Name:       "test_wasm",
			Params:     []string{"x"},
			ReturnType: "INT",
			Body:       wasmPath,
			Language:   "WASM",
			Options:    map[string]string{"invalid_option": "value"},
		})
		if err == nil {
			t.Fatal("expected error for unknown WASM option")
		}
	})

	t.Run("SQL function still works", func(t *testing.T) {
		dir := t.TempDir()
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		session := NewSession(store, nil, txm, nil)

		executeSQL(t, session, "CREATE DATABASE testdb;")
		executeSQL(t, session, "USE testdb;")
		executeSQL(t, session, "CREATE TABLE t (a INT);")

		result, err := session.Execute(&parser.CreateFunctionStatement{
			Name:       "my_func",
			Params:     []string{"x"},
			ReturnType: "INT",
			Body:       "SELECT a FROM t",
			Language:   "SQL",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Message != "Function 'my_func' created." {
			t.Fatalf("unexpected message: %s", result.Message)
		}
	})
}

// --- Security: WASM path traversal tests ---

func TestValidateWASMPath(t *testing.T) {
	dir := t.TempDir()
	dataDir := t.TempDir()

	// Create a WASM file inside dataDir
	wasmPath := dir + "/test.wasm"
	if err := os.WriteFile(wasmPath, []byte{0x00, 0x61, 0x73, 0x6d}, 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		body    string
		dataDir string
		wantErr bool
	}{
		{"relative valid", "file://test.wasm", dir, false},
		{"absolute inside", "file://" + wasmPath, dataDir, true}, // absolute path rejected
		{"path traversal", "file://../../etc/passwd", dir, true}, // traversal rejected
		{"double dot", "file://sub/../../../etc/passwd", dir, true},
		{"nonexistent", "file://nonexistent.wasm", dir, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateWASMPath(tt.body, tt.dataDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWASMPath(%q, %q) error = %v, wantErr %v", tt.body, tt.dataDir, err, tt.wantErr)
			}
		})
	}
}
