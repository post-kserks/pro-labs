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
}

type CreateTableStatement struct {
	TableName string
	Columns   []ColumnDef
}

type DropTableStatement struct {
	TableName string
}

// DML.
type SelectStatement struct {
	Columns   []string // empty means '*'
	TableName string
	Where     Expression
	Limit     int
	HasLimit  bool
	CountAll  bool
	AsOf      *AsOfClause
}

type ExplainStatement struct {
	Inner   *SelectStatement
	Analyze bool
}

type HistoryStatement struct {
	TableName string
	Key       Value
}

type InsertStatement struct {
	TableName string
	Columns   []string // empty means all columns in schema order
	Rows      [][]Value
}

type Assignment struct {
	Column string
	Value  Value
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

func (*CreateDatabaseStatement) statementNode() {}
func (*DropDatabaseStatement) statementNode()   {}
func (*UseDatabaseStatement) statementNode()    {}
func (*ShowDatabasesStatement) statementNode()  {}
func (*ShowTablesStatement) statementNode()     {}
func (*DescribeTableStatement) statementNode()  {}
func (*CreateTableStatement) statementNode()    {}
func (*DropTableStatement) statementNode()      {}
func (*SelectStatement) statementNode()         {}
func (*ExplainStatement) statementNode()        {}
func (*HistoryStatement) statementNode()        {}
func (*InsertStatement) statementNode()         {}
func (*UpdateStatement) statementNode()         {}
func (*DeleteStatement) statementNode()         {}

func (*CreateDatabaseStatement) StatementType() string { return "CREATE_DATABASE" }
func (*DropDatabaseStatement) StatementType() string   { return "DROP_DATABASE" }
func (*UseDatabaseStatement) StatementType() string    { return "USE_DATABASE" }
func (*ShowDatabasesStatement) StatementType() string  { return "SHOW_DATABASES" }
func (*ShowTablesStatement) StatementType() string     { return "SHOW_TABLES" }
func (*DescribeTableStatement) StatementType() string  { return "DESCRIBE_TABLE" }
func (*CreateTableStatement) StatementType() string    { return "CREATE_TABLE" }
func (*DropTableStatement) StatementType() string      { return "DROP_TABLE" }
func (*SelectStatement) StatementType() string         { return "SELECT" }
func (*ExplainStatement) StatementType() string        { return "EXPLAIN" }
func (*HistoryStatement) StatementType() string        { return "HISTORY" }
func (*InsertStatement) StatementType() string         { return "INSERT" }
func (*UpdateStatement) StatementType() string         { return "UPDATE" }
func (*DeleteStatement) StatementType() string         { return "DELETE" }

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

func (Value) expressionNode()       {}
func (*ColumnRef) expressionNode()  {}
func (*BinaryExpr) expressionNode() {}
func (*AndExpr) expressionNode()    {}
func (*OrExpr) expressionNode()     {}
func (*NotExpr) expressionNode()    {}
