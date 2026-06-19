package parser

import (
	"fmt"

	"vaultdb/internal/lexer"
)

func (p *sqlParser) parseCreateView(orReplace bool) (Statement, error) {
	p.advance() // VIEW
	viewName, err := p.consumeIdent("view name")
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
		return nil, err
	}
	sub, err := p.parseSelect()
	if err != nil {
		return nil, fmt.Errorf("CREATE VIEW: %w", err)
	}
	sel, ok := sub.(*SelectStatement)
	if !ok {
		return nil, fmt.Errorf("CREATE VIEW: expected SELECT statement")
	}
	return &CreateViewStatement{Name: viewName, Query: sel, OrReplace: orReplace}, nil
}

func (p *sqlParser) parseDropView() (Statement, error) {
	p.advance()
	name, err := p.consumeIdent("view name")
	if err != nil {
		return nil, err
	}
	return &DropViewStatement{Name: name}, nil
}
