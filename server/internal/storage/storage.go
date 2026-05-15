package storage

import "time"

// Value is a single cell value in a row.
// Supported runtime types: int64, float64, string, bool, nil.
type Value interface{}

// Row is a single row in table order.
type Row []Value

// ColumnSchema describes one table column.
type ColumnSchema struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	VarcharLen int    `json:"length,omitempty"`
}

// TableSchema describes a table.
type TableSchema struct {
	Name      string         `json:"name"`
	Database  string         `json:"database"`
	Columns   []ColumnSchema `json:"columns"`
	CreatedAt time.Time      `json:"created_at"`
}

// TableInfo is lightweight metadata used by clients that browse a database.
type TableInfo struct {
	Name      string
	RowCount  int
	CreatedAt time.Time
}

type VersionedRow struct {
	CreatedTx uint64
	DeletedTx uint64
	Data      Row
}

type TxLogEntry struct {
	TxID      uint64    `json:"tx_id"`
	Timestamp time.Time `json:"timestamp"`
	Op        string    `json:"op"`
	Table     string    `json:"table"`
}

// StorageEngine is the abstraction used by executor.
type StorageEngine interface {
	CreateDatabase(name string) error
	DropDatabase(name string) error
	DatabaseExists(name string) bool
	ListDatabases() ([]string, error)

	CreateTable(dbName string, schema TableSchema) error
	DropTable(dbName, tableName string) error
	TableExists(dbName, tableName string) bool
	ListTables(dbName string) ([]TableInfo, error)
	GetTableSchema(dbName, tableName string) (*TableSchema, error)

	InsertRows(dbName, tableName string, rows []Row) (int, error)
	SelectRows(dbName, tableName string) ([]Row, error)
	ReadCurrentRows(dbName, tableName string) ([]Row, error)
	ReadRowsAsOf(dbName, tableName string, txID uint64) ([]Row, error)
	CountRows(dbName, tableName string) (int, error)
	UpdateRows(dbName, tableName string, indices []int, updates map[string]Value) (int, error)
	DeleteRows(dbName, tableName string, indices []int) (int, error)
	TxIDAtTimestamp(dbName, ts string) (uint64, error)
	RowHistory(dbName, tableName string, pkValue interface{}) ([]VersionedRow, error)
	FinalCheckpoint() error
	Close() error
}
