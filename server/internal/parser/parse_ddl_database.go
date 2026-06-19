package parser

func (p *sqlParser) parseCreateDatabase() (Statement, error) {
	p.advance()
	name, err := p.consumeIdent("database name")
	if err != nil {
		return nil, err
	}
	return &CreateDatabaseStatement{DatabaseName: name}, nil
}

func (p *sqlParser) parseDropDatabase() (Statement, error) {
	p.advance()
	name, err := p.consumeIdent("database name")
	if err != nil {
		return nil, err
	}
	return &DropDatabaseStatement{DatabaseName: name}, nil
}
