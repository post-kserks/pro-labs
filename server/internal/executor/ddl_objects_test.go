package executor

import (
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
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
		cmd := &CreateViewCommand{stmt: stmt}
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
		cmd := &CreateViewCommand{stmt: stmt}
		_, err := cmd.Execute(ctx)
		if err != nil {
			t.Fatalf("create error: %v", err)
		}

		dropStmt := &parser.DropViewStatement{Name: "v1"}
		dropCmd := &DropViewCommand{stmt: dropStmt}
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
		cmd := &CreateTriggerCommand{stmt: stmt}
		_, err := cmd.Execute(ctx)
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

		_, _ = (&CreateViewCommand{stmt: &parser.CreateViewStatement{
			Name: "v1", Query: &parser.SelectStatement{TableName: "t"},
		}}).Execute(ctx)
		_, _ = (&CreateViewCommand{stmt: &parser.CreateViewStatement{
			Name: "v2", Query: &parser.SelectStatement{TableName: "t"},
		}}).Execute(ctx)
		_, _ = (&CreateTriggerCommand{stmt: &parser.CreateTriggerStatement{
			Name: "tr1", TableName: "t", Timing: "AFTER", Event: "INSERT", Body: "SELECT 1;",
		}}).Execute(ctx)

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
		cmd := &ShowTablesCommand{stmt: stmt}
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
