package storage

import (
	"testing"

	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

func newTestPartitionEngine(t *testing.T) (*PageStorageEngine, func()) {
	t.Helper()
	dir := t.TempDir()
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	txMgr := txmanager.NewManager()
	e, err := NewPageStorageEngine(dir, w, txMgr)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		e.Close()
		w.Close()
	}
	return e, cleanup
}

func TestRangePartitionRouting(t *testing.T) {
	pt := &PartitionedTable{
		Spec: &PartitionSpec{
			Type:    "RANGE",
			Columns: []string{"order_date"},
			Partitions: []PartitionDef{
				{Name: "p2023", Bound: "2024-01-01"},
				{Name: "p2024", Bound: "2025-01-01"},
				{Name: "p2025", Bound: nil}, // MAXVALUE
			},
		},
		Schema: &TableSchema{
			Name: "orders",
			Columns: []ColumnSchema{
				{Name: "id", Type: "INT"},
				{Name: "order_date", Type: "TEXT"},
				{Name: "amount", Type: "FLOAT"},
			},
		},
		Partitions: []Partition{
			{Name: "p2023", TableName: "orders_p2023", Bound: "2024-01-01"},
			{Name: "p2024", TableName: "orders_p2024", Bound: "2025-01-01"},
			{Name: "p2025", TableName: "orders_p2025", Bound: nil},
		},
	}

	tests := []struct {
		name     string
		row      Row
		expected string
	}{
		{
			name:     "date in 2023",
			row:      Row{int64(1), "2023-06-15", 100.0},
			expected: "orders_p2023",
		},
		{
			name:     "date in 2024",
			row:      Row{int64(2), "2024-06-15", 200.0},
			expected: "orders_p2024",
		},
		{
			name:     "date in 2025",
			row:      Row{int64(3), "2025-06-15", 300.0},
			expected: "orders_p2025",
		},
		{
			name:     "date before range",
			row:      Row{int64(4), "2022-01-01", 50.0},
			expected: "orders_p2023",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := pt.InsertRoute(tt.row)
			if err != nil {
				t.Fatalf("InsertRoute error: %v", err)
			}
			if target != tt.expected {
				t.Errorf("expected partition '%s', got '%s'", tt.expected, target)
			}
		})
	}
}

func TestHashPartitionRouting(t *testing.T) {
	pt := &PartitionedTable{
		Spec: &PartitionSpec{
			Type:    "HASH",
			Columns: []string{"user_id"},
			NumParts: 4,
		},
		Schema: &TableSchema{
			Name: "sessions",
			Columns: []ColumnSchema{
				{Name: "user_id", Type: "INT"},
				{Name: "data", Type: "TEXT"},
			},
		},
		Partitions: []Partition{
			{Name: "p0", TableName: "sessions_p0"},
			{Name: "p1", TableName: "sessions_p1"},
			{Name: "p2", TableName: "sessions_p2"},
			{Name: "p3", TableName: "sessions_p3"},
		},
	}

	// Verify all rows route to valid partitions
	for i := int64(0); i < 100; i++ {
		row := Row{i, "data"}
		target, err := pt.InsertRoute(row)
		if err != nil {
			t.Fatalf("InsertRoute error for user_id=%d: %v", i, err)
		}
		valid := false
		for _, p := range pt.Partitions {
			if target == p.TableName {
				valid = true
				break
			}
		}
		if !valid {
			t.Errorf("user_id=%d routed to invalid partition '%s'", i, target)
		}
	}

	// Verify deterministic routing (same input → same output)
	row := Row{int64(42), "data"}
	target1, _ := pt.InsertRoute(row)
	target2, _ := pt.InsertRoute(row)
	if target1 != target2 {
		t.Errorf("non-deterministic routing: %s != %s", target1, target2)
	}
}

func TestNewPartitionedTable(t *testing.T) {
	schema := &TableSchema{
		Name: "orders",
		PartitionBy: &PartitionSpec{
			Type:    "HASH",
			Columns: []string{"id"},
			NumParts: 3,
		},
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
		},
	}

	pt := NewPartitionedTable(schema)
	if pt == nil {
		t.Fatal("NewPartitionedTable returned nil")
	}
	if len(pt.Partitions) != 3 {
		t.Fatalf("expected 3 partitions, got %d", len(pt.Partitions))
	}
	for i, p := range pt.Partitions {
		expectedName := "orders_p" + string(rune('0'+i))
		if p.TableName != expectedName {
			t.Errorf("partition %d: expected table name '%s', got '%s'", i, expectedName, p.TableName)
		}
	}
}

func TestPartitionInsertAndSelect(t *testing.T) {
	e, cleanup := newTestPartitionEngine(t)
	defer cleanup()

	schema := TableSchema{
		Name:     "orders",
		Database: "testdb",
		PartitionBy: &PartitionSpec{
			Type:    "HASH",
			Columns: []string{"id"},
			NumParts: 2,
		},
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "amount", Type: "FLOAT"},
		},
	}

	// Create parent table
	if err := e.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Create partition tables
	pt := NewPartitionedTable(&schema)
	for _, part := range pt.Partitions {
		partSchema := TableSchema{
			Name:     part.TableName,
			Database: "testdb",
			Columns:  schema.Columns,
		}
		if err := e.CreateTable("testdb", partSchema); err != nil {
			t.Fatal(err)
		}
	}

	// Insert rows and route to partitions
	rows := []Row{
		{int64(1), 100.0},
		{int64(2), 200.0},
		{int64(3), 300.0},
	}

	for _, row := range rows {
		target, err := pt.InsertRoute(row)
		if err != nil {
			t.Fatal(err)
		}
		n, err := e.InsertRows("testdb", target, []Row{row})
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("expected 1 row inserted, got %d", n)
		}
	}

	// Read from all partitions and verify total count
	totalRows := 0
	for _, part := range pt.Partitions {
		partRows, err := e.ReadCurrentRows("testdb", part.TableName)
		if err != nil {
			t.Fatal(err)
		}
		totalRows += len(partRows)
	}

	if totalRows != len(rows) {
		t.Errorf("expected %d total rows across partitions, got %d", len(rows), totalRows)
	}
}

func TestPartitionPruneAll(t *testing.T) {
	pt := &PartitionedTable{
		Spec: &PartitionSpec{
			Type:    "RANGE",
			Columns: []string{"created_at"},
		},
		Partitions: []Partition{
			{Name: "p1", TableName: "t_p1"},
			{Name: "p2", TableName: "t_p2"},
			{Name: "p3", TableName: "t_p3"},
		},
	}

	pruned := pt.PrunePartitions(nil)
	if len(pruned) != 3 {
		t.Errorf("expected 3 partitions returned, got %d", len(pruned))
	}
}
