package parser

import (
	"strings"

	"vaultdb/internal/core/lexer"
)

func (p *sqlParser) parseCreateTrigger() (Statement, error) {
	p.advance() // TRIGGER
	triggerName, err := p.consumeIdent("trigger name")
	if err != nil {
		return nil, err
	}
	timing := "BEFORE"
	if p.current().Type == lexer.TOKEN_BEFORE {
		p.advance()
		timing = "BEFORE"
	} else if p.current().Type == lexer.TOKEN_AFTER {
		p.advance()
		timing = "AFTER"
	}
	event := strings.ToUpper(p.current().Literal)
	if event != "INSERT" && event != "UPDATE" && event != "DELETE" {
		return nil, p.expectedError("INSERT, UPDATE or DELETE", p.current())
	}
	p.advance()
	if err := p.consume(lexer.TOKEN_ON, "ON"); err != nil {
		return nil, err
	}
	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}
	body := ""
	if p.current().Type == lexer.TOKEN_BEGIN {
		p.advance()
		for p.current().Type != lexer.TOKEN_END && p.current().Type != lexer.TOKEN_EOF {
			body += p.current().Literal + " "
			p.advance()
		}
		if p.current().Type == lexer.TOKEN_END {
			p.advance()
		}
		body = strings.TrimSpace(body)
	}
	return &CreateTriggerStatement{Name: triggerName, TableName: tableName, Timing: timing, Event: event, Body: body}, nil
}

func (p *sqlParser) parseDropTrigger() (Statement, error) {
	p.advance()
	name, err := p.consumeIdent("trigger name")
	if err != nil {
		return nil, err
	}
	return &DropTriggerStatement{Name: name}, nil
}
