package parser

// Statement is the root interface for all SQL statements.
type Statement interface {
	statementNode()
	StatementType() string
}

type AsOfClause struct {
	Timestamp  string
	Version    uint64
	UseVersion bool
}

// DDL.
type CreateDatabaseStatement struct {
	DatabaseName string
}

type DropDatabaseStatement struct {
	DatabaseName string
}

type UseDatabaseStatement struct {
	DatabaseName string
}

type ShowDatabasesStatement struct{}

type ShowTablesStatement struct {
	DatabaseName string // empty means current session database
}

type DescribeTableStatement struct {
	TableName    string
	DatabaseName string // empty means current session database
}

type ColumnDef struct {
	Name       string
	DataType   string // INT, FLOAT, BOOL, TEXT, VARCHAR
	VarcharLen int
	Default    Expression
	Computed   Expression
}

type CreateTableStatement struct {
	TableName   string
	Columns     []ColumnDef
	InferSchema bool
}

type DropTableStatement struct {
	TableName string
}

// DML.
type OrderItem struct {
	Expr      Expression
	Direction string // "ASC" or "DESC"
}

type JoinClause struct {
	Type      string // "INNER", "LEFT", "RIGHT", "FULL", "CROSS"
	TableName string
	Alias     string
	Condition Expression
}

type SelectColumn struct {
	Expr  Expression
	Alias string
}

type SelectStatement struct {
	Columns   []SelectColumn // empty means '*'
	TableName string
	Alias     string
	Joins     []JoinClause
	Where     Expression
	GroupBy   []Expression
	Having    Expression
	OrderBy   []OrderItem
	Limit     int
	HasLimit  bool
	Offset    int
	HasOffset bool
	CountAll  bool // Deprecated: replace with COUNT(*) aggregate function
	AsOf      *AsOfClause
}

// Alter Table actions
type AlterTableAction interface {
	alterTableAction()
}

type AlterAddColumn struct {
	Column ColumnDef
}

type AlterDropColumn struct {
	ColumnName string
}

type AlterRenameColumn struct {
	OldName string
	NewName string
}

type AlterRenameTable struct {
	NewName string
}

type AlterTableStatement struct {
	TableName string
	Action    AlterTableAction
}

func (*AlterAddColumn) alterTableAction()    {}
func (*AlterDropColumn) alterTableAction()   {}
func (*AlterRenameColumn) alterTableAction() {}
func (*AlterRenameTable) alterTableAction()  {}

type ExplainStatement struct {
	Inner   *SelectStatement
	Analyze bool
}

type HistoryStatement struct {
	TableName string
	Key       Expression
}

type InsertStatement struct {
	TableName string
	Columns   []string // empty means all columns in schema order
	Rows      [][]Expression
}

type Assignment struct {
	Column string
	Value  Expression
}

type UpdateStatement struct {
	TableName   string
	Assignments []Assignment
	Where       Expression
}

type DeleteStatement struct {
	TableName string
	Where     Expression
}

type VacuumStatement struct {
	TableName string // empty means vacuum all tables in current DB
	Analyze   bool   // true = VACUUM ANALYZE: show statistics
}

// Index statements
type CreateIndexStatement struct {
	IndexName string
	TableName string
	Column    string
}

type DropIndexStatement struct {
	IndexName string
}

type ShowIndexesStatement struct {
	TableName string
}

// Transaction statements
type BeginStatement struct{}
type CommitStatement struct{}
type RollbackStatement struct{}

// Prepared statements
type PrepareStatement struct {
	Name  string
	Query Statement
}

type ExecuteStatement struct {
	Name   string
	Params []Value
}

type DeallocateStatement struct {
	Name string
}

type SetOperationStatement struct {
	Left  Statement
	Op    string // UNION, UNION ALL, INTERSECT, EXCEPT
	Right Statement
}

type MigrationStatement struct {
	Op   string // CREATE, APPLY, ROLLBACK, PREVIEW
	Name string
	SQL  string
}

type CreatePolicyStatement struct {
	Name      string
	TableName string
	ToUser    string
	Using     Expression
}

type EnableRlsStatement struct {
	TableName string
}

func (*CreateDatabaseStatement) statementNode() {}
func (*DropDatabaseStatement) statementNode()   {}
func (*UseDatabaseStatement) statementNode()    {}
func (*ShowDatabasesStatement) statementNode()  {}
func (*ShowTablesStatement) statementNode()     {}
func (*DescribeTableStatement) statementNode()  {}
func (*CreateTableStatement) statementNode()    {}
func (*DropTableStatement) statementNode()      {}
func (*AlterTableStatement) statementNode()     {}
func (*SelectStatement) statementNode()         {}
func (*ExplainStatement) statementNode()        {}
func (*HistoryStatement) statementNode()        {}
func (*InsertStatement) statementNode()         {}
func (*UpdateStatement) statementNode()         {}
func (*DeleteStatement) statementNode()         {}
func (*VacuumStatement) statementNode()         {}
func (*CreateIndexStatement) statementNode()    {}
func (*DropIndexStatement) statementNode()      {}
func (*ShowIndexesStatement) statementNode()    {}
func (*BeginStatement) statementNode()          {}
func (*CommitStatement) statementNode()         {}
func (*RollbackStatement) statementNode()       {}
func (*PrepareStatement) statementNode()        {}
func (*ExecuteStatement) statementNode()        {}
func (*DeallocateStatement) statementNode()     {}
func (*SetOperationStatement) statementNode() {}
func (*MigrationStatement) statementNode()    {}
func (*CreatePolicyStatement) statementNode() {}
func (*EnableRlsStatement) statementNode()    {}

func (*CreateDatabaseStatement) StatementType() string { return "CREATE_DATABASE" }

func (*DropDatabaseStatement) StatementType() string   { return "DROP_DATABASE" }
func (*UseDatabaseStatement) StatementType() string    { return "USE_DATABASE" }
func (*ShowDatabasesStatement) StatementType() string  { return "SHOW_DATABASES" }
func (*ShowTablesStatement) StatementType() string     { return "SHOW_TABLES" }
func (*DescribeTableStatement) StatementType() string  { return "DESCRIBE_TABLE" }
func (*CreateTableStatement) StatementType() string    { return "CREATE_TABLE" }
func (*DropTableStatement) StatementType() string      { return "DROP_TABLE" }
func (*AlterTableStatement) StatementType() string     { return "ALTER_TABLE" }
func (*SelectStatement) StatementType() string         { return "SELECT" }
func (*ExplainStatement) StatementType() string        { return "EXPLAIN" }
func (*HistoryStatement) StatementType() string        { return "HISTORY" }
func (*InsertStatement) StatementType() string         { return "INSERT" }
func (*UpdateStatement) StatementType() string         { return "UPDATE" }
func (*DeleteStatement) StatementType() string         { return "DELETE" }
func (*VacuumStatement) StatementType() string         { return "VACUUM" }
func (*CreateIndexStatement) StatementType() string    { return "CREATE_INDEX" }
func (*DropIndexStatement) StatementType() string      { return "DROP_INDEX" }
func (*ShowIndexesStatement) StatementType() string    { return "SHOW_INDEXES" }
func (*BeginStatement) StatementType() string          { return "BEGIN" }
func (*CommitStatement) StatementType() string         { return "COMMIT" }
func (*RollbackStatement) StatementType() string       { return "ROLLBACK" }
func (*PrepareStatement) StatementType() string        { return "PREPARE" }
func (*ExecuteStatement) StatementType() string        { return "EXECUTE" }
func (*DeallocateStatement) StatementType() string     { return "DEALLOCATE" }
func (*SetOperationStatement) StatementType() string   { return "SET_OPERATION" }
func (*MigrationStatement) StatementType() string      { return "MIGRATION" }
func (*CreatePolicyStatement) StatementType() string   { return "CREATE_POLICY" }
func (*EnableRlsStatement) StatementType() string      { return "ENABLE_RLS" }

// Expression is the root interface for all WHERE expressions.
type Expression interface {
	expressionNode()
}

// Value is both a literal expression and a transport type used in INSERT/UPDATE AST nodes.
type Value struct {
	Type    string // int, float, string, bool, null
	IntVal  int64
	FltVal  float64
	StrVal  string
	BoolVal bool
}

// ColumnRef references a table column.
type ColumnRef struct {
	Name string
}

// BinaryExpr represents comparison operators: =, !=, <, >, <=, >=.
type BinaryExpr struct {
	Left     Expression
	Operator string
	Right    Expression
}

type AndExpr struct {
	Left  Expression
	Right Expression
}

type OrExpr struct {
	Left  Expression
	Right Expression
}

type NotExpr struct {
	Expr Expression
}

// ParamRef references a prepared statement parameter ($1, $2, ...).
type ParamRef struct {
	Index int // 1-based
}

type InExpr struct {
	Left  Expression
	Not   bool
	Right []Expression
}

type FunctionCall struct {
	Name string
	Args []Expression
}

type AggregateExpr struct {
	Name     string
	Args     []Expression
	Distinct bool
}

type SubqueryExpr struct {
	Query *SelectStatement
}

type FrameSpec struct {
	Mode      string // ROWS or RANGE
	StartType string // UNBOUNDED PRECEDING, PRECEDING, CURRENT ROW, FOLLOWING, UNBOUNDED FOLLOWING
	StartN    int
	EndType   string
	EndN      int
}

type WindowSpec struct {
	PartitionBy []Expression
	OrderBy     []OrderItem
	Frame       *FrameSpec
}

type WindowFunctionExpr struct {
	FuncName string
	Args     []Expression
	Over     WindowSpec
}

type CastExpr struct {
	Expr       Expression
	TargetType string
}

type CaseWhen struct {
	Condition Expression
	Result    Expression
}

type CaseExpr struct {
	Base  Expression
	Whens []CaseWhen
	Else  Expression
}

type JsonPathExpr struct {
	Left  Expression
	Op    string // -> or ->>
	Path  string
}

func (*WindowFunctionExpr) expressionNode() {}
func (*CastExpr) expressionNode()           {}
func (*CaseExpr) expressionNode()           {}
func (*JsonPathExpr) expressionNode()       {}

func (Value) expressionNode()          {}
func (*ColumnRef) expressionNode()     {}
func (*BinaryExpr) expressionNode()    {}
func (*AndExpr) expressionNode()       {}
func (*OrExpr) expressionNode()        {}
func (*NotExpr) expressionNode()       {}
func (*ParamRef) expressionNode()      {}
func (*InExpr) expressionNode()        {}
func (*FunctionCall) expressionNode()  {}
func (*AggregateExpr) expressionNode() {}
func (*SubqueryExpr) expressionNode()  {}
