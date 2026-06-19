package storage

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

type mockStorageEngine struct {
	listDatabasesFunc      func() ([]string, error)
	listTablesFunc         func(db string) ([]TableInfo, error)
	vacuumFunc             func(db, table string) (*VacuumStats, error)
	tableVersionStatsFunc  func(db, table string) (*TableVersionStats, error)
}

func (m *mockStorageEngine) ListDatabases() ([]string, error) {
	if m.listDatabasesFunc != nil {
		return m.listDatabasesFunc()
	}
	return nil, nil
}

func (m *mockStorageEngine) ListTables(db string) ([]TableInfo, error) {
	if m.listTablesFunc != nil {
		return m.listTablesFunc(db)
	}
	return nil, nil
}

func (m *mockStorageEngine) Vacuum(db, table string) (*VacuumStats, error) {
	if m.vacuumFunc != nil {
		return m.vacuumFunc(db, table)
	}
	return nil, nil
}

func (m *mockStorageEngine) TableVersionStats(db, table string) (*TableVersionStats, error) {
	if m.tableVersionStatsFunc != nil {
		return m.tableVersionStatsFunc(db, table)
	}
	return &TableVersionStats{}, nil
}

func (m *mockStorageEngine) DatabaseExists(name string) bool                  { return false }
func (m *mockStorageEngine) TableExists(dbName, tableName string) bool        { return false }
func (m *mockStorageEngine) GetTableSchema(dbName, tableName string) (*TableSchema, error) {
	return nil, nil
}
func (m *mockStorageEngine) SelectRows(dbName, tableName string) ([]Row, error) {
	return nil, nil
}
func (m *mockStorageEngine) ReadCurrentRows(dbName, tableName string) ([]Row, error) {
	return nil, nil
}
func (m *mockStorageEngine) ReadRowsAsOf(dbName, tableName string, txID uint64) ([]Row, error) {
	return nil, nil
}
func (m *mockStorageEngine) ReadRowsByPositions(dbName, tableName string, positions []int) ([]Row, error) {
	return nil, nil
}
func (m *mockStorageEngine) CountRows(dbName, tableName string) (int, error) {
	return 0, nil
}
func (m *mockStorageEngine) TxIDAtTimestamp(dbName, ts string) (uint64, error) {
	return 0, nil
}
func (m *mockStorageEngine) RowHistory(dbName, tableName string, pkValue interface{}) ([]VersionedRow, error) {
	return nil, nil
}
func (m *mockStorageEngine) TableModifiedSince(db, table string, txID uint64) (bool, error) {
	return false, nil
}
func (m *mockStorageEngine) CurrentTxID() uint64                              { return 0 }
func (m *mockStorageEngine) ListIndexes(dbName, tableName string) ([]string, error) {
	return nil, nil
}
func (m *mockStorageEngine) FindIndexForColumn(dbName, tableName, column string) (string, bool) {
	return "", false
}
func (m *mockStorageEngine) IndexLookup(dbName, tableName, column, value string) ([]int, bool) {
	return nil, false
}
func (m *mockStorageEngine) IndexRangeLookup(dbName, tableName, column, low, high string) ([]int, bool) {
	return nil, false
}
func (m *mockStorageEngine) IndexFTSLookup(dbName, tableName, column, query string) ([]int, bool) {
	return nil, false
}
func (m *mockStorageEngine) CreateTable(dbName string, schema TableSchema) error { return nil }
func (m *mockStorageEngine) DropTable(dbName, tableName string) error            { return nil }
func (m *mockStorageEngine) InsertRows(dbName, tableName string, rows []Row) (int, error) {
	return 0, nil
}
func (m *mockStorageEngine) UpdateRows(dbName, tableName string, indices []int, updates map[string]Value) (int, error) {
	return 0, nil
}
func (m *mockStorageEngine) DeleteRows(dbName, tableName string, indices []int) (int, error) {
	return 0, nil
}
func (m *mockStorageEngine) AlterTableAddColumn(dbName, tableName string, col ColumnSchema, defaultVal Value) error {
	return nil
}
func (m *mockStorageEngine) AlterTableDropColumn(dbName, tableName, colName string) error {
	return nil
}
func (m *mockStorageEngine) AlterTableRenameColumn(dbName, tableName, oldName, newName string) error {
	return nil
}
func (m *mockStorageEngine) AlterTableRenameTable(dbName, oldName, newName string) error {
	return nil
}
func (m *mockStorageEngine) CreateIndex(dbName, tableName, indexName, column string) error {
	return nil
}
func (m *mockStorageEngine) CreateIndexMulti(dbName, tableName, indexName string, columns []string) error {
	return nil
}
func (m *mockStorageEngine) DropIndex(dbName, indexName string) error { return nil }
func (m *mockStorageEngine) CreateDatabase(name string) error         { return nil }
func (m *mockStorageEngine) DropDatabase(name string) error           { return nil }
func (m *mockStorageEngine) FinalCheckpoint() error                   { return nil }
func (m *mockStorageEngine) Close() error                             { return nil }

func TestAutoVacuumTriggersOnHighDeadRatio(t *testing.T) {
	vacuumCalled := false
	mock := &mockStorageEngine{
		listDatabasesFunc: func() ([]string, error) {
			return []string{"testdb"}, nil
		},
		listTablesFunc: func(db string) ([]TableInfo, error) {
			return []TableInfo{{Name: "users", RowCount: 100}}, nil
		},
		tableVersionStatsFunc: func(db, table string) (*TableVersionStats, error) {
			return &TableVersionStats{TotalRows: 100, DeadRows: 30}, nil
		},
		vacuumFunc: func(db, table string) (*VacuumStats, error) {
			vacuumCalled = true
			return &VacuumStats{TableName: table, RowsBefore: 100, RowsAfter: 70, ReclaimedRows: 30}, nil
		},
	}

	logger := slog.Default()
	av := NewAutoVacuum(mock, 0.2, 50*time.Millisecond, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	av.Run(ctx)

	if !vacuumCalled {
		t.Error("expected vacuum to be called when dead ratio (30%) exceeds threshold (20%)")
	}
}

func TestAutoVacuumSkipsCleanTables(t *testing.T) {
	vacuumCalled := false
	mock := &mockStorageEngine{
		listDatabasesFunc: func() ([]string, error) {
			return []string{"testdb"}, nil
		},
		listTablesFunc: func(db string) ([]TableInfo, error) {
			return []TableInfo{{Name: "users", RowCount: 100}}, nil
		},
		tableVersionStatsFunc: func(db, table string) (*TableVersionStats, error) {
			return &TableVersionStats{TotalRows: 100, DeadRows: 5}, nil
		},
		vacuumFunc: func(db, table string) (*VacuumStats, error) {
			vacuumCalled = true
			return &VacuumStats{TableName: table}, nil
		},
	}

	logger := slog.Default()
	av := NewAutoVacuum(mock, 0.2, 50*time.Millisecond, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	av.Run(ctx)

	if vacuumCalled {
		t.Error("vacuum should not be called when dead ratio (5%) is below threshold (20%)")
	}
}

func TestAutoVacuumSkipsEmptyTables(t *testing.T) {
	vacuumCalled := false
	mock := &mockStorageEngine{
		listDatabasesFunc: func() ([]string, error) {
			return []string{"testdb"}, nil
		},
		listTablesFunc: func(db string) ([]TableInfo, error) {
			return []TableInfo{{Name: "empty", RowCount: 0}}, nil
		},
		tableVersionStatsFunc: func(db, table string) (*TableVersionStats, error) {
			return &TableVersionStats{TotalRows: 0, DeadRows: 0}, nil
		},
		vacuumFunc: func(db, table string) (*VacuumStats, error) {
			vacuumCalled = true
			return &VacuumStats{}, nil
		},
	}

	logger := slog.Default()
	av := NewAutoVacuum(mock, 0.2, 50*time.Millisecond, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	av.Run(ctx)

	if vacuumCalled {
		t.Error("vacuum should not be called for empty tables")
	}
}

func TestAutoVacuumHandlesListDatabasesError(t *testing.T) {
	mock := &mockStorageEngine{
		listDatabasesFunc: func() ([]string, error) {
			return nil, context.Canceled
		},
	}

	logger := slog.Default()
	av := NewAutoVacuum(mock, 0.2, 50*time.Millisecond, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	av.Run(ctx)
}

func TestAutoVacuumHandlesListTablesError(t *testing.T) {
	mock := &mockStorageEngine{
		listDatabasesFunc: func() ([]string, error) {
			return []string{"testdb"}, nil
		},
		listTablesFunc: func(db string) ([]TableInfo, error) {
			return nil, context.Canceled
		},
	}

	logger := slog.Default()
	av := NewAutoVacuum(mock, 0.2, 50*time.Millisecond, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	av.Run(ctx)
}

func TestAutoVacuumDefaults(t *testing.T) {
	av := NewAutoVacuum(&mockStorageEngine{}, 0, 0, nil)

	if av.threshold != 0.2 {
		t.Errorf("expected default threshold 0.2, got %f", av.threshold)
	}
	if av.interval != time.Minute {
		t.Errorf("expected default interval 1m, got %v", av.interval)
	}
	if av.logger == nil {
		t.Error("expected non-nil default logger")
	}
}

func TestAutoVacuumContextCancellation(t *testing.T) {
	calls := 0
	mock := &mockStorageEngine{
		listDatabasesFunc: func() ([]string, error) {
			calls++
			return []string{"db"}, nil
		},
		listTablesFunc: func(db string) ([]TableInfo, error) {
			return nil, nil
		},
	}

	logger := slog.Default()
	av := NewAutoVacuum(mock, 0.2, 10*time.Millisecond, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		av.Run(ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Run did not exit after context cancellation")
	}

	if calls == 0 {
		t.Error("expected at least one checkAndVacuum call before cancellation")
	}
}
