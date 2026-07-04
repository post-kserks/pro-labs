package parser

import (
	"fmt"

	"vaultdb/internal/lexer"
)

func (p *sqlParser) parseInsert() (Statement, error) {
	p.advance() // INSERT

	orReplace := false
	if p.current().Type == lexer.TOKEN_OR {
		p.advance()
		if p.current().Type == lexer.TOKEN_REPLACE {
			p.advance()
			orReplace = true
		} else {
			return nil, p.expectedError("REPLACE", p.current())
		}
	}

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

	var rows [][]Expression
	var selectQuery Statement

	if p.current().Type == lexer.TOKEN_VALUES {
		p.advance()
		rows = make([][]Expression, 0, 4)
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
	} else if p.current().Type == lexer.TOKEN_SELECT {
		stmt, err := p.parseSelect()
		if err != nil {
			return nil, fmt.Errorf("INSERT ... SELECT: %w", err)
		}
		// Check for set operations (UNION, INTERSECT, EXCEPT)
		selectQuery, err = p.parseSetOperation(stmt)
		if err != nil {
			return nil, fmt.Errorf("INSERT ... SELECT: %w", err)
		}
	} else {
		return nil, fmt.Errorf("syntax error: INSERT requires VALUES or SELECT after column list")
	}

	// Check for ON CONFLICT
	var onConflict *OnConflictClause
	if p.current().Type == lexer.TOKEN_ON {
		p.advance()
		if err := p.consume(lexer.TOKEN_CONFLICT, "CONFLICT"); err != nil {
			return nil, err
		}

		// Parse optional conflict target: ON CONFLICT (col1, col2)
		var conflictColumns []string
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			conflictColumns, err = p.parseColumnList()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_RPAREN, ")"); err != nil {
				return nil, err
			}
		}

		if err := p.consume(lexer.TOKEN_DO, "DO"); err != nil {
			return nil, err
		}

		onConflict = &OnConflictClause{Columns: conflictColumns}

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

	return &InsertStatement{TableName: tableName, Columns: columns, Rows: rows, SelectQuery: selectQuery, OnConflict: onConflict, Returning: returning, OrReplace: orReplace}, nil
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
		// Allow table-qualified LHS: SET t.col = expr
		if p.current().Type == lexer.TOKEN_DOT && p.peek().Type == lexer.TOKEN_IDENT {
			p.advance() // consume dot
			column, err = p.consumeIdent("column name")
			if err != nil {
				return nil, err
			}
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

	// Check for FROM table or FROM (subquery) AS alias (PostgreSQL syntax: UPDATE ... SET ... FROM ...)
	var fromTable string
	var fromAlias string
	var fromSubquery *SelectStatement
	if p.current().Type == lexer.TOKEN_FROM {
		p.advance()
		if p.current().Type == lexer.TOKEN_LPAREN {
			// FROM (SELECT ...) AS alias
			p.advance() // consume '('
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, fmt.Errorf("FROM subquery: %w", err)
			}
			sub, ok := stmt.(*SelectStatement)
			if !ok {
				return nil, fmt.Errorf("FROM subquery: expected SELECT statement")
			}
			if err := p.consume(lexer.TOKEN_RPAREN, ")"); err != nil {
				return nil, err
			}
			fromSubquery = sub
			// Require AS alias for subquery
			if p.current().Type == lexer.TOKEN_AS {
				p.advance()
				fromAlias, err = p.consumeIdent("subquery alias")
				if err != nil {
					return nil, err
				}
			} else if p.current().Type == lexer.TOKEN_IDENT && !isReservedKeyword(p.current().Literal) {
				fromAlias = p.current().Literal
				p.advance()
			}
		} else {
			// FROM table_name
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
		TableName:    tableName,
		Assignments:  assignments,
		Where:        where,
		Returning:    returning,
		FromTable:    fromTable,
		FromAlias:    fromAlias,
		FromSubquery: fromSubquery,
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

	// USING source_table | USING (subquery)
	if err := p.consume(lexer.TOKEN_USING, "USING"); err != nil {
		return nil, err
	}

	var sourceTable string
	var sourceQuery Statement
	if p.current().Type == lexer.TOKEN_LPAREN {
		// USING (subquery) AS alias
		p.advance()
		sel, err := p.parseSelect()
		if err != nil {
			return nil, fmt.Errorf("MERGE USING subquery: %w", err)
		}
		// Check for set operations (UNION, INTERSECT, EXCEPT)
		sourceQuery, err = p.parseSetOperation(sel)
		if err != nil {
			return nil, fmt.Errorf("MERGE USING subquery: %w", err)
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
	} else {
		sourceTable, err = p.consumeIdent("source table")
		if err != nil {
			return nil, err
		}
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

	// Parse WHEN clauses (zero or two: MATCHED and/or NOT MATCHED, in any order)
	var whenMatched *MergeWhenClause
	var whenNotMatched *MergeWhenClause
	for p.current().Type == lexer.TOKEN_WHEN {
		p.advance() // WHEN
		if p.current().Type == lexer.TOKEN_NOT {
			// WHEN NOT MATCHED THEN INSERT ...
			p.advance() // NOT
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

			// INSERT ... VALUES (...) | INSERT ... SELECT ...
			var selectQuery Statement
			var values [][]Expression
			if p.current().Type == lexer.TOKEN_VALUES {
				p.advance()
				if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
					return nil, err
				}
				vals, err := p.parseValueListUntilRParen()
				if err != nil {
					return nil, err
				}
				if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
					return nil, err
				}
				values = [][]Expression{vals}
			} else if p.current().Type == lexer.TOKEN_SELECT {
				stmt, err := p.parseSelect()
				if err != nil {
					return nil, fmt.Errorf("MERGE INSERT ... SELECT: %w", err)
				}
				selectQuery, err = p.parseSetOperation(stmt)
				if err != nil {
					return nil, fmt.Errorf("MERGE INSERT ... SELECT: %w", err)
				}
			} else {
				return nil, fmt.Errorf("syntax error: MERGE INSERT requires VALUES or SELECT after column list")
			}

			whenNotMatched = &MergeWhenClause{Action: "INSERT", Columns: columns, Values: values, SelectQuery: selectQuery}
		} else {
			// WHEN MATCHED THEN UPDATE SET ...
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
				if p.current().Type == lexer.TOKEN_DOT && p.peek().Type == lexer.TOKEN_IDENT {
					p.advance()
					column, err = p.consumeIdent("column name")
					if err != nil {
						return nil, err
					}
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
	}

	// Optional RETURNING clause
	var returning []SelectColumn
	if p.current().Type == lexer.TOKEN_RETURNING {
		p.advance()
		returning, err = p.parseSelectColumns()
		if err != nil {
			return nil, fmt.Errorf("MERGE RETURNING: %w", err)
		}
	}

	return &MergeStatement{
		TargetTable:    targetTable,
		SourceTable:    sourceTable,
		SourceQuery:    sourceQuery,
		Alias:          alias,
		OnCondition:    onCondition,
		WhenMatched:    whenMatched,
		WhenNotMatched: whenNotMatched,
		Returning:      returning,
	}, nil
}
