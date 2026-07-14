package parser

import (
	"fmt"
	"strings"

	"vaultdb/internal/core/lexer"
)

func validateDelimiter(delim string) error {
	if len(delim) == 0 {
		return fmt.Errorf("delimiter must not be empty")
	}
	if len(delim) > 20 {
		return fmt.Errorf("delimiter too long (max 20 characters)")
	}
	return nil
}

func (p *sqlParser) parseCopy() (Statement, error) {
	p.advance() // COPY

	tableName, err := p.consumeIdent("table name")
	if err != nil {
		return nil, err
	}

	stmt := &CopyStatement{TableName: tableName}

	if p.current().Type == lexer.TOKEN_FROM {
		stmt.IsFrom = true
		p.advance()
	} else if p.current().Type == lexer.TOKEN_TO {
		stmt.IsFrom = false
		p.advance()
	} else {
		return nil, p.expectedError("FROM or TO", p.current())
	}

	// Parse filename: string literal, STDIN, or STDOUT
	if p.current().Type == lexer.TOKEN_STRING_LIT {
		stmt.Filename = p.current().Literal
		p.advance()
	} else if p.current().Type == lexer.TOKEN_IDENT {
		upper := strings.ToUpper(p.current().Literal)
		if upper == "STDIN" || upper == "STDOUT" {
			stmt.Filename = upper
			p.advance()
		} else {
			return nil, fmt.Errorf("syntax error at line %d, col %d: expected filename string or STDIN/STDOUT, got '%s'", p.current().Line, p.current().Col, p.current().Literal)
		}
	} else {
		return nil, p.expectedError("filename or STDIN/STDOUT", p.current())
	}

	// Optional WITH clause
	if p.current().Type == lexer.TOKEN_WITH {
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		opts, err := p.parseCopyOptions()
		if err != nil {
			return nil, err
		}
		stmt.Options = opts
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
	}

	// Set defaults
	if stmt.Options.Format == "" {
		stmt.Options.Format = "CSV"
	}
	if stmt.Options.Delimiter == "" {
		stmt.Options.Delimiter = ","
	}

	return stmt, nil
}

func (p *sqlParser) parseCopyOptions() (CopyOptions, error) {
	var opts CopyOptions

	for {
		if p.current().Type == lexer.TOKEN_RPAREN {
			break
		}

		name, err := p.consumeIdent("option name")
		if err != nil {
			return opts, err
		}

		upper := strings.ToUpper(name)
		switch upper {
		case "FORMAT":
			if p.current().Type != lexer.TOKEN_IDENT && p.current().Type != lexer.TOKEN_STRING_LIT {
				return opts, p.expectedError("format name (CSV, JSON)", p.current())
			}
			val := strings.ToUpper(p.current().Literal)
			p.advance()
			if val != "CSV" && val != "JSON" && val != "JSONL" {
				return opts, fmt.Errorf("unsupported format: %s (supported: CSV, JSON, JSONL)", val)
			}
			opts.Format = val
		case "HEADER":
			if p.current().Type == lexer.TOKEN_TRUE {
				opts.Header = true
				p.advance()
			} else if p.current().Type == lexer.TOKEN_FALSE {
				opts.Header = false
				p.advance()
			} else if p.current().Type == lexer.TOKEN_INT_LIT {
				opts.Header = p.current().Literal != "0"
				p.advance()
			} else if p.current().Type == lexer.TOKEN_RPAREN || p.current().Type == lexer.TOKEN_COMMA {
				// Standalone HEADER keyword (e.g., FORMAT CSV, HEADER) means true
				opts.Header = true
			} else {
				return opts, p.expectedError("TRUE, FALSE, or integer", p.current())
			}
		case "DELIMITER":
			if p.current().Type == lexer.TOKEN_STRING_LIT {
				opts.Delimiter = p.current().Literal
				if err := validateDelimiter(opts.Delimiter); err != nil {
					return opts, err
				}
				p.advance()
			} else if p.current().Type == lexer.TOKEN_IDENT {
				// Support single-char aliases like TAB
				if strings.ToUpper(p.current().Literal) == "TAB" {
					opts.Delimiter = "\t"
				} else {
					opts.Delimiter = p.current().Literal
				}
				if err := validateDelimiter(opts.Delimiter); err != nil {
					return opts, err
				}
				p.advance()
			} else {
				return opts, p.expectedError("delimiter string", p.current())
			}
		default:
			return opts, fmt.Errorf("unknown COPY option: %s", name)
		}

		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
		}
	}

	return opts, nil
}
