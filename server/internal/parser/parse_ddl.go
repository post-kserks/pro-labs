package parser

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/lexer"
)

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
	case lexer.TOKEN_ENCRYPTION:
		p.advance() // ENCRYPTION
		if p.current().Type != lexer.TOKEN_IDENT || strings.ToUpper(p.current().Literal) != "STATUS" {
			return nil, p.expectedError("STATUS", p.current())
		}
		p.advance() // STATUS
		return &ShowEncryptionStatusStatement{}, nil
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
		return nil, p.expectedError("DATABASES, TABLES, INDEXES or ENCRYPTION", p.current())
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
		return p.parseCreateDatabase()
	case lexer.TOKEN_TABLE:
		return p.parseCreateTable()
	case lexer.TOKEN_VIEW:
		return p.parseCreateView(orReplace)
	case lexer.TOKEN_TRIGGER:
		return p.parseCreateTrigger()
	case lexer.TOKEN_FUNCTION:
		return p.parseCreateFunction()
	case lexer.TOKEN_PROCEDURE:
		return p.parseCreateProcedure()
	case lexer.TOKEN_INDEX:
		return p.parseCreateIndex()
	case lexer.TOKEN_IDENT:
		if strings.ToUpper(p.current().Literal) == "ROLE" {
			return p.parseCreateRole()
		}
		return nil, p.expectedError("DATABASE, TABLE, VIEW, INDEX or ROLE", p.current())
	default:
		return nil, p.expectedError("DATABASE, TABLE, VIEW or INDEX", p.current())
	}
}

func (p *sqlParser) parseCreateFunction() (Statement, error) {
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
	// LANGUAGE may appear before or after AS
	lang := "SQL"
	if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "LANGUAGE" {
		p.advance()
		lang = strings.ToUpper(p.current().Literal)
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
	// LANGUAGE may also appear after AS
	if lang == "SQL" && p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "LANGUAGE" {
		p.advance()
		lang = strings.ToUpper(p.current().Literal)
		p.advance()
	}
	// Optional WITH clause for WASM options
	var options map[string]string
	if p.current().Type == lexer.TOKEN_WITH {
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		options = make(map[string]string)
		for p.current().Type != lexer.TOKEN_RPAREN && p.current().Type != lexer.TOKEN_EOF {
			key, err := p.consumeIdent("option key")
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_EQ, "'='"); err != nil {
				return nil, err
			}
			if p.current().Type != lexer.TOKEN_STRING_LIT {
				return nil, p.expectedError("option value string", p.current())
			}
			val := p.current().Literal
			p.advance()
			options[strings.ToLower(key)] = val
			if p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
			}
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
	}
	return &CreateFunctionStatement{Name: funcName, Params: params, ReturnType: returnType, Body: body, Language: lang, Options: options}, nil
}

func (p *sqlParser) parseCreateProcedure() (Statement, error) {
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
	// LANGUAGE may appear before or after AS
	lang := "SQL"
	if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "LANGUAGE" {
		p.advance()
		lang = strings.ToUpper(p.current().Literal)
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
	// LANGUAGE may also appear after AS
	if lang == "SQL" && p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "LANGUAGE" {
		p.advance()
		lang = strings.ToUpper(p.current().Literal)
		p.advance()
	}
	// Optional WITH clause for WASM options
	var options map[string]string
	if p.current().Type == lexer.TOKEN_WITH {
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		options = make(map[string]string)
		for p.current().Type != lexer.TOKEN_RPAREN && p.current().Type != lexer.TOKEN_EOF {
			key, err := p.consumeIdent("option key")
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_EQ, "'='"); err != nil {
				return nil, err
			}
			if p.current().Type != lexer.TOKEN_STRING_LIT {
				return nil, p.expectedError("option value string", p.current())
			}
			val := p.current().Literal
			p.advance()
			options[strings.ToLower(key)] = val
			if p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
			}
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
	}
	return &CreateProcedureStatement{Name: procName, Params: params, Body: body, Language: lang, Options: options}, nil
}

func (p *sqlParser) parseCreateRole() (Statement, error) {
	p.advance() // ROLE
	name, err := p.consumeIdent("role name")
	if err != nil {
		return nil, err
	}
	password := ""
	if p.current().Type == lexer.TOKEN_WITH {
		p.advance() // WITH
		if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "PASSWORD" {
			p.advance() // PASSWORD
			if p.current().Type != lexer.TOKEN_STRING_LIT {
				return nil, p.expectedError("password string", p.current())
			}
			password = p.current().Literal
			p.advance()
		}
	}
	return &CreateRoleStatement{Name: name, Password: password}, nil
}

func (p *sqlParser) parseCreateTable() (Statement, error) {
	p.advance() // TABLE

	ifNotExists := false
	if p.current().Type == lexer.TOKEN_IF {
		p.advance() // IF
		if err := p.consume(lexer.TOKEN_NOT, "NOT"); err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_EXISTS, "EXISTS"); err != nil {
			return nil, err
		}
		ifNotExists = true
	}

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	if p.current().Type == lexer.TOKEN_INFER {
		p.advance() // INFER
		if err := p.consume(lexer.TOKEN_SCHEMA, "SCHEMA"); err != nil {
			return nil, err
		}
		return &CreateTableStatement{TableName: tableName, InferSchema: true, IfNotExists: ifNotExists}, nil
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

	encrypted := false
	if p.current().Type == lexer.TOKEN_ENCRYPTED {
		p.advance()
		encrypted = true
	}

	// Parse optional PARTITION BY clause
	var partitionBy *PartitionSpec
	if p.current().Type == lexer.TOKEN_PARTITION && p.peek().Type == lexer.TOKEN_BY {
		p.advance() // PARTITION
		p.advance() // BY
		spec, err := p.parsePartitionSpec()
		if err != nil {
			return nil, err
		}
		partitionBy = spec
	}

	return &CreateTableStatement{TableName: tableName, Columns: columns, IfNotExists: ifNotExists, Encrypted: encrypted, PartitionBy: partitionBy}, nil
}

func (p *sqlParser) parseColumnDef() (*ColumnDef, error) {
	colName, err := p.consumeIdent("column name")
	if err != nil {
		return nil, err
	}

	// SERIAL is a shorthand for INT with AUTO_INCREMENT
	if p.current().Type == lexer.TOKEN_SERIAL {
		p.advance()
		col := &ColumnDef{Name: colName, DataType: "INT", AutoIncrement: true}

		if p.current().Type == lexer.TOKEN_NOT && p.peek().Type == lexer.TOKEN_NULL {
			p.advance()
			p.advance()
			col.NotNull = true
		}

		if p.current().Type == lexer.TOKEN_PRIMARY && p.peek().Type == lexer.TOKEN_KEY {
			p.advance()
			p.advance()
			col.PrimaryKey = true
		}

		return col, nil
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

	if p.current().Type == lexer.TOKEN_NOT && p.peek().Type == lexer.TOKEN_NULL {
		p.advance() // NOT
		p.advance() // NULL
		col.NotNull = true
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
		if p.current().Type == lexer.TOKEN_ALWAYS {
			p.advance() // ALWAYS
			if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
				return nil, err
			}
			if p.current().Type == lexer.TOKEN_IDENTITY {
				p.advance() // IDENTITY
				col.AutoIncrement = true
			} else {
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
				// Optionally consume STORED or VIRTUAL keyword
				if p.current().Type == lexer.TOKEN_STORED {
					p.advance() // STORED
				} else if p.current().Type == lexer.TOKEN_VIRTUAL {
					p.advance() // VIRTUAL
				}
			}
		} else if p.current().Type == lexer.TOKEN_BY {
			p.advance() // BY
			if err := p.consume(lexer.TOKEN_DEFAULT, "DEFAULT"); err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_IDENTITY, "IDENTITY"); err != nil {
				return nil, err
			}
			col.AutoIncrement = true
		} else {
			return nil, p.expectedError("ALWAYS or BY", p.current())
		}
	}

	// Check for AUTO_INCREMENT before PRIMARY KEY (MySQL syntax allows both orderings)
	if p.current().Type == lexer.TOKEN_AUTO_INCREMENT {
		p.advance()
		col.AutoIncrement = true
	}

	if p.current().Type == lexer.TOKEN_PRIMARY && p.peek().Type == lexer.TOKEN_KEY {
		p.advance() // PRIMARY
		p.advance() // KEY
		col.PrimaryKey = true
	}

	// Also check for AUTO_INCREMENT after PRIMARY KEY (existing behavior)
	if p.current().Type == lexer.TOKEN_AUTO_INCREMENT {
		p.advance()
		col.AutoIncrement = true
	}

	if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "UNIQUE" {
		p.advance()
		col.Unique = true
	}

	if p.current().Type == lexer.TOKEN_NOT && p.peek().Type == lexer.TOKEN_NULL {
		p.advance() // NOT
		p.advance() // NULL
		col.NotNull = true
	}

	if p.current().Type == lexer.TOKEN_ENCRYPTED {
		p.advance()
		col.Encrypted = true
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
	case lexer.TOKEN_BLOB:
		p.advance()
		return "BLOB", 0, nil
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
	case lexer.TOKEN_BIGINT:
		p.advance()
		return "INT", 0, nil
	case lexer.TOKEN_NUMERIC:
		p.advance()
		return "FLOAT", 0, nil
	case lexer.TOKEN_TIMESTAMPTZ:
		p.advance()
		return "TIMESTAMP", 0, nil
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
		return p.parseDropDatabase()
	case lexer.TOKEN_TABLE:
		p.advance()
		ifExists := false
		if p.current().Type == lexer.TOKEN_IF {
			p.advance() // IF
			if err := p.consume(lexer.TOKEN_EXISTS, "EXISTS"); err != nil {
				return nil, err
			}
			ifExists = true
		}
		name, err := p.consumeIdent("table name")
		if err != nil {
			return nil, err
		}
		return &DropTableStatement{TableName: name, IfExists: ifExists}, nil
	case lexer.TOKEN_VIEW:
		return p.parseDropView()
	case lexer.TOKEN_TRIGGER:
		return p.parseDropTrigger()
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
		return p.parseDropIndex()
	case lexer.TOKEN_IDENT:
		if strings.ToUpper(p.current().Literal) == "ROLE" {
			p.advance() // ROLE
			ifExists := false
			if p.current().Type == lexer.TOKEN_IF {
				p.advance() // IF
				if err := p.consume(lexer.TOKEN_EXISTS, "EXISTS"); err != nil {
					return nil, err
				}
				ifExists = true
			}
			name, err := p.consumeIdent("role name")
			if err != nil {
				return nil, err
			}
			return &DropRoleStatement{Name: name, IfExists: ifExists}, nil
		}
		return nil, p.expectedError("DATABASE, TABLE, VIEW, INDEX or ROLE", p.current())
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
				} else if keyword == "CHECK" {
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
					action = &AlterAddConstraint{Name: constraintName, Type: "CHECK", CheckExpr: FormatExpression(expr)}
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
					var onDeleteCascade bool
					if p.current().Type == lexer.TOKEN_ON {
						p.advance() // ON
						if p.current().Type == lexer.TOKEN_DELETE {
							p.advance() // DELETE
							if strings.EqualFold(p.current().Literal, "CASCADE") {
								p.advance() // CASCADE
								onDeleteCascade = true
							}
						}
					}
					action = &AlterAddConstraint{
						Name:            constraintName,
						Type:            "FOREIGN_KEY",
						Columns:         cols,
						RefTable:        refTable,
						RefCols:         refCols,
						OnDeleteCascade: onDeleteCascade,
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

func (p *sqlParser) parseGrant() (Statement, error) {
	p.advance() // GRANT

	privileges, err := p.parsePrivilegeList()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
		return nil, err
	}
	on, err := p.parseObjectName()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_TO, "TO"); err != nil {
		return nil, err
	}
	to, err := p.consumeIdent("role name")
	if err != nil {
		return nil, err
	}
	return &GrantStatement{Privileges: privileges, On: on, To: to}, nil
}

func (p *sqlParser) parseRevoke() (Statement, error) {
	p.advance() // REVOKE

	privileges, err := p.parsePrivilegeList()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
		return nil, err
	}
	on, err := p.parseObjectName()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_FROM, "FROM"); err != nil {
		return nil, err
	}
	from, err := p.consumeIdent("role name")
	if err != nil {
		return nil, err
	}
	return &RevokeStatement{Privileges: privileges, On: on, From: from}, nil
}

// parseObjectName parses an object name for GRANT/REVOKE, accepting either
// an identifier or the * wildcard.
func (p *sqlParser) parseObjectName() (string, error) {
	if p.current().Type == lexer.TOKEN_STAR {
		p.advance()
		return "*", nil
	}
	return p.consumeIdent("object name")
}

func (p *sqlParser) parsePrivilegeList() ([]string, error) {
	var privileges []string
	for {
		tok := p.current()
		upper := strings.ToUpper(tok.Literal)
		switch upper {
		case "SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "ALL":
			privileges = append(privileges, upper)
			p.advance()
		default:
			if len(privileges) == 0 {
				return nil, p.expectedError("privilege name (SELECT, INSERT, UPDATE, DELETE, CREATE, ALL)", tok)
			}
			return privileges, nil
		}
		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		break
	}
	return privileges, nil
}

func (p *sqlParser) parsePartitionSpec() (*PartitionSpec, error) {
	// Expect RANGE or HASH
	var partType string
	switch p.current().Type {
	case lexer.TOKEN_RANGE:
		partType = "RANGE"
		p.advance()
	case lexer.TOKEN_HASH:
		partType = "HASH"
		p.advance()
	default:
		return nil, p.expectedError("RANGE or HASH", p.current())
	}

	// Parse column list: (col) or (col1, col2)
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

	spec := &PartitionSpec{
		Type:    partType,
		Columns: cols,
	}

	switch partType {
	case "RANGE":
		// Parse partition definitions: PARTITION p_name VALUES LESS THAN (value)
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		for {
			if p.current().Type == lexer.TOKEN_RPAREN {
				p.advance()
				break
			}
			def, err := p.parsePartitionDef()
			if err != nil {
				return nil, err
			}
			spec.Partitions = append(spec.Partitions, *def)
			if p.current().Type == lexer.TOKEN_COMMA {
				p.advance()
			}
		}
	case "HASH":
		// Parse PARTITIONS N
		if p.current().Type == lexer.TOKEN_PARTITIONS {
			p.advance()
			tok := p.current()
			if tok.Type != lexer.TOKEN_INT_LIT {
				return nil, p.expectedError("partition count", tok)
			}
			n, err := strconv.Atoi(tok.Literal)
			if err != nil {
				return nil, fmt.Errorf("invalid partition count: %w", err)
			}
			if n <= 0 {
				return nil, fmt.Errorf("partition count must be positive")
			}
			spec.NumParts = n
			p.advance()
		} else {
			spec.NumParts = 1
		}
	}

	return spec, nil
}

func (p *sqlParser) parsePartitionDef() (*PartitionDef, error) {
	// PARTITION name VALUES LESS THAN (value_or_maxvalue)
	if err := p.consume(lexer.TOKEN_PARTITION, "PARTITION"); err != nil {
		return nil, err
	}
	name, err := p.consumeIdent("partition name")
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_VALUES, "VALUES"); err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_LESS, "LESS"); err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_THAN, "THAN"); err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
		return nil, err
	}

	var bound interface{}
	if p.current().Type == lexer.TOKEN_MAXVALUE {
		bound = nil // MAXVALUE → no upper bound
		p.advance()
	} else {
		val, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		// Store as a Value expression for later evaluation
		bound = val
	}

	if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
		return nil, err
	}

	return &PartitionDef{Name: name, Bound: bound}, nil
}
