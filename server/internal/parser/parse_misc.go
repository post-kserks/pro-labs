package parser

import "vaultdb/internal/lexer"

func (p *sqlParser) parseVerifyAuditLog() (Statement, error) {
	p.advance() // VERIFY
	// AUDIT is not a keyword token (to avoid breaking CREATE TABLE audit),
	// so it comes as TOKEN_IDENT with literal "AUDIT"
	if p.current().Type != lexer.TOKEN_IDENT || p.current().Literal != "AUDIT" {
		return nil, p.expectedError("AUDIT", p.current())
	}
	p.advance()
	// LOG is not a keyword token, so it comes as TOKEN_IDENT
	if p.current().Type != lexer.TOKEN_IDENT || p.current().Literal != "LOG" {
		return nil, p.expectedError("LOG", p.current())
	}
	p.advance()
	return &VerifyAuditLogStatement{}, nil
}
