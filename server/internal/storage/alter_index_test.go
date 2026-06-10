package storage

import (
	"testing"

	"vaultdb/internal/metrics"
)

func newAlterTestEngine(t *testing.T) *FileStorageEngine {
	t.Helper()
	s := NewFileStorageEngine(t.TempDir(), metrics.New())
	t.Cleanup(func() { _ = s.Close() })

	if err := s.CreateDatabase("shop"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "products",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "label", Type: "TEXT"},
			{Name: "price", Type: "INT"},
		},
	}
	if err := s.CreateTable("shop", schema); err != nil {
		t.Fatal(err)
	}
	rows := []Row{
		{int64(1), "apple", int64(10)},
		{int64(2), "pear", int64(30)},
		{int64(3), "plum", int64(30)},
	}
	if _, err := s.InsertRows("shop", "products", rows); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDropColumnShiftsIndex(t *testing.T) {
	s := newAlterTestEngine(t)
	if err := s.CreateIndex("shop", "products", "idx_price", "price"); err != nil {
		t.Fatal(err)
	}

	// Dropping a column before "price" shifts its position from 2 to 1.
	if err := s.AlterTableDropColumn("shop", "products", "label"); err != nil {
		t.Fatal(err)
	}

	positions, ok := s.IndexLookup("shop", "products", "price", "30")
	if !ok {
		t.Fatal("index lookup missed after DROP COLUMN")
	}
	rows, err := s.ReadRowsByPositions("shop", "products", positions)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows for price=30, want 2: %v", len(rows), rows)
	}
	for _, row := range rows {
		if v, _ := toInt64(row[1]); v != 30 {
			t.Fatalf("index returned row with price %v, want 30 (stale ColIndex)", row[1])
		}
	}
}

func TestDropIndexedColumnRemovesIndex(t *testing.T) {
	s := newAlterTestEngine(t)
	if err := s.CreateIndex("shop", "products", "idx_label", "label"); err != nil {
		t.Fatal(err)
	}
	if err := s.AlterTableDropColumn("shop", "products", "label"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindIndexForColumn("shop", "products", "label"); ok {
		t.Fatal("index on dropped column still registered")
	}
}

func TestRenameColumnRenamesIndex(t *testing.T) {
	s := newAlterTestEngine(t)
	if err := s.CreateIndex("shop", "products", "idx_price", "price"); err != nil {
		t.Fatal(err)
	}
	if err := s.AlterTableRenameColumn("shop", "products", "price", "cost"); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.FindIndexForColumn("shop", "products", "cost"); !ok {
		t.Fatal("index did not follow the renamed column")
	}
	if positions, ok := s.IndexLookup("shop", "products", "cost", "10"); !ok || len(positions) != 1 {
		t.Fatalf("lookup on renamed column: ok=%v positions=%v, want 1 hit", ok, positions)
	}
}
