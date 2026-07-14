package ddl_test

import (
	"testing"

	"vaultdb/internal/core/executor"
)

func TestProcedureCallExecutes(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE proc_test (val INT);")
	executor.ExecuteSQL(t, session, "CREATE PROCEDURE fill_data () AS 'INSERT INTO proc_test VALUES (42)' LANGUAGE SQL;")
	result := executor.ExecuteSQL(t, session, "CALL fill_data();")
	if result.Type != "affected" {
		t.Fatalf("expected affected result, got %s", result.Type)
	}
	rows := executor.ExecuteSQL(t, session, "SELECT * FROM proc_test;")
	if len(rows.Rows) != 1 || rows.Rows[0][0] != "42" {
		t.Fatalf("expected row with 42, got: %v", rows.Rows)
	}
}

func TestProcedureMultiStatement(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE ms (id INT);")
	executor.ExecuteSQL(t, session, "CREATE PROCEDURE multi () AS 'INSERT INTO ms VALUES (1); INSERT INTO ms VALUES (2)' LANGUAGE SQL;")
	executor.ExecuteSQL(t, session, "CALL multi();")
	r := executor.ExecuteSQL(t, session, "SELECT COUNT(*) FROM ms;")
	if r.Rows[0][0] != "2" {
		t.Fatalf("expected 2 rows, got %s", r.Rows[0][0])
	}
}

func TestProcedureCallAndDrop(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE PROCEDURE noop () AS 'SELECT 1' LANGUAGE SQL;")
	executor.ExecuteSQL(t, session, "CALL noop();")
	executor.ExecuteSQL(t, session, "DROP PROCEDURE noop;")
}
