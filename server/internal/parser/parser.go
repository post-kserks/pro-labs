package parser

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"vaultdb/internal/lexer"
)

// defaultGlobalCache is the package-level cache used by ParseCached.
var defaultGlobalCache = NewStatementCache(defaultStatementCacheCapacity)

// Parse parses one SQL statement terminated by ';'.
func Parse(sql string) (Statement, error) {
	stmt, err := parse(sql)
	if err != nil {
		slog.Debug("parse error", "input", sql, "error", err)
		return nil, fmt.Errorf("invalid query syntax")
	}
	return stmt, nil
}

// ParseCached parses SQL with a cache lookup first. Uses the package-level cache.
func ParseCached(sql string) (Statement, error) {
	return ParseCachedWith(sql, defaultGlobalCache)
}

// ParseCachedWith parses SQL using the provided StatementCache.
func ParseCachedWith(sql string, cache *StatementCache) (Statement, error) {
	if stmt, ok := cache.Get(sql); ok {
		return stmt, nil
	}
	stmt, err := Parse(sql)
	if err != nil {
		return nil, err
	}
	cache.Put(sql, stmt)
	return stmt, nil
}

func trimAndLower(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
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
		if e.Table != "" {
			return fmt.Sprintf("%s.%s", e.Table, e.Name)
		}
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
	case *ParamRef:
		return fmt.Sprintf("$%d", e.Index)
	case *FunctionCall:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = FormatExpression(a)
		}
		return fmt.Sprintf("%s(%s)", e.Name, strings.Join(args, ", "))
	case *AggregateExpr:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = FormatExpression(a)
		}
		distinct := ""
		if e.Distinct {
			distinct = "DISTINCT "
		}
		return fmt.Sprintf("%s(%s%s)", e.Name, distinct, strings.Join(args, ", "))
	case *CastExpr:
		return fmt.Sprintf("CAST(%s AS %s)", FormatExpression(e.Expr), e.TargetType)
	case *CaseExpr:
		var sb strings.Builder
		sb.WriteString("CASE")
		if e.Base != nil {
			sb.WriteString(" ")
			sb.WriteString(FormatExpression(e.Base))
		}
		for _, w := range e.Whens {
			sb.WriteString(fmt.Sprintf(" WHEN %s THEN %s", FormatExpression(w.Condition), FormatExpression(w.Result)))
		}
		if e.Else != nil {
			sb.WriteString(fmt.Sprintf(" ELSE %s", FormatExpression(e.Else)))
		}
		sb.WriteString(" END")
		return sb.String()
	case *SubqueryExpr:
		if e.Query != nil {
			return fmt.Sprintf("(%s)", e.Query.StatementType())
		}
		return "(SUBQUERY)"
	case *ExistsExpr:
		not := ""
		if e.Not {
			not = "NOT "
		}
		if e.Select != nil {
			return fmt.Sprintf("%sEXISTS (%s)", not, e.Select.StatementType())
		}
		return fmt.Sprintf("%sEXISTS (SUBQUERY)", not)
	case *BetweenExpr:
		not := ""
		if e.Not {
			not = "NOT "
		}
		return fmt.Sprintf("%s %sBETWEEN %s AND %s", FormatExpression(e.Expr), not, FormatExpression(e.Lower), FormatExpression(e.Upper))
	case *JsonPathExpr:
		return fmt.Sprintf("%s%s'%s'", FormatExpression(e.Left), e.Op, e.Path)
	case *JSONAccess:
		return fmt.Sprintf("%s %s %s", FormatExpression(e.Expr), e.Operator, FormatExpression(e.Argument))
	case *WindowFunctionExpr:
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = FormatExpression(a)
		}
		var parts []string
		if len(e.Over.PartitionBy) > 0 {
			partCols := make([]string, len(e.Over.PartitionBy))
			for i, p := range e.Over.PartitionBy {
				partCols[i] = FormatExpression(p)
			}
			parts = append(parts, fmt.Sprintf("PARTITION BY %s", strings.Join(partCols, ", ")))
		}
		if len(e.Over.OrderBy) > 0 {
			orderItems := make([]string, len(e.Over.OrderBy))
			for i, o := range e.Over.OrderBy {
				dir := ""
				if o.Direction != "" {
					dir = " " + o.Direction
				}
				orderItems[i] = fmt.Sprintf("%s%s", FormatExpression(o.Expr), dir)
			}
			parts = append(parts, fmt.Sprintf("ORDER BY %s", strings.Join(orderItems, ", ")))
		}
		overClause := ""
		if len(parts) > 0 {
			overClause = fmt.Sprintf(" OVER (%s)", strings.Join(parts, " "))
		}
		return fmt.Sprintf("%s(%s)%s", e.FuncName, strings.Join(args, ", "), overClause)
	case *ComparisonSubqueryExpr:
		return fmt.Sprintf("%s %s %s (SUBQUERY)", FormatExpression(e.Left), e.Operator, e.Quantifier)
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

func normalizeWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func parse(sql string) (Statement, error) {
	const maxInputSize = 10 * 1024 * 1024 // 10MB
	if len(sql) > maxInputSize {
		return nil, fmt.Errorf("query too large (%d bytes, max 10MB)", len(sql))
	}
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return nil, fmt.Errorf("syntax error: empty query")
	}
	sql = normalizeWhitespace(sql)
	if !strings.HasSuffix(sql, ";") {
		sql = sql + ";"
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

const defaultMaxParserDepth = 32

type sqlParser struct {
	tokens   []lexer.Token
	pos      int
	depth    int
	maxDepth int
}

func (p *sqlParser) checkDepth() error {
	p.depth++
	if p.maxDepth <= 0 {
		p.maxDepth = defaultMaxParserDepth
	}
	if p.depth > p.maxDepth {
		return fmt.Errorf("query too deeply nested (max depth %d)", p.maxDepth)
	}
	return nil
}

func (p *sqlParser) exitDepth() {
	if p.depth > 0 {
		p.depth--
	}
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
	case lexer.TOKEN_COPY:
		stmt, err = p.parseCopy()
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
	case lexer.TOKEN_VERIFY:
		stmt, err = p.parseVerifyAuditLog()
	case lexer.TOKEN_GRANT:
		stmt, err = p.parseGrant()
	case lexer.TOKEN_REVOKE:
		stmt, err = p.parseRevoke()
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
