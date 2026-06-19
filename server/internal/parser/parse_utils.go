package parser

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/lexer"
)

func (p *sqlParser) parseExpression() (Expression, error) {
	return p.parseOr()
}

func (p *sqlParser) parseOr() (Expression, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.current().Type == lexer.TOKEN_OR {
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &OrExpr{Left: left, Right: right}
	}

	return left, nil
}

func (p *sqlParser) parseAnd() (Expression, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}

	for p.current().Type == lexer.TOKEN_AND {
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = &AndExpr{Left: left, Right: right}
	}

	return left, nil
}

func (p *sqlParser) parseNot() (Expression, error) {
	if p.current().Type == lexer.TOKEN_NOT {
		p.advance()
		expr, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return &NotExpr{Expr: expr}, nil
	}
	return p.parseComparison()
}

func (p *sqlParser) parseComparison() (Expression, error) {
	left, err := p.parseAddition()
	if err != nil {
		return nil, err
	}

	switch p.current().Type {
	case lexer.TOKEN_EQ, lexer.TOKEN_NEQ, lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTE, lexer.TOKEN_GTE, lexer.TOKEN_SEMANTIC_MATCH, lexer.TOKEN_FTS_MATCH, lexer.TOKEN_JSON_CONTAINS, lexer.TOKEN_JSON_CONTAINED_BY, lexer.TOKEN_JSON_HAS_KEY, lexer.TOKEN_JSON_MERGE, lexer.TOKEN_FULLTEXT_MATCH:
		op := p.current().Literal
		p.advance()

		if p.current().Type == lexer.TOKEN_ALL || p.current().Type == lexer.TOKEN_ANY || p.current().Type == lexer.TOKEN_SOME {
			quantifier := strings.ToUpper(p.current().Literal)
			p.advance()
			if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
				return nil, err
			}
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				return nil, fmt.Errorf("expected SELECT statement in %s subquery", quantifier)
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}
			return &ComparisonSubqueryExpr{Left: left, Operator: op, Quantifier: quantifier, Subquery: sel}, nil
		}

		right, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Left: left, Operator: op, Right: right}, nil
	case lexer.TOKEN_LIKE:
		p.advance()
		right, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Left: left, Operator: "LIKE", Right: right}, nil
	case lexer.TOKEN_IN:
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}

		var list []Expression
		if p.current().Type == lexer.TOKEN_SELECT {
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if selectStmt, ok := stmt.(*SelectStatement); ok {
				list = []Expression{&SubqueryExpr{Query: selectStmt}}
			} else {
				return nil, fmt.Errorf("expected SELECT statement in subquery")
			}
		} else {
			var err error
			list, err = p.parseValueListUntilRParen()
			if err != nil {
				return nil, err
			}
		}

		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		return &InExpr{Left: left, Not: false, Right: list}, nil
	case lexer.TOKEN_NOT:
		if p.peek().Type == lexer.TOKEN_BETWEEN {
			p.advance() // NOT
			p.advance() // BETWEEN
			lower, err := p.parseAddition()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_AND, "AND"); err != nil {
				return nil, err
			}
			upper, err := p.parseAddition()
			if err != nil {
				return nil, err
			}
			return &BetweenExpr{Expr: left, Lower: lower, Upper: upper, Not: true}, nil
		}
		if p.peek().Type == lexer.TOKEN_IN {
			p.advance() // NOT
			p.advance() // IN
			if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
				return nil, err
			}
			list, err := p.parseValueListUntilRParen()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}
			return &InExpr{Left: left, Not: true, Right: list}, nil
		}
		if p.peek().Type == lexer.TOKEN_LIKE {
			p.advance() // NOT
			p.advance() // LIKE
			right, err := p.parseAddition()
			if err != nil {
				return nil, err
			}
			return &NotExpr{Expr: &BinaryExpr{Left: left, Operator: "LIKE", Right: right}}, nil
		}
		if p.peek().Type == lexer.TOKEN_EXISTS {
			p.advance() // NOT
			p.advance() // EXISTS
			if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
				return nil, err
			}
			if p.current().Type != lexer.TOKEN_SELECT {
				return nil, fmt.Errorf("expected SELECT after NOT EXISTS (")
			}
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}
			selectStmt, ok := stmt.(*SelectStatement)
			if !ok {
				return nil, fmt.Errorf("expected SELECT statement in NOT EXISTS")
			}
			return &ExistsExpr{Select: selectStmt, Not: true}, nil
		}
		return left, nil
	case lexer.TOKEN_IS:
		p.advance()
		if p.current().Type == lexer.TOKEN_NULL {
			p.advance()
			return &BinaryExpr{Left: left, Operator: "IS", Right: Value{Type: "null"}}, nil
		} else if p.current().Type == lexer.TOKEN_NOT && p.peek().Type == lexer.TOKEN_NULL {
			p.advance()
			p.advance()
			return &BinaryExpr{Left: left, Operator: "IS NOT", Right: Value{Type: "null"}}, nil
		}
		return left, nil
	case lexer.TOKEN_BETWEEN:
		p.advance()
		lower, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_AND, "AND"); err != nil {
			return nil, err
		}
		upper, err := p.parseAddition()
		if err != nil {
			return nil, err
		}
		return &BetweenExpr{Expr: left, Lower: lower, Upper: upper, Not: false}, nil
	default:
		return left, nil
	}
}

func (p *sqlParser) parseAddition() (Expression, error) {
	left, err := p.parseMultiplication()
	if err != nil {
		return nil, err
	}

	for p.current().Type == lexer.TOKEN_PLUS || p.current().Type == lexer.TOKEN_MINUS {
		op := p.current().Literal
		p.advance()
		right, err := p.parseMultiplication()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Operator: op, Right: right}
	}

	return left, nil
}

func (p *sqlParser) parseMultiplication() (Expression, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for p.current().Type == lexer.TOKEN_STAR || p.current().Type == lexer.TOKEN_SLASH {
		op := p.current().Literal
		p.advance()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Left: left, Operator: op, Right: right}
	}

	return left, nil
}

func (p *sqlParser) parsePrimary() (Expression, error) {
	tok := p.current()
	switch tok.Type {
	case lexer.TOKEN_LPAREN:
		p.advance()
		if p.current().Type == lexer.TOKEN_SELECT {
			stmt, err := p.parseSelect()
			if err != nil {
				return nil, err
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}
			return &SubqueryExpr{Query: stmt.(*SelectStatement)}, nil
		}
		expr, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		return expr, nil
	case lexer.TOKEN_EXISTS:
		// EXISTS (SELECT ...)
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		if p.current().Type != lexer.TOKEN_SELECT {
			return nil, fmt.Errorf("expected SELECT after EXISTS (")
		}
		stmt, err := p.parseSelect()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
			return nil, err
		}
		selectStmt, ok := stmt.(*SelectStatement)
		if !ok {
			return nil, fmt.Errorf("expected SELECT statement in EXISTS")
		}
		return &ExistsExpr{Select: selectStmt, Not: false}, nil
	case lexer.TOKEN_CAST:
		return p.parseCast()
	case lexer.TOKEN_CASE:
		return p.parseCase()
	case lexer.TOKEN_LEFT:
		// LEFT(str, n) function
		p.advance()
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			args, err := p.parseValueListUntilRParen()
			if err != nil {
				return nil, err
			}
			return &FunctionCall{Name: "LEFT", Args: args}, nil
		}
		return &ColumnRef{Name: tok.Literal}, nil
	case lexer.TOKEN_RIGHT:
		// RIGHT(str, n) function
		p.advance()
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			args, err := p.parseValueListUntilRParen()
			if err != nil {
				return nil, err
			}
			return &FunctionCall{Name: "RIGHT", Args: args}, nil
		}
		return &ColumnRef{Name: tok.Literal}, nil
	case lexer.TOKEN_CURRENT:
		// CURRENT_DATE, CURRENT_TIME, CURRENT_TIMESTAMP
		p.advance()
		next := p.current()
		switch next.Type {
		case lexer.TOKEN_DATE:
			p.advance()
			return &FunctionCall{Name: "CURRENT_DATE", Args: nil}, nil
		case lexer.TOKEN_TIME:
			p.advance()
			return &FunctionCall{Name: "CURRENT_TIME", Args: nil}, nil
		case lexer.TOKEN_TIMESTAMP:
			p.advance()
			return &FunctionCall{Name: "CURRENT_TIMESTAMP", Args: nil}, nil
		default:
			return nil, p.syntaxError(tok, "expected DATE, TIME, or TIMESTAMP after CURRENT")
		}
	case lexer.TOKEN_STRING_AGG:
		// STRING_AGG(col, delimiter)
		p.advance()
		if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
			return nil, err
		}
		args, err := p.parseValueListUntilRParen()
		if err != nil {
			return nil, err
		}
		return &FunctionCall{Name: "STRING_AGG", Args: args}, nil
	case lexer.TOKEN_IDENT, lexer.TOKEN_COALESCE:
		ident := tok.Literal
		upper := strings.ToUpper(ident)
		p.advance()

		// Handle CURRENT_DATE, CURRENT_TIME, CURRENT_TIMESTAMP
		if upper == "CURRENT_DATE" {
			return &FunctionCall{Name: "CURRENT_DATE", Args: nil}, nil
		}
		if upper == "CURRENT_TIME" {
			return &FunctionCall{Name: "CURRENT_TIME", Args: nil}, nil
		}
		if upper == "CURRENT_TIMESTAMP" {
			return &FunctionCall{Name: "CURRENT_TIMESTAMP", Args: nil}, nil
		}

		if upper == "DATE" && p.peek().Type == lexer.TOKEN_STRING_LIT {
			p.advance()
			val := p.current().Literal
			p.advance()
			return &FunctionCall{Name: "TO_DATE", Args: []Expression{
				&Value{Type: "string", StrVal: val},
				&Value{Type: "string", StrVal: "2006-01-02"},
			}}, nil
		}
		if upper == "TIME" && p.peek().Type == lexer.TOKEN_STRING_LIT {
			p.advance()
			val := p.current().Literal
			p.advance()
			return &FunctionCall{Name: "TO_TIME", Args: []Expression{
				&Value{Type: "string", StrVal: val},
			}}, nil
		}
		if upper == "TIMESTAMP" && p.peek().Type == lexer.TOKEN_STRING_LIT {
			p.advance()
			val := p.current().Literal
			p.advance()
			return &FunctionCall{Name: "TO_TIMESTAMP", Args: []Expression{
				&Value{Type: "string", StrVal: val},
				&Value{Type: "string", StrVal: "2006-01-02 15:04:05"},
			}}, nil
		}

		if p.current().Type == lexer.TOKEN_DOT {
			p.advance() // Consume '.'
			if p.current().Type != lexer.TOKEN_IDENT {
				return nil, p.expectedError("column name after '.'", p.current())
			}
			ident = ident + "." + p.current().Literal
			p.advance()
		}
		if p.current().Type == lexer.TOKEN_LPAREN {
			p.advance()
			args := make([]Expression, 0)
			distinct := false
			if p.current().Type == lexer.TOKEN_IDENT && strings.ToUpper(p.current().Literal) == "DISTINCT" {
				distinct = true
				p.advance()
			} else if p.current().Type == lexer.TOKEN_STAR {
				args = append(args, &ColumnRef{Name: "*"})
				p.advance()
			}

			if len(args) == 0 && p.current().Type != lexer.TOKEN_RPAREN {
				list, err := p.parseValueListUntilRParen()
				if err != nil {
					return nil, err
				}
				args = list
			}
			if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
				return nil, err
			}

			var funcExpr Expression
			nameUp := strings.ToUpper(ident)
			if nameUp == "COUNT" || nameUp == "SUM" || nameUp == "AVG" || nameUp == "MIN" || nameUp == "MAX" {
				funcExpr = &AggregateExpr{Name: nameUp, Args: args, Distinct: distinct}
			} else {
				funcExpr = &FunctionCall{Name: nameUp, Args: args}
			}

			if p.current().Type == lexer.TOKEN_OVER {
				p.advance()
				if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
					return nil, err
				}
				spec, err := p.parseWindowSpec()
				if err != nil {
					return nil, err
				}
				if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
					return nil, err
				}
				return &WindowFunctionExpr{
					FuncName: funcExprName(funcExpr),
					Args:     funcExprArgs(funcExpr),
					Over:     *spec,
				}, nil
			}
			return p.parsePrimaryPost(funcExpr), nil
		}
		return p.parsePrimaryPost(&ColumnRef{Name: ident}), nil

	case lexer.TOKEN_PARAM:
		p.advance()
		idx, err := strconv.Atoi(tok.Literal)
		if err != nil {
			return nil, p.syntaxError(tok, "invalid parameter index")
		}
		return &ParamRef{Index: idx}, nil
	case lexer.TOKEN_INT_LIT, lexer.TOKEN_FLOAT_LIT, lexer.TOKEN_STRING_LIT, lexer.TOKEN_TRUE, lexer.TOKEN_FALSE, lexer.TOKEN_NULL:
		value, err := tokenToValue(tok)
		if err != nil {
			return nil, err
		}
		p.advance()
		return value, nil
	default:
		return nil, p.expectedError("expression", tok)
	}
}

func funcExprName(expr Expression) string {
	switch e := expr.(type) {
	case *AggregateExpr:
		return e.Name
	case *FunctionCall:
		return e.Name
	}
	return ""
}

func funcExprArgs(expr Expression) []Expression {
	switch e := expr.(type) {
	case *AggregateExpr:
		return e.Args
	case *FunctionCall:
		return e.Args
	}
	return nil
}

func (p *sqlParser) parseCast() (Expression, error) {
	p.advance() // CAST
	if err := p.consume(lexer.TOKEN_LPAREN, "'('"); err != nil {
		return nil, err
	}
	expr, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_AS, "AS"); err != nil {
		return nil, err
	}
	targetType, _, err := p.parseColumnType()
	if err != nil {
		return nil, err
	}
	if err := p.consume(lexer.TOKEN_RPAREN, "')'"); err != nil {
		return nil, err
	}
	return &CastExpr{Expr: expr, TargetType: targetType}, nil
}

func (p *sqlParser) parseCase() (Expression, error) {
	p.advance() // CASE

	var base Expression
	if p.current().Type != lexer.TOKEN_WHEN {
		var err error
		base, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	whens := make([]CaseWhen, 0, 4)
	for p.current().Type == lexer.TOKEN_WHEN {
		p.advance() // WHEN
		cond, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		if err := p.consume(lexer.TOKEN_THEN, "THEN"); err != nil {
			return nil, err
		}
		res, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		whens = append(whens, CaseWhen{Condition: cond, Result: res})
	}

	if len(whens) == 0 {
		return nil, p.expectedError("at least one WHEN clause", p.current())
	}

	var elseExpr Expression
	if p.current().Type == lexer.TOKEN_ELSE {
		p.advance() // ELSE
		var err error
		elseExpr, err = p.parseExpression()
		if err != nil {
			return nil, err
		}
	}

	if err := p.consume(lexer.TOKEN_END, "END"); err != nil {
		return nil, err
	}

	return &CaseExpr{Base: base, Whens: whens, Else: elseExpr}, nil
}

func (p *sqlParser) parseLiteralValue() (Value, error) {
	value, err := tokenToValue(p.current())
	if err != nil {
		return Value{}, err
	}
	p.advance()
	return value, nil
}

func (p *sqlParser) parseIdentifierListUntilRParen(context string) ([]string, error) {
	items := make([]string, 0, 4)
	for {
		name, err := p.consumeIdent(context)
		if err != nil {
			return nil, err
		}
		items = append(items, name)

		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		if p.current().Type == lexer.TOKEN_RPAREN {
			break
		}
		return nil, p.expectedError("',' or ')'", p.current())
	}
	return items, nil
}

func (p *sqlParser) parseValueListUntilRParen() ([]Expression, error) {
	items := make([]Expression, 0, 4)
	for {
		value, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		items = append(items, value)

		if p.current().Type == lexer.TOKEN_COMMA {
			p.advance()
			continue
		}
		if p.current().Type == lexer.TOKEN_RPAREN {
			break
		}
		return nil, p.expectedError("',' or ')'", p.current())
	}
	return items, nil
}

func tokenToValue(tok lexer.Token) (Value, error) {
	switch tok.Type {
	case lexer.TOKEN_INT_LIT:
		n, err := strconv.ParseInt(tok.Literal, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("invalid INT literal '%s' at line %d, col %d", tok.Literal, tok.Line, tok.Col)
		}
		return Value{Type: "int", IntVal: n}, nil
	case lexer.TOKEN_FLOAT_LIT:
		f, err := strconv.ParseFloat(tok.Literal, 64)
		if err != nil {
			return Value{}, fmt.Errorf("invalid FLOAT literal '%s' at line %d, col %d", tok.Literal, tok.Line, tok.Col)
		}
		return Value{Type: "float", FltVal: f}, nil
	case lexer.TOKEN_STRING_LIT:
		return Value{Type: "string", StrVal: tok.Literal}, nil
	case lexer.TOKEN_TRUE:
		return Value{Type: "bool", BoolVal: true}, nil
	case lexer.TOKEN_FALSE:
		return Value{Type: "bool", BoolVal: false}, nil
	case lexer.TOKEN_NULL:
		return Value{Type: "null"}, nil
	default:
		return Value{}, fmt.Errorf("syntax error at line %d, col %d: expected literal value, got '%s'", tok.Line, tok.Col, tokenDescription(tok))
	}
}

func (p *sqlParser) consume(tokenType lexer.TokenType, expected string) error {
	if p.current().Type != tokenType {
		return p.expectedError(expected, p.current())
	}
	p.advance()
	return nil
}

func (p *sqlParser) consumeIdent(expected string) (string, error) {
	tok := p.current()
	if tok.Type != lexer.TOKEN_IDENT {
		return "", p.expectedError(expected, tok)
	}
	p.advance()
	return tok.Literal, nil
}

func (p *sqlParser) current() lexer.Token {
	if p.pos >= len(p.tokens) {
		return lexer.Token{Type: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos]
}

func (p *sqlParser) peek() lexer.Token {
	if p.pos+1 >= len(p.tokens) {
		return lexer.Token{Type: lexer.TOKEN_EOF}
	}
	return p.tokens[p.pos+1]
}

func (p *sqlParser) advance() {
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
}

func (p *sqlParser) expectedError(expected string, got lexer.Token) error {
	if got.Type == lexer.TOKEN_EOF {
		return fmt.Errorf("syntax error: unexpected end of input, expected %s", expected)
	}
	return fmt.Errorf("syntax error at line %d, col %d: expected %s, got '%s'", got.Line, got.Col, expected, tokenDescription(got))
}

func (p *sqlParser) syntaxError(tok lexer.Token, message string) error {
	if tok.Type == lexer.TOKEN_EOF {
		return fmt.Errorf("syntax error: %s", message)
	}
	return fmt.Errorf("syntax error at line %d, col %d: %s", tok.Line, tok.Col, message)
}

func tokenDescription(tok lexer.Token) string {
	if tok.Literal != "" {
		return tok.Literal
	}
	if tok.Type == lexer.TOKEN_EOF {
		return "end of input"
	}
	return tok.Type.String()
}

func isReservedKeyword(s string) bool {
	upper := strings.ToUpper(s)
	switch upper {
	case "FROM", "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "OFFSET", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS":
		return true
	}
	return false
}

func (p *sqlParser) parsePrimaryPost(left Expression) Expression {
	for {
		tok := p.current()
		if tok.Type != lexer.TOKEN_ARROW && tok.Type != lexer.TOKEN_DBL_ARROW {
			break
		}
		op := tok.Literal
		p.advance()

		pathTok := p.current()
		if pathTok.Type != lexer.TOKEN_STRING_LIT {
			return left
		}
		path := pathTok.Literal
		p.advance()

		left = &JsonPathExpr{Left: left, Op: op, Path: path}
	}
	return left
}
