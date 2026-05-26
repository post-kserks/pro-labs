package parser

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/lexer"
)

// Parse parses one SQL statement terminated by ';'.
func Parse(sql string) (Statement, error) {
	if strings.TrimSpace(sql) == "" {
		return nil, fmt.Errorf("syntax error: empty query")
	}

	l := lexer.New(sql)
	tokens := make([]lexer.Token, 0, 64)
	for {
		tok := l.NextToken()
		if tok.Type == lexer.TOKEN_ILLEGAL {
			return nil, fmt.Errorf("syntax error at line %d, col %d: illegal token '%s'", tok.Line, tok.Col, tok.Literal)
		}
		tokens = append(tokens, tok)
		if tok.Type == lexer.TOKEN_EOF {
			break
		}
	}

	p := &sqlParser{tokens: tokens}
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	if p.current().Type != lexer.TOKEN_SEMICOLON {
		if p.current().Type == lexer.TOKEN_EOF {
			return nil, fmt.Errorf("syntax error: unexpected end of input, expected ';'")
		}
		return nil, p.expectedError("';'", p.current())
	}
	p.advance()

	if p.current().Type != lexer.TOKEN_EOF {
		return nil, p.syntaxError(p.current(), "unexpected token after ';'")
	}

	return stmt, nil
}

type sqlParser struct {
	tokens []lexer.Token
	pos    int
}

func (p *sqlParser) parseStatement() (Statement, error) {
	switch p.current().Type {
	case lexer.TOKEN_CREATE:
		return p.parseCreate()
	case lexer.TOKEN_DROP:
		return p.parseDrop()
	case lexer.TOKEN_USE:
		return p.parseUse()
	case lexer.TOKEN_SHOW:
		return p.parseShow()
	case lexer.TOKEN_DESCRIBE:
		return p.parseDescribe()
	case lexer.TOKEN_EXPLAIN:
		return p.parseExplain()
	case lexer.TOKEN_HISTORY:
		return p.parseHistory()
	case lexer.TOKEN_SELECT:
		return p.parseSelect()
	case lexer.TOKEN_INSERT:
		return p.parseInsert()
	case lexer.TOKEN_UPDATE:
		return p.parseUpdate()
	case lexer.TOKEN_DELETE:
		return p.parseDelete()
	case lexer.TOKEN_VACUUM:
		return p.parseVacuum()
	case lexer.TOKEN_BEGIN:
		return p.parseBegin()
	case lexer.TOKEN_COMMIT:
		return p.parseCommit()
	case lexer.TOKEN_ROLLBACK:
		return p.parseRollback()
	case lexer.TOKEN_PREPARE:
		return p.parsePrepare()
	case lexer.TOKEN_EXECUTE:
		return p.parseExecute()
	case lexer.TOKEN_DEALLOCATE:
		return p.parseDeallocate()
	default:
		return nil, p.expectedError("a statement", p.current())
	}
}

func (p *sqlParser) parseExplain() (Statement, error) {
	p.advance() // EXPLAIN

	analyze := false
	if p.current().Type == lexer.TOKEN_ANALYZE {
		analyze = true
		p.advance()
	}

	stmt, err := p.parseSelect()
	if err != nil {
		return nil, err
	}

	selectStmt, ok := stmt.(*SelectStatement)
	if !ok {
		return nil, p.expectedError("SELECT statement after EXPLAIN", p.current())
	}

	return &ExplainStatement{
		Inner:   selectStmt,
		Analyze: analyze,
	}, nil
}

func (p *sqlParser) parseVacuum() (Statement, error) {
	p.advance() // VACUUM

	analyze := false
	if p.current().Type == lexer.TOKEN_ANALYZE {
		analyze = true
		p.advance()
	}

	tableName := ""
	if p.current().Type == lexer.TOKEN_IDENT {
		name, err := p.consumeIdent("table name")
		if err != nil {
			return nil, err
		}
		tableName = name
	}

	return &VacuumStatement{
		TableName: tableName,
		Analyze:   analyze,
	}, nil
}

func (p *sqlParser) parseBegin() (Statement, error) {
	p.advance()
	return &BeginStatement{}, nil
}

func (p *sqlParser) parseCommit() (Statement, error) {
	p.advance()
	return &CommitStatement{}, nil
}

func (p *sqlParser) parseRollback() (Statement, error) {
	p.advance()
	return &RollbackStatement{}, nil
}

func (p *sqlParser) parsePrepare() (Statement, error) {
	p.advance() // PREPARE

	name, err := p.consumeIdent("statement name")
	if err != nil {
		return nil, err
	}

	if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
		return nil, err
	}

	query, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	return &PrepareStatement{
		Name:  name,
		Query: query,
	}, nil
}

func (p *sqlParser) parseExecute() (Statement, error) {
	p.advance() // EXECUTE

	name, err := p.consumeIdent("statement name")
	if err != nil {
		return nil, err
	}

	params := make([]Value, 0)
	if p.current().Type == lexer.TOKEN_LPAREN {
		p.advance()
		if p.current().Type != lexer.TOKEN_RPAREN {
			for {
				val, err := p.parseLiteralValue()
				if err != nil {
					return nil, err
				}
				params = append(params, val)
				if p.current().Type == lexer.TOKEN_COMMA {
					p.advance()
					continue
				}
				break
			}
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
	}

	return &ExecuteStatement{
		Name:   name,
		Params: params,
	}, nil
}

func (p *sqlParser) parseDeallocate() (Statement, error) {
	p.advance() // DEALLOCATE
	name, err := p.consumeIdent("statement name")
	if err != nil {
		return nil, err
	}
	return &DeallocateStatement{Name: name}, nil
}

func (p *sqlParser) parseHistory() (Statement, error) {
	p.advance() // HISTORY

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_KEY, "KEY"); err != nil {
		return nil, err
	}

	key, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	return &HistoryStatement{
		TableName: tableName,
		Key:       key,
	}, nil
}

func (p *sqlParser) parseShow() (Statement, error) {
	p.advance() // SHOW

	switch p.current().Type {
	case lexer.TOKEN_DATABASES:
		p.advance()
		return &ShowDatabasesStatement{}, nil
	case lexer.TOKEN_TABLES:
		p.advance()
		stmt := &ShowTablesStatement{}
		if p.current().Type == lexer.TOKEN_FROM {
			p.advance()
			dbName, err := p.consumeIdent("database name")
			if err != nil {
				return nil, err
			}
			stmt.DatabaseName = dbName
		}
		return stmt, nil
	case lexer.TOKEN_INDEXES:
		p.advance()
		if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
			return nil, err
		}
		tableName, err := p.consumeIdent("table name")
		if err != nil {
			return nil, err
		}
		return &ShowIndexesStatement{TableName: tableName}, nil
	default:
		return nil, p.expectedError("DATABASES, TABLES or INDEXES", p.current())
	}
}

func (p *sqlParser) parseDescribe() (Statement, error) {
	p.advance() // DESCRIBE

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	stmt := &DescribeTableStatement{TableName: tableName}
	if p.current().Type == lexer.TOKEN_FROM {
		p.advance()
		dbName, err := p.consumeIdent("database name")
		if err != nil {
			return nil, err
		}
		stmt.DatabaseName = dbName
	}
	return stmt, nil
}

func (p *sqlParser) parseCreate() (Statement, error) {
	p.advance() // CREATE
	switch p.current().Type {
	case lexer.TOKEN_DATABASE:
		p.advance()
		name, err := p.consumeIdent("database name")
		if err != nil {
			return nil, err
		}
		return &CreateDatabaseStatement{DatabaseName: name}, nil
	case lexer.TOKEN_TABLE:
		return p.parseCreateTable()
	case lexer.TOKEN_INDEX:
		p.advance()
		indexName, err := p.consumeIdent("index name")
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
			return nil, err
		}
		tableName, err := p.consumeIdent("table name")
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		column, err := p.consumeIdent("column name")
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		return &CreateIndexStatement{
			IndexName: indexName,
			TableName: tableName,
			Column:    column,
		}, nil
	default:
		return nil, p.expectedError("DATABASE, TABLE or INDEX", p.current())
	}
}

func (p *sqlParser) parseCreateTable() (Statement, error) {
	p.advance() // TABLE
	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
		return nil, err
	}

	columns := make([]ColumnDef, 0, 8)
	for {
		colName, err := p.consumeIdent("column name")
		if err != nil {
			return nil, err
		}

		dataType, varcharLen, err := p.parseColumnType()
		if err != nil {
			return nil, err
		}

		columns = append(columns, ColumnDef{Name: colName, DataType: dataType, VarcharLen: varcharLen})

		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		if p.current().Type == lexer.TOKEN_RPAREN {
			p.advance()
			break
		}
		return nil, p.expectedError("',' or ')'", p.current())
	}

	if len(columns) == 0 {
		return nil, fmt.Errorf("syntax error: CREATE TABLE requires at least one column")
	}

	return &CreateTableStatement{TableName: tableName, Columns: columns}, nil
}

func (p *sqlParser) parseColumnType() (string, int, error) {
	tok := p.current()
	switch tok.Type {
	case lexer.TOKEN_INT:
		p.advance()
		return "INT", 0, nil
	case lexer.TOKEN_FLOAT_TYPE:
		p.advance()
		return "FLOAT", 0, nil
	case lexer.TOKEN_BOOL:
		p.advance()
		return "BOOL", 0, nil
	case lexer.TOKEN_TEXT:
		p.advance()
		return "TEXT", 0, nil
	case lexer.TOKEN_VARCHAR:
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return "", 0, err
		}
		sizeTok := p.current()
		if sizeTok.Type != lexer.TOKEN_INT_LIT {
			return "", 0, p.expectedError("VARCHAR length", sizeTok)
		}
		size, err := strconv.Atoi(sizeTok.Literal)
		if err != nil || size <= 0 {
			return "", 0, p.syntaxError(sizeTok, "VARCHAR length must be a positive integer")
		}
		p.advance()
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return "", 0, err
		}
		return "VARCHAR", size, nil
	case lexer.TOKEN_IDENT:
		return "", 0, fmt.Errorf("unknown data type '%s' at line %d, col %d", tok.Literal, tok.Line, tok.Col)
	default:
		return "", 0, p.expectedError("data type", tok)
	}
}

func (p *sqlParser) parseDrop() (Statement, error) {
	p.advance() // DROP
	switch p.current().Type {
	case lexer.TOKEN_DATABASE:
		p.advance()
		name, err := p.consumeIdent("database name")
		if err != nil {
			return nil, err
		}
		return &DropDatabaseStatement{DatabaseName: name}, nil
	case lexer.TOKEN_TABLE:
		p.advance()
		name, err := p.consumeIdent("table name")
		if err != nil {
			return nil, err
		}
		return &DropTableStatement{TableName: name}, nil
	case lexer.TOKEN_INDEX:
		p.advance()
		name, err := p.consumeIdent("index name")
		if err != nil {
			return nil, err
		}
		return &DropIndexStatement{IndexName: name}, nil
	default:
		return nil, p.expectedError("DATABASE, TABLE or INDEX", p.current())
	}
}

func (p *sqlParser) parseUse() (Statement, error) {
	p.advance() // USE
	name, err := p.consumeIdent("database name")
	if err != nil {
		return nil, err
	}
	return &UseDatabaseStatement{DatabaseName: name}, nil
}

func (p *sqlParser) parseSelect() (Statement, error) {
	p.advance() // SELECT

	columns := make([]string, 0, 8)
	countAll := false
	if p.current().Type == lexer.TOKEN_IDENT && strings.EqualFold(p.current().Literal, "COUNT") && p.peek().Type == lexer.TOKEN_LPAREN {
		p.advance() // COUNT
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_STAR, "'*'"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		countAll = true
	} else if p.current().Type == lexer.TOKEN_STAR {
		p.advance()
	} else {
		list, err := p.parseIdentifierList("column name")
		if err != nil {
			return nil, err
		}
		columns = list
	}

	if err := p.consume(lexer.TOKEN_FROM, "FROM"); err != nil {
		return nil, err
	}

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	var asOf *AsOfClause
	switch p.current().Type {
	case lexer.TOKEN_AS:
		p.advance() // AS
		if err := p.consume(lexer.TOKEN_OF, "OF"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_TIMESTAMP, "TIMESTAMP"); err != nil {
			return nil, err
		}
		tsToken := p.current()
		if tsToken.Type != lexer.TOKEN_STRING_LIT {
			return nil, p.expectedError("timestamp string literal", tsToken)
		}
		p.advance()
		asOf = &AsOfClause{
			Timestamp:  tsToken.Literal,
			UseVersion: false,
		}
	case lexer.TOKEN_VERSION:
		p.advance()
		verTok := p.current()
		if verTok.Type != lexer.TOKEN_INT_LIT {
			return nil, p.expectedError("VERSION number", verTok)
		}
		ver, err := strconv.ParseUint(verTok.Literal, 10, 64)
		if err != nil {
			return nil, p.syntaxError(verTok, "VERSION must be a positive integer")
		}
		p.advance()
		asOf = &AsOfClause{
			Version:    ver,
			UseVersion: true,
		}
	}

	var where Expression
	if p.current().Type == lexer.TOKEN_WHERE {
		p.advance()
		where, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	stmt := &SelectStatement{
		Columns:   columns,
		TableName: tableName,
		Where:     where,
		CountAll:  countAll,
		AsOf:      asOf,
	}
	if p.current().Type == lexer.TOKEN_LIMIT {
		p.advance()
		limitTok := p.current()
		if limitTok.Type != lexer.TOKEN_INT_LIT {
			return nil, p.expectedError("LIMIT value", limitTok)
		}
		limit, err := strconv.Atoi(limitTok.Literal)
		if err != nil || limit < 0 {
			return nil, p.syntaxError(limitTok, "LIMIT must be a non-negative integer")
		}
		p.advance()
		stmt.Limit = limit
		stmt.HasLimit = true
	}

	return stmt, nil
}

func (p *sqlParser) parseInsert() (Statement, error) {
	p.advance() // INSERT
	if err := p.consume(lexer.TOKEN_INTO, "INTO"); err != nil {
		return nil, err
	}

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	columns := make([]string, 0, 8)
	if p.current().Type == lexer.TOKEN_LPAREN {
		p.advance()
		columns, err = p.parseIdentifierListUntilRParen("column name")
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
	}

	if err := p.consume(lexer.TOKEN_VALUES, "VALUES"); err != nil {
		return nil, err
	}

	rows := make([][]Expression, 0, 4)
	for {
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		row, err := p.parseValueListUntilRParen()
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}

		if p.current().Type != lexer.TOKEN_COMMA {
			break
		}
		p.advance()
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("syntax error: INSERT requires at least one VALUES row")
	}

	return &InsertStatement{TableName: tableName, Columns: columns, Rows: rows}, nil
}

func (p *sqlParser) parseUpdate() (Statement, error) {
	p.advance() // UPDATE

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	if err := p.consume(lexer.TOKEN_SET, "SET"); err != nil {
		return nil, err
	}

	assignments := make([]Assignment, 0, 4)
	for {
		column, err := p.consumeIdent("column name")
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_EQ, "'='"); err != nil {
			return nil, err
		}
		val, err := p.parseExpression()
		if err != nil {
			return nil, err
		}

		assignments = append(assignments, Assignment{Column: column, Value: val})

		if p.current().Type != lexer.TOKEN_COMMA {
			break
		}
		p.advance()
	}

	var where Expression
	if p.current().Type == lexer.TOKEN_WHERE {
		p.advance()
		where, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	return &UpdateStatement{TableName: tableName, Assignments: assignments, Where: where}, nil
}

func (p *sqlParser) parseDelete() (Statement, error) {
	p.advance() // DELETE
	if err := p.consume(lexer.TOKEN_FROM, "FROM"); err != nil {
		return nil, err
	}

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	var where Expression
	if p.current().Type == lexer.TOKEN_WHERE {
		p.advance()
		where, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	return &DeleteStatement{TableName: tableName, Where: where}, nil
}

func (p *sqlParser) parseExpression() (Expression, error) {
	return p.parseOr()
}

func (p *sqlParser) parseOr() (Expression, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.current().Type == lexer.TOKEN_OR {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &OrExpr{Left: left, Right: right}
	}

	return left, nil
}

func (p *sqlParser) parseAnd() (Expression, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.current().Type == lexer.TOKEN_AND {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &AndExpr{Left: left, Right: right}
	}

	return left, nil
}

func (p *sqlParser) parseNot() (Expression, error) {
	if p.current().Type == lexer.TOKEN_NOT {
		p.advance()
		expr, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &NotExpr{Expr: expr}, nil
	}
	return p.parseComparison()
}

func (p *sqlParser) parseComparison() (Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	switch p.current().Type {
	case lexer.TOKEN_EQ, lexer.TOKEN_NEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTE, lexer.TOKEN_GTE:
		op := p.current().Literal
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Left: left, Operator: op, Right: right}, nil
	default:
		return left, nil
	}
}

func (p *sqlParser) parsePrimary() (Expression, error) {
	tok := p.current()
	switch tok.Type {
	case lexer.TOKEN_LPAREN:
		p.advance()
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		return expr, nil
	case lexer.TOKEN_IDENT:
		p.advance()
		return &ColumnRef{Name: tok.Literal}, nil
	case lexer.TOKEN_PARAM:
		p.advance()
		idx, err := strconv.Atoi(tok.Literal)
		if err != nil {
			return nil, p.syntaxError(tok, "invalid parameter index")
		}
		return &ParamRef{Index: idx}, nil
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_FLOAT_LIT, lexer.TOKEN_STRING_LIT, lexer.TOKEN_TRUE, lexer.TOKEN_FALSE, lexer.TOKEN_NULL:
		value, err := tokenToValue(tok)
		if err != nil {
			return nil, err
		}
		p.advance()
		return value, nil
	default:
		return nil, p.expectedError("expression", tok)
	}
}

func (p *sqlParser) parseLiteralValue() (Value, error) {
	value, err := tokenToValue(p.current())
	if err != nil {
		return Value{}, err
	}
	p.advance()
	return value, nil
}

func (p *sqlParser) parseIdentifierList(context string) ([]string, error) {
	items := make([]string, 0, 4)
	for {
		name, err := p.consumeIdent(context)
		if err != nil {
			return nil, err
		}
		items = append(items, name)
		if p.current().Type != lexer.TOKEN_COMMA {
			break
		}
		p.advance()
	}
	return items, nil
}

func (p *sqlParser) parseIdentifierListUntilRParen(context string) ([]string, error) {
	items := make([]string, 0, 4)
	for {
		name, err := p.consumeIdent(context)
		if err != nil {
			return nil, err
		}
		items = append(items, name)

		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		if p.current().Type == lexer.TOKEN_RPAREN {
			break
		}
		return nil, p.expectedError("',' or ')'", p.current())
	}
	return items, nil
}

func (p *sqlParser) parseValueListUntilRParen() ([]Expression, error) {
	items := make([]Expression, 0, 4)
	for {
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		items = append(items, value)

		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		if p.current().Type == lexer.TOKEN_RPAREN {
			break
		}
		return nil, p.expectedError("',' or ')'", p.current())
	}
	return items, nil
}

func tokenToValue(tok lexer.Token) (Value, error) {
	switch tok.Type {
	case lexer.TOKEN_INT_LIT:
		n, err := strconv.ParseInt(tok.Literal, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("invalid INT literal '%s' at line %d, col %d", tok.Literal, tok.Line, tok.Col)
		}
		return Value{Type: "int", IntVal: n}, nil
	case lexer.TOKEN_FLOAT_LIT:
		f, err := strconv.ParseFloat(tok.Literal, 64)
		if err != nil {
			return Value{}, fmt.Errorf("invalid FLOAT literal '%s' at line %d, col %d", tok.Literal, tok.Line, tok.Col)
		}
		return Value{Type: "float", FltVal: f}, nil
	case lexer.TOKEN_STRING_LIT:
		return Value{Type: "string", StrVal: tok.Literal}, nil
	case lexer.TOKEN_TRUE:
		return Value{Type: "bool", BoolVal: true}, nil
	case lexer.TOKEN_FALSE:
		return Value{Type: "bool", BoolVal: false}, nil
	case lexer.TOKEN_NULL:
		return Value{Type: "null"}, nil
	default:
		return Value{}, fmt.Errorf("syntax error at line %d, col %d: expected literal value, got '%s'", tok.Line, tok.Col, tokenDescription(tok))
	}
}

func (p *sqlParser) consume(tokenType lexer.TokenType, expected string) error {
	if p.current().Type != tokenType {
		return p.expectedError(expected, p.current())
	}
	p.advance()
	return nil
}

func (p *sqlParser) consumeIdent(expected string) (string, error) {
	tok := p.current()
	if tok.Type != lexer.TOKEN_IDENT {
		return "", p.expectedError(expected, tok)
	}
	p.advance()
	return tok.Literal, nil
}

func (p *sqlParser) current() lexer.Token {
	if p.pos >= len(p.tokens) {
		return lexer.Token{Type: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos]
}

func (p *sqlParser) peek() lexer.Token {
	if p.pos+1 >= len(p.tokens) {
		return lexer.Token{Type: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos+1]
}

func (p *sqlParser) advance() {
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
}

func (p *sqlParser) expectedError(expected string, got lexer.Token) error {
	if got.Type == lexer.TOKEN_EOF {
		return fmt.Errorf("syntax error: unexpected end of input, expected %s", expected)
	}
	return fmt.Errorf("syntax error at line %d, col %d: expected %s, got '%s'", got.Line, got.Col, expected, tokenDescription(got))
}

func (p *sqlParser) syntaxError(tok lexer.Token, message string) error {
	if tok.Type == lexer.TOKEN_EOF {
		return fmt.Errorf("syntax error: %s", message)
	}
	return fmt.Errorf("syntax error at line %d, col %d: %s", tok.Line, tok.Col, message)
}

func tokenDescription(tok lexer.Token) string {
	if tok.Literal != "" {
		return tok.Literal
	}
	if tok.Type == lexer.TOKEN_EOF {
		return "end of input"
	}
	return tok.Type.String()
}
