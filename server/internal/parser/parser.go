package parser

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/lexer"
)

// Parse parses one SQL statement terminated by ';'.
func Parse(sql string) (Statement, error) {
	const maxInputSize = 10 * 1024 * 1024 // 10MB
	if len(sql) > maxInputSize {
		return nil, fmt.Errorf("query too large (%d bytes, max 10MB)", len(sql))
	}
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
	var stmt Statement
	var err error

	switch p.current().Type {
	case lexer.TOKEN_CREATE:
		if p.peek().Type == lexer.TOKEN_MIGRATION {
			stmt, err = p.parseMigration("CREATE")
		} else if p.peek().Type == lexer.TOKEN_POLICY {
			stmt, err = p.parseCreatePolicy()
		} else {
			stmt, err = p.parseCreate()
		}
	case lexer.TOKEN_ALTER:
		stmt, err = p.parseAlterTable()
	case lexer.TOKEN_DROP:
		stmt, err = p.parseDrop()
	case lexer.TOKEN_USE:
		stmt, err = p.parseUse()
	case lexer.TOKEN_SHOW:
		stmt, err = p.parseShow()
	case lexer.TOKEN_DESCRIBE:
		stmt, err = p.parseDescribe()
	case lexer.TOKEN_EXPLAIN:
		stmt, err = p.parseExplain()
	case lexer.TOKEN_HISTORY:
		stmt, err = p.parseHistory()
	case lexer.TOKEN_SELECT:
		stmt, err = p.parseSelect()
	case lexer.TOKEN_WITH:
		stmt, err = p.parseCTE()
	case lexer.TOKEN_INSERT:
		stmt, err = p.parseInsert()
	case lexer.TOKEN_UPDATE:
		stmt, err = p.parseUpdate()
	case lexer.TOKEN_DELETE:
		stmt, err = p.parseDelete()
	case lexer.TOKEN_MERGE:
		stmt, err = p.parseMerge()
	case lexer.TOKEN_TRUNCATE:
		stmt, err = p.parseTruncate()
	case lexer.TOKEN_VACUUM:
		stmt, err = p.parseVacuum()
	case lexer.TOKEN_BEGIN:
		stmt, err = p.parseBegin()
	case lexer.TOKEN_COMMIT:
		stmt, err = p.parseCommit()
	case lexer.TOKEN_ROLLBACK:
		if p.peek().Type == lexer.TOKEN_TO {
			stmt, err = p.parseRollbackToSavepoint()
		} else if p.peek().Type == lexer.TOKEN_MIGRATION {
			stmt, err = p.parseMigration("ROLLBACK")
		} else {
			stmt, err = p.parseRollback()
		}
	case lexer.TOKEN_SAVEPOINT:
		stmt, err = p.parseSavepoint()
	case lexer.TOKEN_RELEASE:
		stmt, err = p.parseReleaseSavepoint()
	case lexer.TOKEN_PREPARE:
		stmt, err = p.parsePrepare()
	case lexer.TOKEN_EXECUTE:
		stmt, err = p.parseExecute()
	case lexer.TOKEN_DEALLOCATE:
		stmt, err = p.parseDeallocate()
	case lexer.TOKEN_CALL:
		stmt, err = p.parseCall()
	case lexer.TOKEN_ENABLE:
		stmt, err = p.parseEnableRls()
	case lexer.TOKEN_APPLY:
		stmt, err = p.parseMigration("APPLY")
	case lexer.TOKEN_PREVIEW:
		stmt, err = p.parseMigration("PREVIEW")
	default:
		return nil, p.expectedError("a statement", p.current())
	}

	if err != nil {
		return nil, err
	}

	// Check for set operations after SELECT
	if _, ok := stmt.(*SelectStatement); ok {
		return p.parseSetOperation(stmt)
	}

	return stmt, nil
}

func (p *sqlParser) parseCreatePolicy() (Statement, error) {
	p.advance() // CREATE
	p.advance() // POLICY
	name, err := p.consumeIdent("policy name")
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
	if err := p.consume(lexer.TOKEN_FOR, "FOR"); err != nil {
		return nil, err
	}
	p.advance() // ALL or other actions
	if err := p.consume(lexer.TOKEN_TO, "TO"); err != nil {
		return nil, err
	}
	user, err := p.consumeIdent("user name")
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_USING, "USING"); err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
		return nil, err
	}
	using, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
		return nil, err
	}
	return &CreatePolicyStatement{Name: name, TableName: tableName, ToUser: user, Using: using}, nil
}

func (p *sqlParser) parseEnableRls() (Statement, error) {
	p.advance() // ENABLE
	if err := p.consume(lexer.TOKEN_RLS, "RLS"); err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
		return nil, err
	}
	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}
	return &EnableRlsStatement{TableName: tableName}, nil
}

func (p *sqlParser) parseMigration(op string) (Statement, error) {
	p.advance() // CREATE, APPLY, ROLLBACK, or PREVIEW
	if p.current().Type == lexer.TOKEN_MIGRATION {
		p.advance()
	}

	name, err := p.consumeIdent("migration name")
	if err != nil {
		return nil, err
	}

	sql := ""
	if op == "CREATE" && p.current().Type == lexer.TOKEN_LPAREN {
		p.advance()
		tok := p.current()
		if tok.Type != lexer.TOKEN_STRING_LIT {
			return nil, p.expectedError("migration SQL string", tok)
		}
		sql = tok.Literal
		p.advance()
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
	}

	return &MigrationStatement{Op: op, Name: name, SQL: sql}, nil
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

func (p *sqlParser) parseCall() (Statement, error) {
	p.advance() // CALL
	name, err := p.consumeIdent("procedure name")
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
		return nil, err
	}
	var params []Expression
	if p.current().Type != lexer.TOKEN_RPAREN {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		params = append(params, expr)
		for p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			expr, err = p.parseExpression()
			if err != nil {
				return nil, err
			}
			params = append(params, expr)
		}
	}
	if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
		return nil, err
	}
	return &CallProcedureStatement{Name: name, Params: params}, nil
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
	orReplace := false
	if p.current().Type == lexer.TOKEN_OR {
		p.advance() // OR
		if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "REPLACE" {
			p.advance()
			orReplace = true
		}
	}
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
	case lexer.TOKEN_VIEW:
		p.advance() // VIEW
		viewName, err := p.consumeIdent("view name")
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
			return nil, err
		}
		sub, err := p.parseSelect()
		if err != nil {
			return nil, fmt.Errorf("CREATE VIEW: %w", err)
		}
		sel, ok := sub.(*SelectStatement)
		if !ok {
			return nil, fmt.Errorf("CREATE VIEW: expected SELECT statement")
		}
		return &CreateViewStatement{Name: viewName, Query: sel, OrReplace: orReplace}, nil
	case lexer.TOKEN_TRIGGER:
		p.advance() // TRIGGER
		triggerName, err := p.consumeIdent("trigger name")
		if err != nil {
			return nil, err
		}
		timing := "BEFORE"
		if p.current().Type == lexer.TOKEN_BEFORE {
			p.advance()
			timing = "BEFORE"
		} else if p.current().Type == lexer.TOKEN_AFTER {
			p.advance()
			timing = "AFTER"
		}
		event := strings.ToUpper(p.current().Literal)
		if event != "INSERT" && event != "UPDATE" && event != "DELETE" {
			return nil, p.expectedError("INSERT, UPDATE or DELETE", p.current())
		}
		p.advance()
		if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
			return nil, err
		}
		tableName, err := p.consumeIdent("table name")
		if err != nil {
			return nil, err
		}
		body := ""
		if p.current().Type == lexer.TOKEN_BEGIN {
			p.advance()
			for p.current().Type != lexer.TOKEN_END && p.current().Type != lexer.TOKEN_EOF {
				body += p.current().Literal + " "
				p.advance()
			}
			if p.current().Type == lexer.TOKEN_END {
				p.advance()
			}
			body = strings.TrimSpace(body)
		}
		return &CreateTriggerStatement{Name: triggerName, TableName: tableName, Timing: timing, Event: event, Body: body}, nil
	case lexer.TOKEN_FUNCTION:
		p.advance() // FUNCTION
		funcName, err := p.consumeIdent("function name")
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		var params []string
		if p.current().Type != lexer.TOKEN_RPAREN {
			param, err := p.consumeIdent("parameter name")
			if err != nil {
				return nil, err
			}
			params = append(params, param)
			for p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
				param, err = p.consumeIdent("parameter name")
				if err != nil {
					return nil, err
				}
				params = append(params, param)
			}
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		returnType := "TEXT"
		if p.current().Type == lexer.TOKEN_RETURNS {
			p.advance()
			typeTok := p.current()
			returnType = strings.ToUpper(typeTok.Literal)
			p.advance()
		}
		if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
			return nil, err
		}
		body := ""
		if p.current().Type == lexer.TOKEN_STRING_LIT {
			body = p.current().Literal
			p.advance()
		} else {
			if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
				return nil, err
			}
			for p.current().Type != lexer.TOKEN_RPAREN && p.current().Type != lexer.TOKEN_EOF {
				body += p.current().Literal + " "
				p.advance()
			}
			if p.current().Type == lexer.TOKEN_RPAREN {
				p.advance()
			}
			body = strings.TrimSpace(body)
		}
		lang := "SQL"
		if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "LANGUAGE" {
			p.advance()
			lang = strings.ToUpper(p.current().Literal)
			p.advance()
		}
		return &CreateFunctionStatement{Name: funcName, Params: params, ReturnType: returnType, Body: body, Language: lang}, nil
	case lexer.TOKEN_PROCEDURE:
		p.advance() // PROCEDURE
		procName, err := p.consumeIdent("procedure name")
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		var params []string
		if p.current().Type != lexer.TOKEN_RPAREN {
			param, err := p.consumeIdent("parameter name")
			if err != nil {
				return nil, err
			}
			params = append(params, param)
			for p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
				param, err = p.consumeIdent("parameter name")
				if err != nil {
					return nil, err
				}
				params = append(params, param)
			}
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
			return nil, err
		}
		body := ""
		if p.current().Type == lexer.TOKEN_STRING_LIT {
			body = p.current().Literal
			p.advance()
		} else {
			if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
				return nil, err
			}
			for p.current().Type != lexer.TOKEN_RPAREN && p.current().Type != lexer.TOKEN_EOF {
				body += p.current().Literal + " "
				p.advance()
			}
			if p.current().Type == lexer.TOKEN_RPAREN {
				p.advance()
			}
			body = strings.TrimSpace(body)
		}
		lang := "SQL"
		if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "LANGUAGE" {
			p.advance()
			lang = strings.ToUpper(p.current().Literal)
			p.advance()
		}
		return &CreateProcedureStatement{Name: procName, Params: params, Body: body, Language: lang}, nil
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
		return nil, p.expectedError("DATABASE, TABLE, VIEW or INDEX", p.current())
	}
}

func (p *sqlParser) parseCreateTable() (Statement, error) {
	p.advance() // TABLE
	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	if p.current().Type == lexer.TOKEN_INFER {
		p.advance() // INFER
		if err := p.consume(lexer.TOKEN_SCHEMA, "SCHEMA"); err != nil {
			return nil, err
		}
		return &CreateTableStatement{TableName: tableName, InferSchema: true}, nil
	}

	if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
		return nil, err
	}

	columns := make([]ColumnDef, 0, 8)
	for {
		col, err := p.parseColumnDef()
		if err != nil {
			return nil, err
		}
		columns = append(columns, *col)

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

func (p *sqlParser) parseColumnDef() (*ColumnDef, error) {
	colName, err := p.consumeIdent("column name")
	if err != nil {
		return nil, err
	}

	dataType, varcharLen, err := p.parseColumnType()
	if err != nil {
		return nil, err
	}

	col := &ColumnDef{Name: colName, DataType: dataType, VarcharLen: varcharLen}

	if strings.HasPrefix(dataType, "ENUM:") {
		col.EnumValues = strings.Split(strings.TrimPrefix(dataType, "ENUM:"), ",")
		col.DataType = "ENUM"
	}

	if p.current().Type == lexer.TOKEN_DEFAULT {
		p.advance()
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		col.Default = expr
	}

	if p.current().Type == lexer.TOKEN_GENERATED {
		p.advance() // GENERATED
		if err := p.consume(lexer.TOKEN_ALWAYS, "ALWAYS"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		col.Computed = expr
	}

	return col, nil
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
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			lenTok := p.current()
			if lenTok.Type != lexer.TOKEN_INT_LIT {
				return "", 0, p.expectedError("VARCHAR length", lenTok)
			}
			l, err := strconv.Atoi(lenTok.Literal)
			if err != nil {
				return "", 0, fmt.Errorf("invalid VARCHAR length: %w", err)
			}
			p.advance()
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return "", 0, err
			}
			return "VARCHAR", l, nil
		}
		return "VARCHAR", 0, nil

	case lexer.TOKEN_VECTOR:
		p.advance()
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			lenTok := p.current()
			if lenTok.Type != lexer.TOKEN_INT_LIT {
				return "", 0, p.expectedError("VECTOR dimension", lenTok)
			}
			d, err := strconv.Atoi(lenTok.Literal)
			if err != nil {
				return "", 0, fmt.Errorf("invalid VECTOR dimension: %w", err)
			}
			p.advance()
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return "", 0, err
			}
			return "VECTOR", d, nil
		}
		return "VECTOR", 0, nil

	case lexer.TOKEN_DATE:
		p.advance()
		return "DATE", 0, nil
	case lexer.TOKEN_TIME:
		p.advance()
		return "TIME", 0, nil
	case lexer.TOKEN_TIMESTAMP:
		p.advance()
		return "TIMESTAMP", 0, nil
	case lexer.TOKEN_DECIMAL:
		p.advance()
		return "DECIMAL", 0, nil
	case lexer.TOKEN_UUID:
		p.advance()
		return "UUID", 0, nil
	case lexer.TOKEN_INTERVAL:
		p.advance()
		return "INTERVAL", 0, nil
	case lexer.TOKEN_JSONB:
		p.advance()
		return "JSONB", 0, nil
	case lexer.TOKEN_ARRAY:
		p.advance() // ARRAY
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return "", 0, err
		}
		baseType, _, err := p.parseColumnType()
		if err != nil {
			return "", 0, fmt.Errorf("ARRAY element type: %w", err)
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return "", 0, err
		}
		return "ARRAY:" + baseType, 0, nil
	case lexer.TOKEN_ENUM:
		p.advance() // ENUM
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return "", 0, err
		}
		var values []string
		tok := p.current()
		if tok.Type != lexer.TOKEN_STRING_LIT {
			return "", 0, p.expectedError("enum value string", tok)
		}
		values = append(values, tok.Literal)
		p.advance()
		for p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			tok = p.current()
			if tok.Type != lexer.TOKEN_STRING_LIT {
				return "", 0, p.expectedError("enum value string", tok)
			}
			values = append(values, tok.Literal)
			p.advance()
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return "", 0, err
		}
		return "ENUM:" + strings.Join(values, ","), 0, nil
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
	case lexer.TOKEN_VIEW:
		p.advance()
		name, err := p.consumeIdent("view name")
		if err != nil {
			return nil, err
		}
		return &DropViewStatement{Name: name}, nil
	case lexer.TOKEN_TRIGGER:
		p.advance()
		name, err := p.consumeIdent("trigger name")
		if err != nil {
			return nil, err
		}
		return &DropTriggerStatement{Name: name}, nil
	case lexer.TOKEN_FUNCTION:
		p.advance()
		name, err := p.consumeIdent("function name")
		if err != nil {
			return nil, err
		}
		return &DropFunctionStatement{Name: name}, nil
	case lexer.TOKEN_PROCEDURE:
		p.advance()
		name, err := p.consumeIdent("procedure name")
		if err != nil {
			return nil, err
		}
		return &DropProcedureStatement{Name: name}, nil
	case lexer.TOKEN_INDEX:
		p.advance()
		name, err := p.consumeIdent("index name")
		if err != nil {
			return nil, err
		}
		return &DropIndexStatement{IndexName: name}, nil
	default:
		return nil, p.expectedError("DATABASE, TABLE, VIEW or INDEX", p.current())
	}
}

func (p *sqlParser) parseAlterTable() (Statement, error) {
	p.advance() // ALTER
	if err := p.consume(lexer.TOKEN_TABLE, "TABLE"); err != nil {
		return nil, err
	}
	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	var action AlterTableAction
	switch p.current().Type {
	case lexer.TOKEN_ADD:
		p.advance() // ADD
		if p.current().Type == lexer.TOKEN_COLUMN {
			p.advance() // COLUMN
		}
		if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "CONSTRAINT" {
			p.advance() // CONSTRAINT
			constraintName, err := p.consumeIdent("constraint name")
			if err != nil {
				return nil, err
			}
			switch p.current().Type {
			case lexer.TOKEN_IDENT:
				keyword := strings.ToUpper(p.current().Literal)
				if keyword == "UNIQUE" {
					p.advance()
					if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
						return nil, err
					}
					cols, err := p.parseColumnList()
					if err != nil {
						return nil, err
					}
					if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
						return nil, err
					}
					action = &AlterAddConstraint{Name: constraintName, Type: "UNIQUE", Columns: cols}
				} else 			if keyword == "CHECK" {
					p.advance()
					if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
						return nil, err
					}
					expr, err := p.parseExpression()
					if err != nil {
						return nil, err
					}
					if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
						return nil, err
					}
					action = &AlterAddConstraint{Name: constraintName, Type: "CHECK", CheckExpr: fmt.Sprintf("%v", expr)}
				} else if keyword == "FOREIGN" {
					p.advance() // FOREIGN
					if err := p.consume(lexer.TOKEN_KEY, "KEY"); err != nil {
						return nil, err
					}
					if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
						return nil, err
					}
					cols, err := p.parseColumnList()
					if err != nil {
						return nil, err
					}
					if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
						return nil, err
					}
					if err := p.consume(lexer.TOKEN_REFERENCES, "REFERENCES"); err != nil {
						return nil, err
					}
					refTable, err := p.consumeIdent("referenced table")
					if err != nil {
						return nil, err
					}
					if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
						return nil, err
					}
					refCols, err := p.parseColumnList()
					if err != nil {
						return nil, err
					}
					if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
						return nil, err
					}
					action = &AlterAddConstraint{
						Name:      constraintName,
						Type:      "FOREIGN_KEY",
						Columns:   cols,
						RefTable:  refTable,
						RefCols:   refCols,
					}
			} else {
				return nil, p.expectedError("UNIQUE, CHECK, or FOREIGN KEY", p.current())
			}
		default:
			return nil, p.expectedError("UNIQUE, CHECK, or FOREIGN KEY", p.current())
			}
		} else {
			col, err := p.parseColumnDef()
			if err != nil {
				return nil, err
			}
			action = &AlterAddColumn{Column: *col}
		}

	case lexer.TOKEN_DROP:
		p.advance() // DROP
		if p.current().Type == lexer.TOKEN_COLUMN {
			p.advance() // COLUMN
		}
		colName, err := p.consumeIdent("column name")
		if err != nil {
			return nil, err
		}
		action = &AlterDropColumn{ColumnName: colName}

	case lexer.TOKEN_RENAME:
		p.advance() // RENAME
		if p.current().Type == lexer.TOKEN_TO {
			p.advance() // TO
			newName, err := p.consumeIdent("new table name")
			if err != nil {
				return nil, err
			}
			action = &AlterRenameTable{NewName: newName}
		} else {
			if p.current().Type == lexer.TOKEN_COLUMN {
				p.advance() // COLUMN
			}
			oldName, err := p.consumeIdent("old column name")
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_TO, "TO"); err != nil {
				return nil, err
			}
			newName, err := p.consumeIdent("new column name")
			if err != nil {
				return nil, err
			}
			action = &AlterRenameColumn{OldName: oldName, NewName: newName}
		}

	default:
		return nil, p.expectedError("ADD, DROP, or RENAME", p.current())
	}

	return &AlterTableStatement{TableName: tableName, Action: action}, nil
}

func (p *sqlParser) parseUse() (Statement, error) {
	p.advance() // USE
	name, err := p.consumeIdent("database name")
	if err != nil {
		return nil, err
	}
	return &UseDatabaseStatement{DatabaseName: name}, nil
}

func (p *sqlParser) parseCTE() (Statement, error) {
	p.advance() // WITH

	recursive := false
	if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "RECURSIVE" {
		p.advance()
		recursive = true
	}

	ctes := make([]CTEDefinition, 0)

	for {
		// Parse CTE name
		name, err := p.consumeIdent("CTE name")
		if err != nil {
			return nil, err
		}

		// Optional column aliases
		var columns []string
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

		// AS (query)
		if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}

		// Parse the CTE query
		stmt, err := p.parseSelect()
		if err != nil {
			return nil, err
		}

		selectStmt, ok := stmt.(*SelectStatement)
		if !ok {
			return nil, fmt.Errorf("CTE body must be a SELECT statement")
		}

		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}

		ctes = append(ctes, CTEDefinition{
			Name:    name,
			Columns: columns,
			Query:   selectStmt,
		})

		// Check for more CTEs
		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		break
	}

	// Parse the main query after CTEs
	body, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	return &CTEStatement{CTEs: ctes, Body: body, Recursive: recursive}, nil
}

func (p *sqlParser) parseTruncate() (Statement, error) {
	p.advance() // TRUNCATE

	// Optional TABLE keyword
	if p.current().Type == lexer.TOKEN_TABLE {
		p.advance()
	}

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	return &TruncateStatement{TableName: tableName}, nil
}

func (p *sqlParser) parseSavepoint() (Statement, error) {
	p.advance() // SAVEPOINT

	name, err := p.consumeIdent("savepoint name")
	if err != nil {
		return nil, err
	}

	return &SavepointStatement{Name: name}, nil
}

func (p *sqlParser) parseRollbackToSavepoint() (Statement, error) {
	p.advance() // ROLLBACK
	if err := p.consume(lexer.TOKEN_TO, "TO"); err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_SAVEPOINT, "SAVEPOINT"); err != nil {
		return nil, err
	}

	name, err := p.consumeIdent("savepoint name")
	if err != nil {
		return nil, err
	}

	return &RollbackToSavepointStatement{Name: name}, nil
}

func (p *sqlParser) parseReleaseSavepoint() (Statement, error) {
	p.advance() // RELEASE
	if err := p.consume(lexer.TOKEN_SAVEPOINT, "SAVEPOINT"); err != nil {
		return nil, err
	}

	name, err := p.consumeIdent("savepoint name")
	if err != nil {
		return nil, err
	}

	return &ReleaseSavepointStatement{Name: name}, nil
}

func (p *sqlParser) parseMerge() (Statement, error) {
	p.advance() // MERGE

	// INTO target_table
	if err := p.consume(lexer.TOKEN_INTO, "INTO"); err != nil {
		return nil, err
	}
	targetTable, err := p.consumeIdent("target table")
	if err != nil {
		return nil, err
	}

	// USING source_table
	if err := p.consume(lexer.TOKEN_USING, "USING"); err != nil {
		return nil, err
	}
	sourceTable, err := p.consumeIdent("source table")
	if err != nil {
		return nil, err
	}

	// Optional alias
	var alias string
	if p.current().Type == lexer.TOKEN_AS {
		p.advance()
		alias, err = p.consumeIdent("alias")
		if err != nil {
			return nil, err
		}
	}

	// ON condition
	if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
		return nil, err
	}
	onCondition, err := p.parseExpression()
	if err != nil {
		return nil, err
	}

	// WHEN MATCHED THEN UPDATE ...
	var whenMatched *MergeWhenClause
	if p.current().Type == lexer.TOKEN_WHEN {
		p.advance()
		if err := p.consume(lexer.TOKEN_MATCHED, "MATCHED"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_THEN, "THEN"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_UPDATE, "UPDATE"); err != nil {
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
		whenMatched = &MergeWhenClause{Action: "UPDATE", Assignments: assignments}
	}

	// WHEN NOT MATCHED THEN INSERT ...
	var whenNotMatched *MergeWhenClause
	if p.current().Type == lexer.TOKEN_WHEN {
		p.advance()
		if err := p.consume(lexer.TOKEN_NOT, "NOT"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_MATCHED, "MATCHED"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_THEN, "THEN"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_INSERT, "INSERT"); err != nil {
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
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		values, err := p.parseValueListUntilRParen()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}

		whenNotMatched = &MergeWhenClause{Action: "INSERT", Columns: columns, Values: [][]Expression{values}}
	}

	return &MergeStatement{
		TargetTable:    targetTable,
		SourceTable:    sourceTable,
		Alias:          alias,
		OnCondition:    onCondition,
		WhenMatched:    whenMatched,
		WhenNotMatched: whenNotMatched,
	}, nil
}

func (p *sqlParser) parseSelect() (Statement, error) {
	p.advance() // SELECT

	// Check for DISTINCT
	distinct := false
	if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "DISTINCT" {
		distinct = true
		p.advance()
	}

	columns := make([]SelectColumn, 0, 8)

	if p.current().Type == lexer.TOKEN_STAR {
		p.advance() // Consume '*'
	} else {
		for {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}

			alias := ""
			if p.current().Type == lexer.TOKEN_AS {
				p.advance()
				alias, err = p.consumeIdent("alias")
				if err != nil {
					return nil, err
				}
			} else if p.current().Type == lexer.TOKEN_IDENT && !isReservedKeyword(p.current().Literal) {
				alias = p.current().Literal
				p.advance()
			}

			columns = append(columns, SelectColumn{Expr: expr, Alias: alias})

			if p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
				continue
			}
			break
		}
	}

	var tableName string
	var alias string
	var fromSubquery *SelectStatement
	var fromAlias string
	if p.current().Type == lexer.TOKEN_FROM {
		p.advance()
		isLateral := false
		if p.current().Type == lexer.TOKEN_LATERAL {
			p.advance()
			isLateral = true
		}
		if p.current().Type == lexer.TOKEN_LPAREN || isLateral {
			if p.current().Type == lexer.TOKEN_LPAREN {
				p.advance() // consume '('
			}
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, fmt.Errorf("derived table: %w", err)
			}
			sub, ok := stmt.(*SelectStatement)
			if !ok {
				return nil, fmt.Errorf("derived table: expected SELECT statement")
			}
			if !isLateral {
				if err := p.consume(lexer.TOKEN_RPAREN, ")"); err != nil {
					return nil, err
				}
			}
			fromSubquery = sub
			if isLateral {
				sub.IsLateral = true
			}
			if p.current().Type == lexer.TOKEN_AS && p.peek().Type != lexer.TOKEN_OF {
				p.advance()
				fromAlias, err = p.consumeIdent("derived table alias")
				if err != nil {
					return nil, err
				}
			} else if p.current().Type == lexer.TOKEN_IDENT {
				tokType := strings.ToUpper(p.current().Literal)
				if tokType != "WHERE" && tokType != "GROUP" && tokType != "HAVING" && tokType != "ORDER" && tokType != "LIMIT" && tokType != "OFFSET" && tokType != "JOIN" && tokType != "INNER" && tokType != "LEFT" && tokType != "RIGHT" && tokType != "FULL" && tokType != "CROSS" {
					fromAlias = p.current().Literal
					p.advance()
				}
			}
		} else {
			var err error
			tableName, err = p.consumeIdent("table name")
			if err != nil {
				return nil, err
			}

			if p.current().Type == lexer.TOKEN_AS && p.peek().Type != lexer.TOKEN_OF {
				p.advance()
				alias, err = p.consumeIdent("table alias")
				if err != nil {
					return nil, err
				}
			} else if p.current().Type == lexer.TOKEN_IDENT {
				tokType := strings.ToUpper(p.current().Literal)
				if tokType != "AS" && tokType != "VERSION" && tokType != "JOIN" && tokType != "INNER" && tokType != "LEFT" && tokType != "RIGHT" && tokType != "FULL" && tokType != "CROSS" && tokType != "WHERE" && tokType != "GROUP" && tokType != "HAVING" && tokType != "ORDER" && tokType != "LIMIT" && tokType != "OFFSET" {
					alias = p.current().Literal
					p.advance()
				}
			}
		}
	}

	var asOf *AsOfClause
	switch p.current().Type {
	case lexer.TOKEN_AS:
		if p.peek().Type == lexer.TOKEN_OF {
			p.advance() // AS
			p.advance() // OF
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

	// Joins
	var joins []JoinClause
	for {
		tokType := p.current().Type
		if tokType != lexer.TOKEN_JOIN && tokType != lexer.TOKEN_INNER && tokType != lexer.TOKEN_LEFT && tokType != lexer.TOKEN_RIGHT && tokType != lexer.TOKEN_FULL && tokType != lexer.TOKEN_CROSS {
			break
		}

		joinType := "INNER"
		if tokType != lexer.TOKEN_JOIN {
			joinType = p.current().Literal
			p.advance()
			if strings.ToUpper(joinType) != "CROSS" {
				if err := p.consume(lexer.TOKEN_JOIN, "JOIN"); err != nil {
					return nil, err
				}
			}
		} else {
			p.advance() // JOIN
		}

		joinTable, err := p.consumeIdent("join table name")
		if err != nil {
			return nil, err
		}

		joinAlias := ""
		if p.current().Type == lexer.TOKEN_AS {
			p.advance()
			joinAlias, err = p.consumeIdent("join alias")
			if err != nil {
				return nil, err
			}
		} else if p.current().Type == lexer.TOKEN_IDENT {
			if strings.ToUpper(p.current().Literal) != "ON" {
				joinAlias = p.current().Literal
				p.advance()
			}
		}

		var joinCond Expression
		if strings.ToUpper(joinType) != "CROSS" {
			if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
				return nil, err
			}
			joinCond, err = p.parseExpression()
			if err != nil {
				return nil, err
			}
		}

		joins = append(joins, JoinClause{
			Type:      strings.ToUpper(joinType),
			TableName: joinTable,
			Alias:     joinAlias,
			Condition: joinCond,
		})
	}

	var where Expression
	if p.current().Type == lexer.TOKEN_WHERE {
		p.advance()
		var err error
		where, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	var groupBy []Expression
	if p.current().Type == lexer.TOKEN_GROUP {
		p.advance()
		if err := p.consume(lexer.TOKEN_BY, "BY"); err != nil {
			return nil, err
		}
		for {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			groupBy = append(groupBy, expr)
			if p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
				continue
			}
			break
		}
	}

	var having Expression
	if p.current().Type == lexer.TOKEN_HAVING {
		p.advance()
		var err error
		having, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	var orderBy []OrderItem
	if p.current().Type == lexer.TOKEN_ORDER {
		p.advance()
		if err := p.consume(lexer.TOKEN_BY, "BY"); err != nil {
			return nil, err
		}
		for {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			direction := "ASC"
			if p.current().Type == lexer.TOKEN_ASC {
				p.advance()
			} else if p.current().Type == lexer.TOKEN_DESC {
				direction = "DESC"
				p.advance()
			}
			orderBy = append(orderBy, OrderItem{Expr: expr, Direction: direction})
			if p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
				continue
			}
			break
		}
	}

	stmt := &SelectStatement{
		Columns:      columns,
		TableName:    tableName,
		Alias:        alias,
		FromSubquery: fromSubquery,
		FromAlias:    fromAlias,
		Joins:        joins,
		Where:        where,
		GroupBy:      groupBy,
		Having:       having,
		OrderBy:      orderBy,
		CountAll:     false,
		AsOf:         asOf,
		Distinct:     distinct,
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

	if p.current().Type == lexer.TOKEN_OFFSET {
		p.advance()
		offsetTok := p.current()
		if offsetTok.Type != lexer.TOKEN_INT_LIT {
			return nil, p.expectedError("OFFSET value", offsetTok)
		}
		offset, err := strconv.Atoi(offsetTok.Literal)
		if err != nil || offset < 0 {
			return nil, p.syntaxError(offsetTok, "OFFSET must be a non-negative integer")
		}
		p.advance()
		stmt.Offset = offset
		stmt.HasOffset = true
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

	// Check for ON CONFLICT
	var onConflict *OnConflictClause
	if p.current().Type == lexer.TOKEN_ON {
		p.advance()
		if err := p.consume(lexer.TOKEN_CONFLICT, "CONFLICT"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_DO, "DO"); err != nil {
			return nil, err
		}

		onConflict = &OnConflictClause{}

		if p.current().Type == lexer.TOKEN_NOTHING {
			p.advance()
			onConflict.Action = "NOTHING"
		} else if p.current().Type == lexer.TOKEN_UPDATE {
			p.advance()
			onConflict.Action = "UPDATE"

			// Parse SET assignments
			if err := p.consume(lexer.TOKEN_SET, "SET"); err != nil {
				return nil, err
			}
			onConflict.Assignments = make([]Assignment, 0, 4)
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
				onConflict.Assignments = append(onConflict.Assignments, Assignment{
					Column: column,
					Value:  val,
				})

				if p.current().Type != lexer.TOKEN_COMMA {
					break
				}
				p.advance()
			}
		}
	}

	// Check for RETURNING
	var returning []SelectColumn
	if p.current().Type == lexer.TOKEN_RETURNING {
		p.advance()
		returning, err = p.parseSelectColumns()
		if err != nil {
			return nil, err
		}
	}

	return &InsertStatement{TableName: tableName, Columns: columns, Rows: rows, OnConflict: onConflict, Returning: returning}, nil
}

func (p *sqlParser) parseUpdate() (Statement, error) {
	p.advance() // UPDATE

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	// Check for FROM table
	var fromTable string
	var fromAlias string
	if p.current().Type == lexer.TOKEN_FROM {
		p.advance()
		fromTable, err = p.consumeIdent("table name")
		if err != nil {
			return nil, err
		}
		// Optional alias
		if p.current().Type == lexer.TOKEN_IDENT && !isReservedKeyword(p.current().Literal) {
			fromAlias = p.current().Literal
			p.advance()
		}
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

	// Check for RETURNING
	var returning []SelectColumn
	if p.current().Type == lexer.TOKEN_RETURNING {
		p.advance()
		returning, err = p.parseSelectColumns()
		if err != nil {
			return nil, err
		}
	}

	return &UpdateStatement{
		TableName:   tableName,
		Assignments: assignments,
		Where:       where,
		Returning:   returning,
		FromTable:   fromTable,
		FromAlias:   fromAlias,
	}, nil
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

	// Check for RETURNING
	var returning []SelectColumn
	if p.current().Type == lexer.TOKEN_RETURNING {
		p.advance()
		returning, err = p.parseSelectColumns()
		if err != nil {
			return nil, err
		}
	}

	return &DeleteStatement{TableName: tableName, Where: where, Returning: returning}, nil
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
	left, err := p.parseAddition()
	if err != nil {
		return nil, err
	}

	switch p.current().Type {
	case lexer.TOKEN_EQ, lexer.TOKEN_NEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTE, lexer.TOKEN_GTE, lexer.TOKEN_SEMANTIC_MATCH, lexer.TOKEN_FTS_MATCH, lexer.TOKEN_JSON_CONTAINS, lexer.TOKEN_JSON_CONTAINED_BY, lexer.TOKEN_JSON_HAS_KEY, lexer.TOKEN_JSON_MERGE, lexer.TOKEN_FULLTEXT_MATCH:
		op := p.current().Literal
		p.advance()

		if p.current().Type == lexer.TOKEN_ALL || p.current().Type == lexer.TOKEN_ANY || p.current().Type == lexer.TOKEN_SOME {
			quantifier := strings.ToUpper(p.current().Literal)
			p.advance()
			if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
				return nil, err
			}
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				return nil, fmt.Errorf("expected SELECT statement in %s subquery", quantifier)
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}
			return &ComparisonSubqueryExpr{Left: left, Operator: op, Quantifier: quantifier, Subquery: sel}, nil
		}

		right, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Left: left, Operator: op, Right: right}, nil
	case lexer.TOKEN_LIKE:
		p.advance()
		right, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Left: left, Operator: "LIKE", Right: right}, nil
	case lexer.TOKEN_IN:
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}

		var list []Expression
		if p.current().Type == lexer.TOKEN_SELECT {
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if selectStmt, ok := stmt.(*SelectStatement); ok {
				list = []Expression{&SubqueryExpr{Query: selectStmt}}
			} else {
				return nil, fmt.Errorf("expected SELECT statement in subquery")
			}
		} else {
			var err error
			list, err = p.parseValueListUntilRParen()
			if err != nil {
				return nil, err
			}
		}

		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		return &InExpr{Left: left, Not: false, Right: list}, nil
	case lexer.TOKEN_NOT:
		if p.peek().Type == lexer.TOKEN_BETWEEN {
			p.advance() // NOT
			p.advance() // BETWEEN
			lower, err := p.parseAddition()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_AND, "AND"); err != nil {
				return nil, err
			}
			upper, err := p.parseAddition()
			if err != nil {
				return nil, err
			}
			return &BetweenExpr{Expr: left, Lower: lower, Upper: upper, Not: true}, nil
		}
		if p.peek().Type == lexer.TOKEN_IN {
			p.advance() // NOT
			p.advance() // IN
			if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
				return nil, err
			}
			list, err := p.parseValueListUntilRParen()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}
			return &InExpr{Left: left, Not: true, Right: list}, nil
		}
		if p.peek().Type == lexer.TOKEN_LIKE {
			p.advance() // NOT
			p.advance() // LIKE
			right, err := p.parseAddition()
			if err != nil {
				return nil, err
			}
			return &NotExpr{Expr: &BinaryExpr{Left: left, Operator: "LIKE", Right: right}}, nil
		}
		if p.peek().Type == lexer.TOKEN_EXISTS {
			p.advance() // NOT
			p.advance() // EXISTS
			if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
				return nil, err
			}
			if p.current().Type != lexer.TOKEN_SELECT {
				return nil, fmt.Errorf("expected SELECT after NOT EXISTS (")
			}
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}
			selectStmt, ok := stmt.(*SelectStatement)
			if !ok {
				return nil, fmt.Errorf("expected SELECT statement in NOT EXISTS")
			}
			return &ExistsExpr{Select: selectStmt, Not: true}, nil
		}
		return left, nil
	case lexer.TOKEN_IS:
		p.advance()
		if p.current().Type == lexer.TOKEN_NULL {
			p.advance()
			return &BinaryExpr{Left: left, Operator: "IS", Right: Value{Type: "null"}}, nil
		} else if p.current().Type == lexer.TOKEN_NOT && p.peek().Type == lexer.TOKEN_NULL {
			p.advance()
			p.advance()
			return &BinaryExpr{Left: left, Operator: "IS NOT", Right: Value{Type: "null"}}, nil
		}
		return left, nil
	case lexer.TOKEN_BETWEEN:
		p.advance()
		lower, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_AND, "AND"); err != nil {
			return nil, err
		}
		upper, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		return &BetweenExpr{Expr: left, Lower: lower, Upper: upper, Not: false}, nil
	default:
		return left, nil
	}
}

func (p *sqlParser) parseAddition() (Expression, error) {
	left, err := p.parseMultiplication()
	if err != nil {
		return nil, err
	}

	for p.current().Type == lexer.TOKEN_PLUS || p.current().Type == lexer.TOKEN_MINUS {
		op := p.current().Literal
		p.advance()
		right, err := p.parseMultiplication()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Operator: op, Right: right}
	}

	return left, nil
}

func (p *sqlParser) parseMultiplication() (Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for p.current().Type == lexer.TOKEN_STAR || p.current().Type == lexer.TOKEN_SLASH {
		op := p.current().Literal
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Operator: op, Right: right}
	}

	return left, nil
}

func (p *sqlParser) parsePrimary() (Expression, error) {
	tok := p.current()
	switch tok.Type {
	case lexer.TOKEN_LPAREN:
		p.advance()
		if p.current().Type == lexer.TOKEN_SELECT {
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}
			return &SubqueryExpr{Query: stmt.(*SelectStatement)}, nil
		}
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		return expr, nil
	case lexer.TOKEN_EXISTS:
		// EXISTS (SELECT ...)
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		if p.current().Type != lexer.TOKEN_SELECT {
			return nil, fmt.Errorf("expected SELECT after EXISTS (")
		}
		stmt, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		selectStmt, ok := stmt.(*SelectStatement)
		if !ok {
			return nil, fmt.Errorf("expected SELECT statement in EXISTS")
		}
		return &ExistsExpr{Select: selectStmt, Not: false}, nil
	case lexer.TOKEN_CAST:
		return p.parseCast()
	case lexer.TOKEN_CASE:
		return p.parseCase()
	case lexer.TOKEN_LEFT:
		// LEFT(str, n) function
		p.advance()
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			args, err := p.parseValueListUntilRParen()
			if err != nil {
				return nil, err
			}
			return &FunctionCall{Name: "LEFT", Args: args}, nil
		}
		return &ColumnRef{Name: tok.Literal}, nil
	case lexer.TOKEN_RIGHT:
		// RIGHT(str, n) function
		p.advance()
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			args, err := p.parseValueListUntilRParen()
			if err != nil {
				return nil, err
			}
			return &FunctionCall{Name: "RIGHT", Args: args}, nil
		}
		return &ColumnRef{Name: tok.Literal}, nil
	case lexer.TOKEN_CURRENT:
		// CURRENT_DATE, CURRENT_TIME, CURRENT_TIMESTAMP
		p.advance()
		next := p.current()
		switch next.Type {
		case lexer.TOKEN_DATE:
			p.advance()
			return &FunctionCall{Name: "CURRENT_DATE", Args: nil}, nil
		case lexer.TOKEN_TIME:
			p.advance()
			return &FunctionCall{Name: "CURRENT_TIME", Args: nil}, nil
		case lexer.TOKEN_TIMESTAMP:
			p.advance()
			return &FunctionCall{Name: "CURRENT_TIMESTAMP", Args: nil}, nil
		default:
			return nil, p.syntaxError(tok, "expected DATE, TIME, or TIMESTAMP after CURRENT")
		}
	case lexer.TOKEN_STRING_AGG:
		// STRING_AGG(col, delimiter)
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		args, err := p.parseValueListUntilRParen()
		if err != nil {
			return nil, err
		}
		return &FunctionCall{Name: "STRING_AGG", Args: args}, nil
	case lexer.TOKEN_IDENT, lexer.TOKEN_COALESCE:
		ident := tok.Literal
		upper := strings.ToUpper(ident)
		p.advance()

		// Handle CURRENT_DATE, CURRENT_TIME, CURRENT_TIMESTAMP
		if upper == "CURRENT_DATE" {
			return &FunctionCall{Name: "CURRENT_DATE", Args: nil}, nil
		}
		if upper == "CURRENT_TIME" {
			return &FunctionCall{Name: "CURRENT_TIME", Args: nil}, nil
		}
		if upper == "CURRENT_TIMESTAMP" {
			return &FunctionCall{Name: "CURRENT_TIMESTAMP", Args: nil}, nil
		}

		if upper == "DATE" && p.peek().Type == lexer.TOKEN_STRING_LIT {
			p.advance()
			val := p.current().Literal
			p.advance()
			return &FunctionCall{Name: "TO_DATE", Args: []Expression{
				&Value{Type: "string", StrVal: val},
				&Value{Type: "string", StrVal: "2006-01-02"},
			}}, nil
		}
		if upper == "TIME" && p.peek().Type == lexer.TOKEN_STRING_LIT {
			p.advance()
			val := p.current().Literal
			p.advance()
			return &FunctionCall{Name: "TO_TIME", Args: []Expression{
				&Value{Type: "string", StrVal: val},
			}}, nil
		}
		if upper == "TIMESTAMP" && p.peek().Type == lexer.TOKEN_STRING_LIT {
			p.advance()
			val := p.current().Literal
			p.advance()
			return &FunctionCall{Name: "TO_TIMESTAMP", Args: []Expression{
				&Value{Type: "string", StrVal: val},
				&Value{Type: "string", StrVal: "2006-01-02 15:04:05"},
			}}, nil
		}

		if p.current().Type == lexer.TOKEN_DOT {
			p.advance() // Consume '.'
			if p.current().Type != lexer.TOKEN_IDENT {
				return nil, p.expectedError("column name after '.'", p.current())
			}
			ident = ident + "." + p.current().Literal
			p.advance()
		}
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			args := make([]Expression, 0)
			distinct := false
			if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "DISTINCT" {
				distinct = true
				p.advance()
			} else if p.current().Type == lexer.TOKEN_STAR {
				args = append(args, &ColumnRef{Name: "*"})
				p.advance()
			}

			if len(args) == 0 && p.current().Type != lexer.TOKEN_RPAREN {
				list, err := p.parseValueListUntilRParen()
				if err != nil {
					return nil, err
				}
				args = list
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}

			var funcExpr Expression
			nameUp := strings.ToUpper(ident)
			if nameUp == "COUNT" || nameUp == "SUM" || nameUp == "AVG" || nameUp == "MIN" || nameUp == "MAX" {
				funcExpr = &AggregateExpr{Name: nameUp, Args: args, Distinct: distinct}
			} else {
				funcExpr = &FunctionCall{Name: nameUp, Args: args}
			}

			if p.current().Type == lexer.TOKEN_OVER {
				p.advance()
				if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
					return nil, err
				}
				spec, err := p.parseWindowSpec()
				if err != nil {
					return nil, err
				}
				if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
					return nil, err
				}
				return &WindowFunctionExpr{
					FuncName: funcExprName(funcExpr),
					Args:     funcExprArgs(funcExpr),
					Over:     *spec,
				}, nil
			}
			return p.parsePrimaryPost(funcExpr), nil
		}
		return p.parsePrimaryPost(&ColumnRef{Name: ident}), nil

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

func (p *sqlParser) parseWindowSpec() (*WindowSpec, error) {
	spec := &WindowSpec{}

	if p.current().Type == lexer.TOKEN_PARTITION {
		p.advance()
		if err := p.consume(lexer.TOKEN_BY, "BY"); err != nil {
			return nil, err
		}
		for {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			spec.PartitionBy = append(spec.PartitionBy, expr)
			if p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
				continue
			}
			break
		}
	}

	if p.current().Type == lexer.TOKEN_ORDER {
		p.advance()
		if err := p.consume(lexer.TOKEN_BY, "BY"); err != nil {
			return nil, err
		}
		for {
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			dir := "ASC"
			if p.current().Type == lexer.TOKEN_ASC {
				p.advance()
			} else if p.current().Type == lexer.TOKEN_DESC {
				dir = "DESC"
				p.advance()
			}
			spec.OrderBy = append(spec.OrderBy, OrderItem{Expr: expr, Direction: dir})
			if p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
				continue
			}
			break
		}
	}

	if p.current().Type == lexer.TOKEN_ROWS || p.current().Type == lexer.TOKEN_RANGE {
		mode := p.current().Literal
		p.advance()
		frame, err := p.parseFrameSpec(mode)
		if err != nil {
			return nil, err
		}
		spec.Frame = frame
	}

	return spec, nil
}

func (p *sqlParser) parseFrameSpec(mode string) (*FrameSpec, error) {
	frame := &FrameSpec{Mode: strings.ToUpper(mode)}

	if p.current().Type == lexer.TOKEN_BETWEEN {
		p.advance()
		startType, startN, err := p.parseFrameBound()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_AND, "AND"); err != nil {
			return nil, err
		}
		endType, endN, err := p.parseFrameBound()
		if err != nil {
			return nil, err
		}
		frame.StartType = startType
		frame.StartN = startN
		frame.EndType = endType
		frame.EndN = endN
	} else {
		// Single bound implies BETWEEN bound AND CURRENT ROW
		startType, startN, err := p.parseFrameBound()
		if err != nil {
			return nil, err
		}
		frame.StartType = startType
		frame.StartN = startN
		frame.EndType = "CURRENT ROW"
	}

	return frame, nil
}

func (p *sqlParser) parseFrameBound() (string, int, error) {
	tok := p.current()
	switch tok.Type {
	case lexer.TOKEN_UNBOUNDED:
		p.advance()
		if p.current().Type == lexer.TOKEN_PRECEDING {
			p.advance()
			return "UNBOUNDED PRECEDING", 0, nil
		}
		if p.current().Type == lexer.TOKEN_FOLLOWING {
			p.advance()
			return "UNBOUNDED FOLLOWING", 0, nil
		}
		return "", 0, p.expectedError("PRECEDING or FOLLOWING", p.current())
	case lexer.TOKEN_CURRENT:
		p.advance()
		if err := p.consume(lexer.TOKEN_ROW, "ROW"); err != nil {
			return "", 0, err
		}
		return "CURRENT ROW", 0, nil
	case lexer.TOKEN_INT_LIT:
		n, err := strconv.Atoi(tok.Literal)
		if err != nil {
			return "", 0, fmt.Errorf("invalid window frame offset: %w", err)
		}
		p.advance()
		if p.current().Type == lexer.TOKEN_PRECEDING {
			p.advance()
			return "PRECEDING", n, nil
		}
		if p.current().Type == lexer.TOKEN_FOLLOWING {
			p.advance()
			return "FOLLOWING", n, nil
		}
		return "", 0, p.expectedError("PRECEDING or FOLLOWING", p.current())
	default:
		return "", 0, p.expectedError("frame bound", tok)
	}
}

func funcExprName(expr Expression) string {
	switch e := expr.(type) {
	case *AggregateExpr:
		return e.Name
	case *FunctionCall:
		return e.Name
	}
	return ""
}

func funcExprArgs(expr Expression) []Expression {
	switch e := expr.(type) {
	case *AggregateExpr:
		return e.Args
	case *FunctionCall:
		return e.Args
	}
	return nil
}

func (p *sqlParser) parseCast() (Expression, error) {
	p.advance() // CAST
	if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
		return nil, err
	}
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
		return nil, err
	}
	targetType, _, err := p.parseColumnType()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
		return nil, err
	}
	return &CastExpr{Expr: expr, TargetType: targetType}, nil
}

func (p *sqlParser) parseCase() (Expression, error) {
	p.advance() // CASE

	var base Expression
	if p.current().Type != lexer.TOKEN_WHEN {
		var err error
		base, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	whens := make([]CaseWhen, 0, 4)
	for p.current().Type == lexer.TOKEN_WHEN {
		p.advance() // WHEN
		cond, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_THEN, "THEN"); err != nil {
			return nil, err
		}
		res, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		whens = append(whens, CaseWhen{Condition: cond, Result: res})
	}

	if len(whens) == 0 {
		return nil, p.expectedError("at least one WHEN clause", p.current())
	}

	var elseExpr Expression
	if p.current().Type == lexer.TOKEN_ELSE {
		p.advance() // ELSE
		var err error
		elseExpr, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	if err := p.consume(lexer.TOKEN_END, "END"); err != nil {
		return nil, err
	}

	return &CaseExpr{Base: base, Whens: whens, Else: elseExpr}, nil
}

func (p *sqlParser) parseSelectColumns() ([]SelectColumn, error) {
	columns := make([]SelectColumn, 0, 8)

	if p.current().Type == lexer.TOKEN_STAR {
		p.advance()
		return columns, nil
	}

	for {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}

		alias := ""
		if p.current().Type == lexer.TOKEN_AS {
			p.advance()
			alias, err = p.consumeIdent("alias")
			if err != nil {
				return nil, err
			}
		} else if p.current().Type == lexer.TOKEN_IDENT && !isReservedKeyword(p.current().Literal) {
			alias = p.current().Literal
			p.advance()
		}

		columns = append(columns, SelectColumn{Expr: expr, Alias: alias})

		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		break
	}

	return columns, nil
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

func isReservedKeyword(s string) bool {
	upper := strings.ToUpper(s)
	switch upper {
	case "FROM", "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "OFFSET", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS":
		return true
	}
	return false
}

func (p *sqlParser) parsePrimaryPost(left Expression) Expression {
	for {
		tok := p.current()
		if tok.Type != lexer.TOKEN_ARROW && tok.Type != lexer.TOKEN_DBL_ARROW {
			break
		}
		op := tok.Literal
		p.advance()

		pathTok := p.current()
		if pathTok.Type != lexer.TOKEN_STRING_LIT {
			return left
		}
		path := pathTok.Literal
		p.advance()

		left = &JsonPathExpr{Left: left, Op: op, Path: path}
	}
	return left
}

func (p *sqlParser) parseColumnList() ([]string, error) {
	var cols []string
	name, err := p.consumeIdent("column name")
	if err != nil {
		return nil, err
	}
	cols = append(cols, name)
	for p.current().Type == lexer.TOKEN_COMMA {
		p.advance()
		name, err = p.consumeIdent("column name")
		if err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, nil
}

func (p *sqlParser) parseSetOperation(left Statement) (Statement, error) {
	for {
		tok := p.current()
		if tok.Type != lexer.TOKEN_UNION && tok.Type != lexer.TOKEN_INTERSECT && tok.Type != lexer.TOKEN_EXCEPT {
			break
		}

		op := tok.Literal
		p.advance()

		if strings.EqualFold(op, "UNION") && strings.EqualFold(p.current().Literal, "ALL") {
			op = "UNION ALL"
			p.advance()
		}

		if p.current().Type != lexer.TOKEN_SELECT {
			return nil, p.expectedError("SELECT after set operator", p.current())
		}

		right, err := p.parseSelect()
		if err != nil {
			return nil, err
		}

		// Precedence: INTERSECT is higher than UNION/EXCEPT.
		if strings.EqualFold(op, "UNION") || strings.EqualFold(op, "EXCEPT") {
			if p.current().Type == lexer.TOKEN_INTERSECT {
				right, err = p.parseSetOperation(right)
				if err != nil {
					return nil, err
				}
			}
		}

		left = &SetOperationStatement{
			Left:  left,
			Op:    strings.ToUpper(op),
			Right: right,
		}
	}
	return left, nil
}
