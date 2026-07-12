package executor

import (
	"context"
	crypto_rand "crypto/rand"
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/fts"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/wasmudf"
)

// builtinFunc — function type for builtin SQL functions.
type builtinFunc func(args []interface{}, ctx *ExecutionContext) (interface{}, error)

// extendedBuiltinFunc — function type for builtins that need row/schema access.
type extendedBuiltinFunc func(args []interface{}, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error)

// builtinFuncs — map of builtin SQL functions.
var builtinFuncs = map[string]builtinFunc{
	"NOW":                  fnNow,
	"UPPER":                fnUpper,
	"LOWER":                fnLower,
	"CONCAT":               fnConcat,
	"LENGTH":               fnLength,
	"LEN":                  fnLength,
	"SUBSTRING":            fnSubstring,
	"SUBSTR":               fnSubstring,
	"TRIM":                 fnTrim,
	"LTRIM":                fnLtrim,
	"RTRIM":                fnRtrim,
	"REPLACE":              fnReplace,
	"POSITION":             fnPosition,
	"LEFT":                 fnLeft,
	"RIGHT":                fnRight,
	"LPAD":                 fnLpad,
	"RPAD":                 fnRpad,
	"REVERSE":              fnReverse,
	"INITCAP":              fnInitcapBuiltin,
	"MOD":                  fnMod,
	"POWER":                fnPower,
	"POW":                  fnPower,
	"SQRT":                 fnSqrt,
	"LN":                   fnLn,
	"LOG":                  fnLog,
	"LOG10":                fnLog,
	"EXP":                  fnExp,
	"SIGN":                 fnSign,
	"ABS":                  fnAbs,
	"CEIL":                 fnCeil,
	"CEILING":              fnCeil,
	"FLOOR":                fnFloor,
	"ROUND":                fnRound,
	"COALESCE":             fnCoalesce,
	"NULLIF":               fnNullif,
	"GREATEST":             fnGreatest,
	"LEAST":                fnLeast,
	"JSON_OBJECT":          fnJsonObject,
	"JSON_ARRAY":           fnJsonArray,
	"JSON_EXTRACT":         fnJsonExtract,
	"CURRENT_DATE":         fnCurrentDate,
	"CURRENT_TIME":         fnCurrentTime,
	"CURRENT_TIMESTAMP":    fnCurrentTimestamp,
	"DATE_TRUNC":           fnDateTrunc,
	"EXTRACT":              fnExtract,
	"ARRAY_APPEND":         fnArrayAppend,
	"ARRAY_LENGTH":         fnArrayLength,
	"ARRAY_CONTAINS":       fnArrayContains,
	"ARRAY_TO_STRING":      fnArrayToString,
	"ARRAY_SLICE":          fnArraySlice,
	"AGE":                  fnAge,
	"TO_DATE":              fnToDate,
	"TO_CHAR":              fnToChar,
	"TO_TIMESTAMP":         fnToTimestamp,
	"DATE_ADD":             fnDateAdd,
	"DATE_SUB":             fnDateSub,
	"DATE_DIFF":            fnDateDiff,
	"AI_EMBED":             fnAiEmbed,
	"UUID":                 fnUuid,
	"INTERVAL":             fnInterval,
	"JSONB_BUILD_OBJECT":   fnJsonbBuildObject,
	"JSONB_BUILD_ARRAY":    fnJsonbBuildArray,
	"JSONB_ARRAY_ELEMENTS": fnJsonbArrayElements,
	"JSONB_TYPEOF":         fnJsonbTypeof,
	"JSONB_SET":            fnJsonbSet,
	"JSONB_EXTRACT_PATH":   fnJsonbExtractPath,
}

// extendedBuiltinFuncs — functions that need row/schema context (not just args).
var extendedBuiltinFuncs = map[string]extendedBuiltinFunc{
	"BM25_SCORE": fnBm25Score,
}

// evalFunctionCall evaluates a function call.
func evalFunctionCall(fn *parser.FunctionCall, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	args := make([]interface{}, len(fn.Args))
	for i, arg := range fn.Args {
		val, err := evalOperand(arg, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		args[i] = val
	}

	name := strings.ToUpper(fn.Name)
	if efn, ok := extendedBuiltinFuncs[name]; ok {
		return efn(args, row, schema, ctx)
	}
	if fn, ok := builtinFuncs[name]; ok {
		return fn(args, ctx)
	}
	if ctx != nil && ctx.Session != nil {
		db := ctx.Session.CurrentDatabase()
		if db != "" {
			if result, err := executeUserDefinedFunction(db, name, args, ctx); err == nil {
				return result, nil
			}
		}
	}
	return nil, fmt.Errorf("unknown function: %s", name)
}

// executeSubquery executes a scalar subquery.
func executeSubquery(sub *parser.SubqueryExpr, outerRow storage.Row, outerSchema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	var cmd parser.Statement
	if sel, ok := sub.Query.(*parser.SelectStatement); ok {
		subCopy := *sel
		if outerRow != nil && outerSchema != nil && subCopy.Where != nil {
			subCopy.Where = injectOuterColumns(subCopy.Where, outerRow, outerSchema)
		}
		cmd = &subCopy
	} else {
		cmd = sub.Query
	}

	command, err := CommandFactory(cmd)
	if err != nil {
		return nil, err
	}

	res, err := command.Execute(ctx)
	if err != nil {
		return nil, err
	}

	if len(res.Rows) == 0 {
		return nil, nil
	}
	if len(res.Rows) > 1 {
		return nil, fmt.Errorf("scalar subquery returned more than one row")
	}
	if len(res.Rows[0]) != 1 {
		return nil, fmt.Errorf("scalar subquery returned more than one column")
	}

	val := res.Rows[0][0]
	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f, nil
	}

	return val, nil
}

// injectOuterColumns injects outer column values into a subquery.
func injectOuterColumns(expr parser.Expression, outerRow storage.Row, outerSchema *storage.TableSchema) parser.Expression {
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		left := injectOuterColumns(e.Left, outerRow, outerSchema)
		right := injectOuterColumns(e.Right, outerRow, outerSchema)
		return &parser.BinaryExpr{Left: left, Operator: e.Operator, Right: right}
	case *parser.AndExpr:
		return &parser.AndExpr{
			Left:  injectOuterColumns(e.Left, outerRow, outerSchema),
			Right: injectOuterColumns(e.Right, outerRow, outerSchema),
		}
	case *parser.OrExpr:
		return &parser.OrExpr{
			Left:  injectOuterColumns(e.Left, outerRow, outerSchema),
			Right: injectOuterColumns(e.Right, outerRow, outerSchema),
		}
	case *parser.NotExpr:
		return &parser.NotExpr{Expr: injectOuterColumns(e.Expr, outerRow, outerSchema)}
	case *parser.ColumnRef:
		for i, col := range outerSchema.Columns {
			if strings.EqualFold(col.Name, e.Name) && i < len(outerRow) {
				val := outerRow[i]
				switch v := val.(type) {
				case int64:
					return &parser.Value{Type: "int", IntVal: v}
				case float64:
					return &parser.Value{Type: "float", FltVal: v}
				case string:
					return &parser.Value{Type: "string", StrVal: v}
				case bool:
					return &parser.Value{Type: "bool", BoolVal: v}
				default:
					return &parser.Value{Type: "string", StrVal: fmt.Sprintf("%v", v)}
				}
			}
			parts := strings.SplitN(col.Name, ".", 2)
			if len(parts) == 2 && strings.EqualFold(parts[1], e.Name) && i < len(outerRow) {
				val := outerRow[i]
				switch v := val.(type) {
				case int64:
					return &parser.Value{Type: "int", IntVal: v}
				case float64:
					return &parser.Value{Type: "float", FltVal: v}
				case string:
					return &parser.Value{Type: "string", StrVal: v}
				case bool:
					return &parser.Value{Type: "bool", BoolVal: v}
				default:
					return &parser.Value{Type: "string", StrVal: fmt.Sprintf("%v", v)}
				}
			}
		}
		return e
	case *parser.InExpr:
		left := injectOuterColumns(e.Left, outerRow, outerSchema)
		rights := make([]parser.Expression, len(e.Right))
		for i, r := range e.Right {
			rights[i] = injectOuterColumns(r, outerRow, outerSchema)
		}
		return &parser.InExpr{Left: left, Right: rights, Not: e.Not}
	case *parser.ExistsExpr:
		return e
	case *parser.SubqueryExpr:
		return e
	case *parser.Value:
		return e
	case *parser.FunctionCall:
		args := make([]parser.Expression, len(e.Args))
		for i, a := range e.Args {
			args[i] = injectOuterColumns(a, outerRow, outerSchema)
		}
		return &parser.FunctionCall{Name: e.Name, Args: args}
	case *parser.AggregateExpr:
		return e
	default:
		return expr
	}
}

// parseJSONArray parses a JSON array.
func parseJSONArray(s string) []interface{} {
	raw, err := storage.DecodeJSON([]byte(s))
	if err != nil {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	return arr
}

// generateUUID generates a UUID v4.
func generateUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate UUID: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// executeUserDefinedFunction executes a user-defined function.
func executeUserDefinedFunction(dbName, funcName string, args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	fd, err := loadObject(ctx, dbName, objTypeFunction, funcName)
	if err != nil {
		return nil, err
	}
	if fd == nil {
		return nil, fmt.Errorf("function '%s' not found", funcName)
	}
	body, _ := fd["body"].(string)
	params, _ := fd["params"].([]interface{})
	language, _ := fd["language"].(string)
	opts, _ := fd["options"].(map[string]string)

	if body == "" {
		return nil, fmt.Errorf("function '%s' has no body", funcName)
	}

	// WASM UDF path
	if strings.EqualFold(language, "wasm") {
		return executeWASMFunction(body, opts, args)
	}

	// PL/pgSQL UDF path
	if strings.EqualFold(language, "plpgsql") {
		paramNames := make([]string, len(params))
		for i, p := range params {
			paramNames[i], _ = p.(string)
		}
		return ExecutePLPGSQL(body, paramNames, args, ctx)
	}

	// SQL UDF path
	bodyStmt, err := parser.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("function '%s' body parse: %w", funcName, err)
	}

	sel, ok := bodyStmt.(*parser.SelectStatement)
	if !ok {
		return nil, fmt.Errorf("function '%s' body must be a SELECT", funcName)
	}

	if sel.Where != nil {
		boundWhere := sel.Where
		for i, p := range params {
			paramName, _ := p.(string)
			if paramName != "" && i < len(args) {
				boundWhere = substituteParam(boundWhere, paramName, args[i])
			}
		}
		sel.Where = boundWhere
	}

	cmd := &SelectCommand{stmt: sel}
	res, err := cmd.Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("function '%s': %w", funcName, err)
	}
	if len(res.Rows) == 0 {
		return nil, nil
	}
	if len(res.Rows[0]) == 0 {
		return nil, nil
	}
	val := res.Rows[0][0]
	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f, nil
	}
	return val, nil
}

// executeWASMFunction runs a WASM user-defined function.
func executeWASMFunction(wasmPath string, opts map[string]string, args []interface{}) (interface{}, error) {
	// Strip file:// prefix if present
	wasmPath = strings.TrimPrefix(wasmPath, "file://")

	memLimit, timeout, err := wasmudf.ParseOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("WASM options: %w", err)
	}

	// Convert memory limit from bytes to pages (64 KB each) for wazero.
	var maxPages uint32
	if memLimit > 0 {
		maxPages = memLimit / (64 * 1024)
		if maxPages == 0 {
			maxPages = 1 // enforce at least 1 page if limit < 64 KB
		}
	}

	rt, err := wasmudf.NewRuntimeWithLimits(maxPages, timeout)
	if err != nil {
		return nil, fmt.Errorf("WASM runtime: %w", err)
	}
	defer rt.Close()

	fn, err := rt.LoadModule(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("WASM load: %w", err)
	}
	fn.Timeout = timeout

	return fn.Call(context.Background(), args)
}

// substituteParam substitutes a parameter value into an expression.
func substituteParam(expr parser.Expression, paramName string, paramValue interface{}) parser.Expression {
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		return &parser.BinaryExpr{
			Left:     substituteParam(e.Left, paramName, paramValue),
			Operator: e.Operator,
			Right:    substituteParam(e.Right, paramName, paramValue),
		}
	case *parser.ColumnRef:
		if strings.EqualFold(e.Name, paramName) {
			switch v := paramValue.(type) {
			case int64:
				return &parser.Value{Type: "int", IntVal: v}
			case float64:
				return &parser.Value{Type: "float", FltVal: v}
			case string:
				return &parser.Value{Type: "string", StrVal: v}
			case bool:
				return &parser.Value{Type: "bool", BoolVal: v}
			default:
				return &parser.Value{Type: "string", StrVal: fmt.Sprintf("%v", v)}
			}
		}
		return e
	case *parser.AndExpr:
		return &parser.AndExpr{
			Left:  substituteParam(e.Left, paramName, paramValue),
			Right: substituteParam(e.Right, paramName, paramValue),
		}
	case *parser.OrExpr:
		return &parser.OrExpr{
			Left:  substituteParam(e.Left, paramName, paramValue),
			Right: substituteParam(e.Right, paramName, paramValue),
		}
	default:
		return expr
	}
}

// ─── String Functions ───────────────────────────────────────────────────────

func fnUpper(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("UPPER requires 1 argument")
	}
	if s, ok := args[0].(string); ok {
		return strings.ToUpper(s), nil
	}
	return nil, fmt.Errorf("UPPER requires string argument")
}

func fnLower(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LOWER requires 1 argument")
	}
	if s, ok := args[0].(string); ok {
		return strings.ToLower(s), nil
	}
	return nil, fmt.Errorf("LOWER requires string argument")
}

func fnConcat(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	var sb strings.Builder
	for _, arg := range args {
		sb.WriteString(valueToString(arg))
	}
	return sb.String(), nil
}

func fnLength(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LENGTH requires 1 argument")
	}
	s := valueToString(args[0])
	return int64(len([]rune(s))), nil
}

func fnSubstring(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("SUBSTRING requires 2 or 3 arguments")
	}
	s := valueToString(args[0])
	start, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("SUBSTRING start must be integer")
	}
	length := int64(len([]rune(s)))
	if len(args) == 3 {
		if l, ok := toInt64(args[2]); ok {
			length = l
		}
	}
	runes := []rune(s)
	start--
	if start < 0 {
		start = 0
	}
	if start >= int64(len(runes)) {
		return "", nil
	}
	end := start + length
	if end > int64(len(runes)) {
		end = int64(len(runes))
	}
	return string(runes[start:end]), nil
}

func fnTrim(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("TRIM requires 1 argument")
	}
	return strings.TrimSpace(valueToString(args[0])), nil
}

func fnLtrim(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LTRIM requires 1 argument")
	}
	return strings.TrimLeft(valueToString(args[0]), " \t\n\r"), nil
}

func fnRtrim(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("RTRIM requires 1 argument")
	}
	return strings.TrimRight(valueToString(args[0]), " \t\n\r"), nil
}

func fnReplace(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("REPLACE requires 3 arguments")
	}
	return strings.ReplaceAll(valueToString(args[0]), valueToString(args[1]), valueToString(args[2])), nil
}

func fnPosition(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("POSITION requires 2 arguments")
	}
	substr := valueToString(args[0])
	s := valueToString(args[1])
	runes := []rune(s)
	subRunes := []rune(substr)
	for i := 0; i <= len(runes)-len(subRunes); i++ {
		if string(runes[i:i+len(subRunes)]) == substr {
			return int64(i + 1), nil
		}
	}
	return int64(0), nil
}

func fnLeft(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("LEFT requires 2 arguments")
	}
	s := valueToString(args[0])
	n, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("LEFT count must be integer")
	}
	runes := []rune(s)
	if n > int64(len(runes)) {
		n = int64(len(runes))
	}
	if n < 0 {
		n = 0
	}
	return string(runes[:n]), nil
}

func fnRight(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("RIGHT requires 2 arguments")
	}
	s := valueToString(args[0])
	n, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("RIGHT count must be integer")
	}
	runes := []rune(s)
	if n > int64(len(runes)) {
		n = int64(len(runes))
	}
	if n < 0 {
		n = 0
	}
	return string(runes[len(runes)-int(n):]), nil
}

func fnLpad(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("LPAD requires 2 or 3 arguments")
	}
	s := valueToString(args[0])
	length, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("LPAD length must be integer")
	}
	pad := " "
	if len(args) == 3 {
		pad = valueToString(args[2])
	}
	runes := []rune(s)
	if int64(len(runes)) >= length {
		return string(runes[:length]), nil
	}
	padRunes := []rune(pad)
	if len(padRunes) == 0 {
		return nil, fmt.Errorf("LPAD: pad string must not be empty")
	}
	var result []rune
	remaining := length - int64(len(runes))
	for int64(len(result)) < remaining {
		result = append(result, padRunes...)
	}
	result = result[:remaining]
	return string(append(result, runes...)), nil
}

func fnRpad(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("RPAD requires 2 or 3 arguments")
	}
	s := valueToString(args[0])
	length, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("RPAD length must be integer")
	}
	pad := " "
	if len(args) == 3 {
		pad = valueToString(args[2])
	}
	runes := []rune(s)
	if int64(len(runes)) >= length {
		return string(runes[:length]), nil
	}
	padRunes := []rune(pad)
	if len(padRunes) == 0 {
		return nil, fmt.Errorf("RPAD: pad string must not be empty")
	}
	result := make([]rune, 0, length)
	result = append(result, runes...)
	for int64(len(result)) < length {
		result = append(result, padRunes...)
	}
	return string(result[:length]), nil
}

func fnReverse(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("REVERSE requires 1 argument")
	}
	runes := []rune(valueToString(args[0]))
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes), nil
}

func fnInitcapBuiltin(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("INITCAP requires 1 argument")
	}
	return initcap(valueToString(args[0])), nil
}

// extractFtsQueryFromWhere walks a WHERE clause AST and returns the search
// query from the first FTS_MATCH or @@ predicate it finds.
func extractFtsQueryFromWhere(where parser.Expression) string {
	if where == nil {
		return ""
	}
	switch e := where.(type) {
	case *parser.BinaryExpr:
		if e.Operator == "FTS_MATCH" || e.Operator == "@" || e.Operator == "MATCH" {
			if val, ok := e.Right.(*parser.Value); ok {
				return val.StrVal
			}
		}
		if q := extractFtsQueryFromWhere(e.Left); q != "" {
			return q
		}
		if q := extractFtsQueryFromWhere(e.Right); q != "" {
			return q
		}
	case *parser.AndExpr:
		if q := extractFtsQueryFromWhere(e.Left); q != "" {
			return q
		}
		return extractFtsQueryFromWhere(e.Right)
	case *parser.OrExpr:
		if q := extractFtsQueryFromWhere(e.Left); q != "" {
			return q
		}
		return extractFtsQueryFromWhere(e.Right)
	case *parser.NotExpr:
		return extractFtsQueryFromWhere(e.Expr)
	}
	return ""
}

// fnBm25Score computes BM25 relevance score.
// Usage: bm25_score(table_name, column_name, 'search query')  -- 3 args
//
//	bm25_score(table_name, column_name)                   -- 2 args (query from WHERE FTS_MATCH)
func fnBm25Score(args []interface{}, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("BM25_SCORE requires 2 or 3 arguments: bm25_score(table_name, column_name [, query])")
	}
	if ctx == nil || ctx.Storage == nil {
		return nil, fmt.Errorf("BM25_SCORE: no storage engine available")
	}

	tableName := valueToString(args[0])
	colName := valueToString(args[1])

	var query string
	if len(args) == 3 {
		query = valueToString(args[2])
	} else {
		query = ctx.FtsQuery
	}

	// If no query is available (2-arg form without WHERE FTS_MATCH), return 0.0
	if query == "" {
		return 0.0, nil
	}

	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("BM25_SCORE: %w", err)
	}

	tableSchema, err := ctx.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return nil, fmt.Errorf("BM25_SCORE: %w", err)
	}

	colIdx := -1
	for i, c := range tableSchema.Columns {
		if strings.EqualFold(c.Name, colName) {
			colIdx = i
			break
		}
	}
	if colIdx < 0 {
		return nil, fmt.Errorf("BM25_SCORE: column '%s' not found in table '%s'", colName, tableName)
	}

	allRows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
	if err != nil {
		return nil, fmt.Errorf("BM25_SCORE: %w", err)
	}

	corpus := fts.NewCorpus()
	for _, r := range allRows {
		if colIdx < len(r) {
			docTerms := fts.Tokenize(valueToString(r[colIdx]))
			corpus.IndexDoc(docTerms)
		}
	}

	queryTerms := fts.Tokenize(query)
	if len(queryTerms) == 0 || corpus.TotalDocs == 0 {
		return 0.0, nil
	}

	// Find current row's column value for scoring
	currentText := ""
	if row != nil && colIdx < len(row) {
		currentText = valueToString(row[colIdx])
	} else if schema != nil {
		// Fallback: try to resolve from current row using the schema
		if val, err := resolveColumn(row, schema, colName, ctx.ColumnIndex); err == nil {
			currentText = valueToString(val)
		}
	}

	docTerms := fts.Tokenize(currentText)
	return corpus.ScoreDocument(docTerms, queryTerms), nil
}
