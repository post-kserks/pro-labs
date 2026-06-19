package parser

import "vaultdb/internal/lexer"

func (p *sqlParser) parseCreateIndex() (Statement, error) {
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
