package parser

import (
	"strings"

	"vaultdb/internal/lexer"
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
	columns, err := p.parseIdentifierListUntilRParen("column name")
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
		return nil, err
	}
	result := &CreateIndexStatement{
		IndexName: indexName,
		TableName: tableName,
		Unique:    unique,
		IndexType: indexType,
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
