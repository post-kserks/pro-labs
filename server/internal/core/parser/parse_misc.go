package parser

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/core/lexer"
)

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

func (p *sqlParser) parseArchiveAuditLog() (Statement, error) {
	p.advance() // ARCHIVE
	// AUDIT
	if p.current().Type != lexer.TOKEN_IDENT || p.current().Literal != "AUDIT" {
		return nil, p.expectedError("AUDIT", p.current())
	}
	p.advance()
	// LOG
	if p.current().Type != lexer.TOKEN_IDENT || p.current().Literal != "LOG" {
		return nil, p.expectedError("LOG", p.current())
	}
	p.advance()

	stmt := &ArchiveAuditLogStatement{}

	// Optional: TO 'path'
	if p.current().Type == lexer.TOKEN_TO {
		p.advance() // TO
		if p.current().Type != lexer.TOKEN_STRING_LIT {
			return nil, p.expectedError("string path", p.current())
		}
		stmt.Path = p.current().Literal
		p.advance()
	}

	// Optional: KEEP N
	if p.current().Type == lexer.TOKEN_IDENT && p.current().Literal == "KEEP" {
		p.advance() // KEEP
		if p.current().Type != lexer.TOKEN_INT_LIT {
			return nil, p.expectedError("integer count", p.current())
		}
		val, err := strconv.ParseInt(p.current().Literal, 10, 64)
		if err != nil {
			return nil, err
		}
		stmt.KeepCount = int(val)
		p.advance()
	}

	return stmt, nil
}

func (p *sqlParser) parseKill() (Statement, error) {
	p.advance() // KILL
	if p.current().Type != lexer.TOKEN_IDENT || !strings.EqualFold(p.current().Literal, "QUERY") {
		return nil, p.expectedError("QUERY", p.current())
	}
	p.advance()
	if p.current().Type != lexer.TOKEN_INT_LIT {
		return nil, p.expectedError("session ID (integer)", p.current())
	}
	val, err := strconv.ParseUint(p.current().Literal, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid session ID: %w", err)
	}
	p.advance()
	return &KillStatement{SessionID: val}, nil
}
