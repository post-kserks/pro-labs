package parser

import (
	"fmt"

	"vaultdb/internal/lexer"
)

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

