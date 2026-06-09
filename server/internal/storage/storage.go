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
	VarcharLen int    `json:"varchar_len,omitempty"`
	IsComputed bool   `json:"is_computed,omitempty"`
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

type VacuumStats struct {
	TableName      string
	RowsBefore     int   // строк до vacuum (включая все версии)
	RowsAfter      int   // строк после vacuum (только актуальные)
	ReclaimedRows  int   // удалено устаревших версий
	FileSizeBefore int64 // байт до
	FileSizeAfter  int64 // байт после
	DurationMs     float64
}

type TableVersionStats struct {
	TotalRows int
	DeadRows  int
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

	AlterTableAddColumn(dbName, tableName string, col ColumnSchema, defaultVal Value) error
	AlterTableDropColumn(dbName, tableName string, colName string) error
	AlterTableRenameColumn(dbName, tableName, oldName, newName string) error
	AlterTableRenameTable(dbName, oldName, newName string) error

	InsertRows(dbName, tableName string, rows []Row) (int, error)
	SelectRows(dbName, tableName string) ([]Row, error)
	ReadCurrentRows(dbName, tableName string) ([]Row, error)
	ReadRowsAsOf(dbName, tableName string, txID uint64) ([]Row, error)
	ReadRowsByPositions(dbName, tableName string, positions []int) ([]Row, error)
	CountRows(dbName, tableName string) (int, error)
	UpdateRows(dbName, tableName string, indices []int, updates map[string]Value) (int, error)
	DeleteRows(dbName, tableName string, indices []int) (int, error)
	TxIDAtTimestamp(dbName, ts string) (uint64, error)
	RowHistory(dbName, tableName string, pkValue interface{}) ([]VersionedRow, error)
	Vacuum(dbName, tableName string) (*VacuumStats, error)
	TableVersionStats(dbName, tableName string) (*TableVersionStats, error)
	TableModifiedSince(db, table string, txID uint64) (bool, error)
	CurrentTxID() uint64

	CreateIndex(dbName, tableName, indexName, column string) error
	DropIndex(dbName, indexName string) error
	ListIndexes(dbName, tableName string) ([]string, error)
	FindIndexForColumn(dbName, tableName, column string) (string, bool)
	IndexLookup(dbName, tableName, column, value string) ([]int, bool)

	FinalCheckpoint() error
	Close() error
}
