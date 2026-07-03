package parser

import "vaultdb/internal/lexer"

func (p *sqlParser) parseCreateDatabase() (Statement, error) {
	p.advance()

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

	name, err := p.consumeIdent("database name")
	if err != nil {
		return nil, err
	}
	return &CreateDatabaseStatement{DatabaseName: name, IfNotExists: ifNotExists}, nil
}

func (p *sqlParser) parseDropDatabase() (Statement, error) {
	p.advance()

	ifExists := false
	if p.current().Type == lexer.TOKEN_IF {
		p.advance() // IF
		if err := p.consume(lexer.TOKEN_EXISTS, "EXISTS"); err != nil {
			return nil, err
		}
		ifExists = true
	}

	name, err := p.consumeIdent("database name")
	if err != nil {
		return nil, err
	}
	return &DropDatabaseStatement{DatabaseName: name, IfExists: ifExists}, nil
}
