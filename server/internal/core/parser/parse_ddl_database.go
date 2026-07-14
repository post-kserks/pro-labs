package parser

import "vaultdb/internal/core/lexer"

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

	encrypted := false
	encryptionKey := ""
	if p.current().Type == lexer.TOKEN_ENCRYPTED {
		p.advance()
		encrypted = true
		if p.current().Type == lexer.TOKEN_WITH {
			p.advance()
			if err := p.consume(lexer.TOKEN_KEY, "KEY"); err != nil {
				return nil, err
			}
			keyExpr, err := p.parseExpression()
			if err != nil {
				return nil, err
			}
			if keyLit, ok := keyExpr.(Value); ok {
				encryptionKey = keyLit.StrVal
			}
		}
	}

	return &CreateDatabaseStatement{DatabaseName: name, IfNotExists: ifNotExists, Encrypted: encrypted, EncryptionKey: encryptionKey}, nil
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
