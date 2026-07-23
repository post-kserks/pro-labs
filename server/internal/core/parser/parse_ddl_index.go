package parser

import (
	"strings"

	"vaultdb/internal/core/lexer"
)

func (p *sqlParser) parseCreateIndex() (Statement, error) {
	// Check for optional UNIQUE keyword before INDEX
	unique := false
	if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "UNIQUE" {
		unique = true
		p.advance()
	}

	// Now we should be at INDEX
	if err := p.consume(lexer.TOKEN_INDEX, "INDEX"); err != nil {
		return nil, err
	}

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

	// Optional: USING <type>
	indexType := ""
	if p.current().Type == lexer.TOKEN_USING {
		p.advance() // USING
		typeTok := p.current()
		if typeTok.Type == lexer.TOKEN_IDENT || typeTok.Type == lexer.TOKEN_HASH {
			indexType = strings.ToUpper(typeTok.Literal)
			p.advance()
		} else {
			return nil, p.expectedError("GIN, BTREE, GIST or HASH", typeTok)
		}
	}

	if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
		return nil, err
	}

	var columns []string
	isExpression := false
	for {
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}

		if ident, ok := expr.(*ColumnRef); ok {
			columns = append(columns, ident.Name)
		} else {
			isExpression = true
			columns = append(columns, FormatExpression(expr))
		}

		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		if p.current().Type == lexer.TOKEN_RPAREN {
			break
		}
		return nil, p.expectedError("',' or ')'", p.current())
	}

	p.advance() // Consume ')'

	var predicate interface{}
	if p.current().Type == lexer.TOKEN_WHERE {
		p.advance()
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		predicate = expr
	}

	result := &CreateIndexStatement{
		IndexName:    indexName,
		TableName:    tableName,
		Unique:       unique,
		IndexType:    indexType,
		IsExpression: isExpression,
		Predicate:    predicate,
	}
	if len(columns) == 1 {
		result.Column = columns[0]
	} else {
		result.Columns = columns
	}
	return result, nil
}

func (p *sqlParser) parseDropIndex() (Statement, error) {
	p.advance()
	name, err := p.consumeIdent("index name")
	if err != nil {
		return nil, err
	}
	return &DropIndexStatement{IndexName: name}, nil
}
