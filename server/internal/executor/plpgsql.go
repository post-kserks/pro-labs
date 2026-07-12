package executor

import (
	"fmt"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// plpgsqlResult captures the outcome of a PL/pgSQL function body execution.
type plpgsqlResult struct {
	scalar interface{} // non-nil for RETURN expression
	rows   *Result     // non-nil for RETURN QUERY
}

// ExecutePLPGSQL interprets a minimal PL/pgSQL function body.
func ExecutePLPGSQL(body string, params []string, args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	// Strip outer BEGIN...END block if present
	inner := stripBeginEnd(body)

	// Parse DECLARE section and body statements
	declaredVars, stmts := parsePLPGSQLBody(inner)

	// Build variable map from parameters
	vars := make(map[string]interface{})
	for i, p := range params {
		if i < len(args) {
			vars[strings.ToLower(p)] = args[i]
		} else {
			vars[strings.ToLower(p)] = nil
		}
	}
	// Add declared variables (nil initial value)
	for _, v := range declaredVars {
		if _, exists := vars[v]; !exists {
			vars[v] = nil
		}
	}

	// Execute statements
	for _, stmt := range stmts {
		res, err := executePLPGSQLStmt(stmt, vars, ctx)
		if err != nil {
			return nil, err
		}
		if res != nil {
			if res.scalar != nil {
				return res.scalar, nil
			}
			if res.rows != nil {
				return res.rows, nil
			}
		}
	}

	return nil, nil
}

// stripBeginEnd removes the outermost BEGIN...END wrapper if present.
func stripBeginEnd(body string) string {
	trimmed := strings.TrimSpace(body)
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "BEGIN") {
		rest := strings.TrimSpace(trimmed[5:])
		// Ensure we match the trailing END; (with optional semicolon)
		if strings.HasSuffix(strings.ToUpper(rest), "END;") {
			rest = strings.TrimSpace(rest[:len(rest)-4])
		} else if strings.HasSuffix(strings.ToUpper(rest), "END") {
			rest = strings.TrimSpace(rest[:len(rest)-3])
		}
		return rest
	}
	return trimmed
}

// parsePLPGSQLBody splits a PL/pgSQL body into declared variables and statements.
func parsePLPGSQLBody(body string) ([]string, []string) {
	trimmed := strings.TrimSpace(body)
	upper := strings.ToUpper(trimmed)

	var declaredVars []string
	var stmtsBody string

	// Check for DECLARE section
	if strings.HasPrefix(upper, "DECLARE") {
		rest := strings.TrimSpace(trimmed[7:])
		// Find where DECLARE section ends (BEGIN or first statement keyword)
		bodyStart := -1
		for _, kw := range []string{"BEGIN", "RETURN", "IF", "LOOP", "FOR", "WHILE"} {
			idx := findKeyword(rest, kw)
			if idx >= 0 && (bodyStart < 0 || idx < bodyStart) {
				bodyStart = idx
			}
		}
		if bodyStart >= 0 {
			declPart := strings.TrimSpace(rest[:bodyStart])
			stmtsBody = strings.TrimSpace(rest[bodyStart:])
			declaredVars = parseDeclarations(declPart)
		} else {
			stmtsBody = rest
		}
	} else {
		stmtsBody = trimmed
	}

	stmts := splitPLPGSQLStatements(stmtsBody)
	return declaredVars, stmts
}

// findKeyword finds the position of a keyword at a word boundary.
func findKeyword(s, keyword string) int {
	upper := strings.ToUpper(s)
	upperKW := strings.ToUpper(keyword)
	idx := 0
	for idx < len(upper) {
		pos := strings.Index(upper[idx:], upperKW)
		if pos < 0 {
			return -1
		}
		absPos := idx + pos
		// Check word boundaries
		beforeOK := absPos == 0 || !isIdentChar(upper[absPos-1])
		afterPos := absPos + len(upperKW)
		afterOK := afterPos >= len(upper) || !isIdentChar(upper[afterPos])
		if beforeOK && afterOK {
			return absPos
		}
		idx = absPos + len(upperKW)
	}
	return -1
}

func isIdentChar(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_'
}

// parseDeclarations parses variable declarations from the DECLARE section.
func parseDeclarations(decl string) []string {
	var vars []string
	lines := strings.Split(decl, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToUpper(line), "--") {
			continue
		}
		// Format: var_name TYPE [:= expr]
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			name := strings.ToLower(parts[0])
			// Skip if it looks like a keyword
			switch strings.ToUpper(name) {
			case "BEGIN", "END", "DECLARE", "RETURN", "IF", "LOOP", "FOR", "WHILE":
				continue
			}
			vars = append(vars, name)
		}
	}
	return vars
}

// splitPLPGSQLStatements splits a PL/pgSQL body into individual statements on semicolons.
func splitPLPGSQLStatements(body string) []string {
	var stmts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for _, ch := range body {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' && (inSingleQuote || inDoubleQuote) {
			current.WriteRune(ch)
			escaped = true
			continue
		}
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			current.WriteRune(ch)
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			current.WriteRune(ch)
			continue
		}
		if ch == ';' && !inSingleQuote && !inDoubleQuote {
			s := strings.TrimSpace(current.String())
			if s != "" {
				stmts = append(stmts, s)
			}
			current.Reset()
			continue
		}
		current.WriteRune(ch)
	}
	s := strings.TrimSpace(current.String())
	if s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

// executePLPGSQLStmt executes a single PL/pgSQL statement.
func executePLPGSQLStmt(stmt string, vars map[string]interface{}, ctx *ExecutionContext) (*plpgsqlResult, error) {
	upper := strings.ToUpper(strings.TrimSpace(stmt))

	// Variable assignment: var := expr
	if idx := strings.Index(strings.ToUpper(stmt), ":="); idx > 0 {
		varName := strings.ToLower(strings.TrimSpace(stmt[:idx]))
		exprStr := strings.TrimSpace(stmt[idx+2:])
		val, err := evalPLPGSQLExpr(exprStr, vars, ctx)
		if err != nil {
			return nil, fmt.Errorf("assignment to %s: %w", varName, err)
		}
		vars[varName] = val
		return nil, nil
	}

	// RETURN expression
	if strings.HasPrefix(upper, "RETURN") {
		rest := strings.TrimSpace(stmt[6:])
		upperRest := strings.ToUpper(rest)

		// RETURN QUERY SELECT ...
		if strings.HasPrefix(upperRest, "QUERY") {
			queryStr := strings.TrimSpace(rest[5:])
			return executeReturnQuery(queryStr, vars, ctx)
		}

		// RETURN expr
		if rest == "" {
			return &plpgsqlResult{scalar: nil}, nil
		}
		val, err := evalPLPGSQLExpr(rest, vars, ctx)
		if err != nil {
			return nil, fmt.Errorf("RETURN: %w", err)
		}
		return &plpgsqlResult{scalar: val}, nil
	}

	return nil, nil
}

// evalPLPGSQLExpr evaluates a PL/pgSQL expression using the existing eval machinery.
func evalPLPGSQLExpr(exprStr string, vars map[string]interface{}, ctx *ExecutionContext) (interface{}, error) {
	expr, err := parser.ParseExpression(exprStr)
	if err != nil {
		return nil, fmt.Errorf("parse expression: %w", err)
	}

	// Build a synthetic schema and row from the variable map
	schema, row := buildVarSchemaRow(vars)
	return evalOperand(expr, row, schema, ctx)
}

// buildVarSchemaRow creates a synthetic TableSchema and Row from a variable map.
// This allows the existing evalOperand to resolve variable references as column lookups.
func buildVarSchemaRow(vars map[string]interface{}) (*storage.TableSchema, storage.Row) {
	// Use a deterministic ordering: sort keys
	sorted := make([]string, 0, len(vars))
	for k := range vars {
		sorted = append(sorted, k)
	}
	// Simple insertion sort for small slices
	for i := 1; i < len(sorted); i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > key {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}

	cols := make([]storage.ColumnSchema, len(sorted))
	row := make(storage.Row, len(sorted))
	for i, name := range sorted {
		val := vars[name]
		cols[i] = storage.ColumnSchema{
			Name: name,
			Type: inferVarType(val),
		}
		row[i] = val
	}
	return &storage.TableSchema{Columns: cols}, row
}

// inferVarType returns a type string for a variable value.
func inferVarType(val interface{}) string {
	if val == nil {
		return "TEXT"
	}
	switch val.(type) {
	case int, int64:
		return "INT"
	case float64:
		return "FLOAT"
	case bool:
		return "BOOL"
	case string:
		return "TEXT"
	default:
		return "TEXT"
	}
}

// executeReturnQuery handles RETURN QUERY SELECT ... by executing the SELECT
// and packaging results as a Result.
func executeReturnQuery(queryStr string, vars map[string]interface{}, ctx *ExecutionContext) (*plpgsqlResult, error) {
	// Add trailing semicolon if missing
	if !strings.HasSuffix(strings.TrimSpace(queryStr), ";") {
		queryStr += ";"
	}

	// Substitute variable references into the query
	substituted := substituteVarsInQuery(queryStr, vars)

	stmt, err := parser.Parse(substituted)
	if err != nil {
		return nil, fmt.Errorf("RETURN QUERY: parse error: %w", err)
	}

	cmd, err := CommandFactory(stmt)
	if err != nil {
		return nil, fmt.Errorf("RETURN QUERY: %w", err)
	}

	result, err := cmd.Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("RETURN QUERY: %w", err)
	}

	return &plpgsqlResult{rows: result}, nil
}

// substituteVarsInQuery replaces variable references in a query string with their literal values.
func substituteVarsInQuery(query string, vars map[string]interface{}) string {
	// Build replacement map: only replace standalone identifiers that match var names
	result := query
	// Sort by length descending to replace longer names first
	type kv struct{ k, v string }
	var replacements []kv
	for name, val := range vars {
		lit := literalSQLValue(val)
		replacements = append(replacements, kv{name, lit})
	}
	// Sort descending by key length
	for i := 0; i < len(replacements); i++ {
		for j := i + 1; j < len(replacements); j++ {
			if len(replacements[j].k) > len(replacements[i].k) {
				replacements[i], replacements[j] = replacements[j], replacements[i]
			}
		}
	}
	for _, r := range replacements {
		// Replace whole-word occurrences only (not inside quotes or as part of larger identifiers)
		result = replaceWholeWord(result, r.k, r.v)
	}
	return result
}

// replaceWholeWord replaces occurrences of word in s with replacement,
// only when the word is not part of a larger identifier.
func replaceWholeWord(s, word, replacement string) string {
	lowerWord := strings.ToLower(word)
	var result strings.Builder
	i := 0
	for i < len(s) {
		if i+len(lowerWord) <= len(s) && strings.ToLower(s[i:i+len(lowerWord)]) == lowerWord {
			// Check boundaries
			beforeOK := i == 0 || !isIdentChar(s[i-1])
			end := i + len(lowerWord)
			afterOK := end >= len(s) || !isIdentChar(s[end])
			if beforeOK && afterOK {
				result.WriteString(replacement)
				i = end
				continue
			}
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

// literalSQLValue converts a Go value to a SQL literal string.
func literalSQLValue(val interface{}) string {
	if val == nil {
		return "NULL"
	}
	switch v := val.(type) {
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%g", v)
	default:
		return fmt.Sprintf("'%v'", v)
	}
}
