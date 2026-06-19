package parser

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"vaultdb/internal/lexer"
)

// Parse parses one SQL statement terminated by ';'.
func Parse(sql string) (Statement, error) {
	stmt, err := parse(sql)
	if err != nil {
		slog.Debug("parse error", "input", sql, "error", err)
		return nil, fmt.Errorf("invalid query syntax")
	}
	return stmt, nil
}

// ParseExpression parses a standalone SQL expression (no statement wrapper).
func ParseExpression(expr string) (Expression, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, fmt.Errorf("empty expression")
	}
	l := lexer.New(expr)
	tokens := make([]lexer.Token, 0, 64)
	for {
		tok := l.NextToken()
		if tok.Type == lexer.TOKEN_ILLEGAL {
			return nil, fmt.Errorf("syntax error at line %d, col %d: illegal token '%s'", tok.Line, tok.Col, tok.Literal)
		}
		tokens = append(tokens, tok)
		if tok.Type == lexer.TOKEN_EOF {
			break
		}
	}
	p := &sqlParser{tokens: tokens}
	return p.parseExpression()
}

// FormatExpression converts a parsed expression back to a SQL string.
func FormatExpression(expr Expression) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *BinaryExpr:
		left := FormatExpression(e.Left)
		right := FormatExpression(e.Right)
		return fmt.Sprintf("%s %s %s", left, e.Operator, right)
	case *AndExpr:
		return fmt.Sprintf("(%s AND %s)", FormatExpression(e.Left), FormatExpression(e.Right))
	case *OrExpr:
		return fmt.Sprintf("(%s OR %s)", FormatExpression(e.Left), FormatExpression(e.Right))
	case *NotExpr:
		return fmt.Sprintf("NOT %s", FormatExpression(e.Expr))
	case *ColumnRef:
		return e.Name
	case Value:
		return formatValueSQL(e)
	case *Value:
		return formatValueSQL(*e)
	case *InExpr:
		ops := make([]string, len(e.Right))
		for i, r := range e.Right {
			ops[i] = FormatExpression(r)
		}
		not := ""
		if e.Not {
			not = "NOT "
		}
		return fmt.Sprintf("%s %sIN (%s)", FormatExpression(e.Left), not, strings.Join(ops, ", "))
	default:
		return "<expr>"
	}
}

func formatValueSQL(v Value) string {
	switch v.Type {
	case "string":
		return fmt.Sprintf("'%s'", v.StrVal)
	case "int":
		return strconv.FormatInt(v.IntVal, 10)
	case "float":
		return strconv.FormatFloat(v.FltVal, 'f', -1, 64)
	case "bool":
		if v.BoolVal {
			return "TRUE"
		}
		return "FALSE"
	case "null":
		return "NULL"
	default:
		return v.StrVal
	}
}

func parse(sql string) (Statement, error) {
	const maxInputSize = 10 * 1024 * 1024 // 10MB
	if len(sql) > maxInputSize {
		return nil, fmt.Errorf("query too large (%d bytes, max 10MB)", len(sql))
	}
	if strings.TrimSpace(sql) == "" {
		return nil, fmt.Errorf("syntax error: empty query")
	}

	l := lexer.New(sql)
	tokens := make([]lexer.Token, 0, 64)
	for {
		tok := l.NextToken()
		if tok.Type == lexer.TOKEN_ILLEGAL {
			return nil, fmt.Errorf("syntax error at line %d, col %d: illegal token '%s'", tok.Line, tok.Col, tok.Literal)
		}
		tokens = append(tokens, tok)
		if tok.Type == lexer.TOKEN_EOF {
			break
		}
	}

	p := &sqlParser{tokens: tokens}
	stmt, err := p.parseStatement()
	if err != nil {
		return nil, err
	}

	if p.current().Type != lexer.TOKEN_SEMICOLON {
		if p.current().Type == lexer.TOKEN_EOF {
			return nil, fmt.Errorf("syntax error: unexpected end of input, expected ';'")
		}
		return nil, p.expectedError("';'", p.current())
	}
	p.advance()

	if p.current().Type != lexer.TOKEN_EOF {
		return nil, p.syntaxError(p.current(), "unexpected token after ';'")
	}

	return stmt, nil
}

type sqlParser struct {
	tokens []lexer.Token
	pos    int
}

func (p *sqlParser) parseStatement() (Statement, error) {
	var stmt Statement
	var err error

	switch p.current().Type {
	case lexer.TOKEN_CREATE:
		if p.peek().Type == lexer.TOKEN_MIGRATION {
			stmt, err = p.parseMigration("CREATE")
		} else if p.peek().Type == lexer.TOKEN_POLICY {
			stmt, err = p.parseCreatePolicy()
		} else {
			stmt, err = p.parseCreate()
		}
	case lexer.TOKEN_ALTER:
		stmt, err = p.parseAlterTable()
	case lexer.TOKEN_DROP:
		stmt, err = p.parseDrop()
	case lexer.TOKEN_USE:
		stmt, err = p.parseUse()
	case lexer.TOKEN_SHOW:
		stmt, err = p.parseShow()
	case lexer.TOKEN_DESCRIBE:
		stmt, err = p.parseDescribe()
	case lexer.TOKEN_EXPLAIN:
		stmt, err = p.parseExplain()
	case lexer.TOKEN_HISTORY:
		stmt, err = p.parseHistory()
	case lexer.TOKEN_SELECT:
		stmt, err = p.parseSelect()
	case lexer.TOKEN_WITH:
		stmt, err = p.parseCTE()
	case lexer.TOKEN_INSERT:
		stmt, err = p.parseInsert()
	case lexer.TOKEN_UPDATE:
		stmt, err = p.parseUpdate()
	case lexer.TOKEN_DELETE:
		stmt, err = p.parseDelete()
	case lexer.TOKEN_MERGE:
		stmt, err = p.parseMerge()
	case lexer.TOKEN_TRUNCATE:
		stmt, err = p.parseTruncate()
	case lexer.TOKEN_VACUUM:
		stmt, err = p.parseVacuum()
	case lexer.TOKEN_BEGIN:
		stmt, err = p.parseBegin()
	case lexer.TOKEN_COMMIT:
		stmt, err = p.parseCommit()
	case lexer.TOKEN_ROLLBACK:
		if p.peek().Type == lexer.TOKEN_TO {
			stmt, err = p.parseRollbackToSavepoint()
		} else if p.peek().Type == lexer.TOKEN_MIGRATION {
			stmt, err = p.parseMigration("ROLLBACK")
		} else {
			stmt, err = p.parseRollback()
		}
	case lexer.TOKEN_SAVEPOINT:
		stmt, err = p.parseSavepoint()
	case lexer.TOKEN_RELEASE:
		stmt, err = p.parseReleaseSavepoint()
	case lexer.TOKEN_PREPARE:
		stmt, err = p.parsePrepare()
	case lexer.TOKEN_EXECUTE:
		stmt, err = p.parseExecute()
	case lexer.TOKEN_DEALLOCATE:
		stmt, err = p.parseDeallocate()
	case lexer.TOKEN_CALL:
		stmt, err = p.parseCall()
	case lexer.TOKEN_ENABLE:
		stmt, err = p.parseEnableRls()
	case lexer.TOKEN_APPLY:
		stmt, err = p.parseMigration("APPLY")
	case lexer.TOKEN_PREVIEW:
		stmt, err = p.parseMigration("PREVIEW")
	default:
		return nil, p.expectedError("a statement", p.current())
	}

	if err != nil {
		return nil, err
	}

	// Check for set operations after SELECT
	if _, ok := stmt.(*SelectStatement); ok {
		return p.parseSetOperation(stmt)
	}

	return stmt, nil
}
