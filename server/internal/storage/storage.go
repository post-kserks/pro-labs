package storage

import "time"

// Value is a single cell value in a row.
// Supported runtime types: int64, float64, string, bool, nil.
type Value interface{}

// Row is a single row in table order.
type Row []Value

// ColumnSchema describes one table column.
type ColumnSchema struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	VarcharLen    int      `json:"varchar_len,omitempty"`
	IsComputed    bool     `json:"is_computed,omitempty"`
	EnumValues    []string `json:"enum_values,omitempty"`
	NotNull       bool     `json:"not_null,omitempty"`
	PrimaryKey    bool     `json:"primary_key,omitempty"`
	AutoIncrement bool     `json:"auto_increment,omitempty"`
}

// TableSchema describes a table.
// TableConstraint represents a constraint on a table.
type TableConstraint struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"` // "UNIQUE", "CHECK", "FOREIGN_KEY"
	Columns         []string `json:"columns"`
	Expr            string   `json:"expr,omitempty"`              // for CHECK constraints
	RefTable        string   `json:"ref_table,omitempty"`         // for FOREIGN_KEY
	RefCols         []string `json:"ref_cols,omitempty"`          // for FOREIGN_KEY
	OnDeleteCascade bool     `json:"on_delete_cascade,omitempty"` // for FOREIGN_KEY
}

type TableSchema struct {
	Name        string            `json:"name"`
	Database    string            `json:"database"`
	Columns     []ColumnSchema    `json:"columns"`
	Constraints []TableConstraint `json:"constraints,omitempty"`
	RLSEnabled  bool              `json:"rls_enabled,omitempty"`
	Policies    []RLSPolicy       `json:"policies,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

// RLSPolicy stores a row-level security policy for a table.
type RLSPolicy struct {
	Name      string `json:"name"`
	ToUser    string `json:"to_user"`
	UsingExpr string `json:"using_expr"`
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

// ReadOnlyEngine defines read-only database operations.
type ReadOnlyEngine interface {
	DatabaseExists(name string) bool
	ListDatabases() ([]string, error)
	TableExists(dbName, tableName string) bool
	ListTables(dbName string) ([]TableInfo, error)
	GetTableSchema(dbName, tableName string) (*TableSchema, error)
	SelectRows(dbName, tableName string) ([]Row, error)
	ReadCurrentRows(dbName, tableName string) ([]Row, error)
	ReadRowsAsOf(dbName, tableName string, txID uint64) ([]Row, error)
	ReadRowsByPositions(dbName, tableName string, positions []int) ([]Row, error)
	CountRows(dbName, tableName string) (int, error)
	TxIDAtTimestamp(dbName, ts string) (uint64, error)
	RowHistory(dbName, tableName string, pkValue interface{}) ([]VersionedRow, error)
	TableVersionStats(dbName, tableName string) (*TableVersionStats, error)
	TableModifiedSince(db, table string, txID uint64) (bool, error)
	CurrentTxID() uint64
	ListIndexes(dbName, tableName string) ([]string, error)
	FindIndexForColumn(dbName, tableName, column string) (string, bool)
	IndexLookup(dbName, tableName, column, value string) ([]int, bool)
	IndexRangeLookup(dbName, tableName, column, low, high string) ([]int, bool)
	IndexFTSLookup(dbName, tableName, column, query string) ([]int, bool)
	ReadSampleRows(dbName, tableName string, limit int) ([]Row, error)
}

// WriteEngine defines mutating database operations.
type WriteEngine interface {
	CreateTable(dbName string, schema TableSchema) error
	DropTable(dbName, tableName string) error
	InsertRows(dbName, tableName string, rows []Row) (int, error)
	UpdateRows(dbName, tableName string, indices []int, updates map[string]Value) (int, error)
	UpdateRowsDirect(dbName, tableName string, indices []int, newValues []Row) (int, error)
	DeleteRows(dbName, tableName string, indices []int) (int, error)
	TruncateTable(dbName, tableName string) error
	Vacuum(dbName, tableName string) (*VacuumStats, error)
	AlterTableAddColumn(dbName, tableName string, col ColumnSchema, defaultVal Value) error
	AlterTableDropColumn(dbName, tableName string, colName string) error
	AlterTableRenameColumn(dbName, tableName, oldName, newName string) error
	SetTableRLS(dbName, tableName string, enabled bool) error
	AddPolicy(dbName, tableName string, policy RLSPolicy) error
	AlterTableRenameTable(dbName, oldName, newName string) error
	CreateIndex(dbName, tableName, indexName, column string) error
	CreateIndexMulti(dbName, tableName, indexName string, columns []string) error
	DropIndex(dbName, indexName string) error
}

// AdminEngine defines lifecycle and administrative operations.
type AdminEngine interface {
	CreateDatabase(name string) error
	DropDatabase(name string) error
	FinalCheckpoint() error
	Close() error
}

// StorageEngine is the full abstraction used by executor.
// It composes ReadOnlyEngine, WriteEngine, and AdminEngine for backward compatibility.
type StorageEngine interface {
	ReadOnlyEngine
	WriteEngine
	AdminEngine
}
