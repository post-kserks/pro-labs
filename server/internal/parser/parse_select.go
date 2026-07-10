package parser

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/lexer"
)

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

		// Parse the CTE query — can be SELECT or nested CTE
		stmt, err := p.parseStatement()
		if err != nil {
			return nil, err
		}

		// Accept SelectStatement, SetOperationStatement, CTEStatement, and DML with RETURNING as CTE body
		switch s := stmt.(type) {
		case *SelectStatement:
			// OK
		case *SetOperationStatement:
			_ = s // UNION/INTERSECT/EXCEPT — valid
		case *CTEStatement:
			_ = s // nested CTE — valid
		case *DeleteStatement:
			_ = s // DELETE ... RETURNING — valid
		case *InsertStatement:
			_ = s // INSERT ... RETURNING — valid
		case *UpdateStatement:
			_ = s // UPDATE ... RETURNING — valid
		default:
			return nil, fmt.Errorf("CTE body must be a SELECT, DML with RETURNING, set operation, or WITH statement")
		}

		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}

		ctes = append(ctes, CTEDefinition{
			Name:    name,
			Columns: columns,
			Query:   stmt,
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
func (p *sqlParser) parseSelect() (Statement, error) {
	p.advance() // SELECT

	// Check for DISTINCT [ON (...)]
	distinct := false
	var distinctOn []Expression
	if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "DISTINCT" {
		distinct = true
		p.advance()

		// Check for DISTINCT ON (expr, ...)
		if p.current().Type == lexer.TOKEN_ON {
			p.advance()
			if err := p.consume(lexer.TOKEN_LPAREN, "'(' after DISTINCT ON"); err != nil {
				return nil, err
			}
			for {
				expr, err := p.parseExpression()
				if err != nil {
					return nil, err
				}
				distinctOn = append(distinctOn, expr)
				if p.current().Type == lexer.TOKEN_COMMA {
					p.advance()
					continue
				}
				break
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')' after DISTINCT ON columns"); err != nil {
				return nil, err
			}
		}
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
			if err := p.checkDepth(); err != nil {
				return nil, err
			}
			if p.current().Type == lexer.TOKEN_LPAREN {
				p.advance() // consume '('
			}
			stmt, err := p.parseSelect()
			if err != nil {
				p.exitDepth()
				return nil, fmt.Errorf("derived table: %w", err)
			}
			sub, ok := stmt.(*SelectStatement)
			if !ok {
				p.exitDepth()
				return nil, fmt.Errorf("derived table: expected SELECT statement")
			}
			if !isLateral {
				if err := p.consume(lexer.TOKEN_RPAREN, ")"); err != nil {
					p.exitDepth()
					return nil, err
				}
			}
			p.exitDepth()
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
	case lexer.TOKEN_IDENT:
		if strings.ToUpper(p.current().Literal) == "VERSION" {
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
			// CROSS JOIN and regular JOINs need the JOIN keyword
			// CROSS alone (without JOIN) is also valid in some dialects
			if p.current().Type == lexer.TOKEN_JOIN {
				p.advance() // consume optional JOIN keyword
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
		DistinctOn:   distinctOn,
	}

	if p.current().Type == lexer.TOKEN_LIMIT {
		p.advance()
		limitTok := p.current()
		switch limitTok.Type {
		case lexer.TOKEN_INT_LIT:
			limit, err := strconv.Atoi(limitTok.Literal)
			if err != nil || limit < 0 {
				return nil, p.syntaxError(limitTok, "LIMIT must be a non-negative integer")
			}
			p.advance()
			stmt.Limit = limit
			stmt.HasLimit = true
		case lexer.TOKEN_PARAM:
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			stmt.LimitExpr = expr
			stmt.HasLimit = true
		default:
			return nil, p.expectedError("LIMIT value", limitTok)
		}
	}

	if p.current().Type == lexer.TOKEN_OFFSET {
		p.advance()
		offsetTok := p.current()
		switch offsetTok.Type {
		case lexer.TOKEN_INT_LIT:
			offset, err := strconv.Atoi(offsetTok.Literal)
			if err != nil || offset < 0 {
				return nil, p.syntaxError(offsetTok, "OFFSET must be a non-negative integer")
			}
			p.advance()
			stmt.Offset = offset
			stmt.HasOffset = true
		case lexer.TOKEN_PARAM:
			expr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			stmt.OffsetExpr = expr
			stmt.HasOffset = true
		default:
			return nil, p.expectedError("OFFSET value", offsetTok)
		}
	}

	return stmt, nil
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
