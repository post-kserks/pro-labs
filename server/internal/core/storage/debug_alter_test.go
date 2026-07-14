package storage

import (
	"testing"
)

func TestDebugAlterTable(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "alice", 1.0}})

	if err := e.AlterTableAddColumn("db", "users", ColumnSchema{Name: "age", Type: "INT"}, int64(18)); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.ReadCurrentRows("db", "users")
	t.Logf("rows after add column: %d", len(rows))
	if len(rows) != 1 || len(rows[0]) != 4 || rows[0][3] != int64(18) {
		t.Fatalf("after add column: %#v", rows)
	}
}
