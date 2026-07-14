package executor

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/core/executor/eval"
	"vaultdb/internal/core/fts"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/wasmudf"
)

// builtinFunc — function type for builtin SQL functions.
type builtinFunc func(args []interface{}, ctx *ExecutionContext) (interface{}, error)

// evalBuiltinFunc — function type for eval package builtin functions.
type evalBuiltinFunc func(args []interface{}, ctx interface{}) (interface{}, error)

// extendedBuiltinFunc — function type for builtins that need row/schema access.
type extendedBuiltinFunc func(args []interface{}, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error)

// builtinFuncs — map of builtin SQL functions.
var builtinFuncs = map[string]builtinFunc{
	// String functions (stay in root)
	"UPPER":     fnUpper,
	"LOWER":     fnLower,
	"CONCAT":    fnConcat,
	"LENGTH":    fnLength,
	"LEN":       fnLength,
	"SUBSTRING": fnSubstring,
	"SUBSTR":    fnSubstring,
	"TRIM":      fnTrim,
	"LTRIM":     fnLtrim,
	"RTRIM":     fnRtrim,
	"REPLACE":   fnReplace,
	"POSITION":  fnPosition,
	"LEFT":      fnLeft,
	"RIGHT":     fnRight,
	"LPAD":      fnLpad,
	"RPAD":      fnRpad,
	"REVERSE":   fnReverse,
	"INITCAP":   fnInitcapBuiltin,
}

// evalBuiltinFuncs — functions from the eval package.
var evalBuiltinFuncs = map[string]evalBuiltinFunc{
	// Date/time functions
	"NOW":               eval.FnNow,
	"CURRENT_DATE":      eval.FnCurrentDate,
	"CURRENT_TIME":      eval.FnCurrentTime,
	"CURRENT_TIMESTAMP": eval.FnCurrentTimestamp,
	"DATE_TRUNC":        eval.FnDateTrunc,
	"EXTRACT":           eval.FnExtract,
	"AGE":               eval.FnAge,
	"TO_DATE":           eval.FnToDate,
	"TO_CHAR":           eval.FnToChar,
	"TO_TIMESTAMP":      eval.FnToTimestamp,
	"DATE_ADD":          eval.FnDateAdd,
	"DATE_SUB":          eval.FnDateSub,
	"DATE_DIFF":         eval.FnDateDiff,
	// Math functions
	"MOD":     eval.FnMod,
	"POWER":   eval.FnPower,
	"POW":     eval.FnPower,
	"SQRT":    eval.FnSqrt,
	"LN":      eval.FnLn,
	"LOG":     eval.FnLog,
	"LOG10":   eval.FnLog,
	"EXP":     eval.FnExp,
	"SIGN":    eval.FnSign,
	"ABS":     eval.FnAbs,
	"CEIL":    eval.FnCeil,
	"CEILING": eval.FnCeil,
	"FLOOR":   eval.FnFloor,
	"ROUND":   eval.FnRound,
	// Aggregate-like functions
	"COALESCE": eval.FnCoalesce,
	"NULLIF":   eval.FnNullif,
	"GREATEST": eval.FnGreatest,
	"LEAST":    eval.FnLeast,
	// JSON functions
	"JSON_OBJECT":          eval.FnJsonObject,
	"JSON_ARRAY":           eval.FnJsonArray,
	"JSON_EXTRACT":         eval.FnJsonExtract,
	"JSONB_BUILD_OBJECT":   eval.FnJsonbBuildObject,
	"JSONB_BUILD_ARRAY":    eval.FnJsonbBuildArray,
	"JSONB_ARRAY_ELEMENTS": eval.FnJsonbArrayElements,
	"JSONB_TYPEOF":         eval.FnJsonbTypeof,
	"JSONB_SET":            eval.FnJsonbSet,
	"JSONB_EXTRACT_PATH":   eval.FnJsonbExtractPath,
	// Array functions
	"ARRAY_APPEND":    eval.FnArrayAppend,
	"ARRAY_LENGTH":    eval.FnArrayLength,
	"ARRAY_CONTAINS":  eval.FnArrayContains,
	"ARRAY_TO_STRING": eval.FnArrayToString,
	"ARRAY_SLICE":     eval.FnArraySlice,
	// Misc functions
	"AI_EMBED": eval.FnAiEmbed,
	"UUID":     eval.FnUuid,
	"INTERVAL": eval.FnInterval,
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
	if efn, ok := evalBuiltinFuncs[name]; ok {
		return efn(args, ctx)
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

	res, err := ctx.RunSubquery.RunSubquery(ctx, cmd)
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

func fnUpper(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("UPPER requires 1 argument")
	}
	if s, ok := args[0].(string); ok {
		return strings.ToUpper(s), nil
	}
	return nil, fmt.Errorf("UPPER requires string argument")
}

func fnLower(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LOWER requires 1 argument")
	}
	if s, ok := args[0].(string); ok {
		return strings.ToLower(s), nil
	}
	return nil, fmt.Errorf("LOWER requires string argument")
}

func fnConcat(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	var sb strings.Builder
	for _, arg := range args {
		sb.WriteString(eval.ValueToString(arg))
	}
	return sb.String(), nil
}

func fnLength(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LENGTH requires 1 argument")
	}
	s := eval.ValueToString(args[0])
	return int64(len([]rune(s))), nil
}

func fnSubstring(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("SUBSTRING requires 2 or 3 arguments")
	}
	s := eval.ValueToString(args[0])
	start, ok := eval.ToInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("SUBSTRING start must be integer")
	}
	length := int64(len([]rune(s)))
	if len(args) == 3 {
		if l, ok := eval.ToInt64(args[2]); ok {
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

func fnTrim(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("TRIM requires 1 argument")
	}
	return strings.TrimSpace(eval.ValueToString(args[0])), nil
}

func fnLtrim(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LTRIM requires 1 argument")
	}
	return strings.TrimLeft(eval.ValueToString(args[0]), " \t\n\r"), nil
}

func fnRtrim(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("RTRIM requires 1 argument")
	}
	return strings.TrimRight(eval.ValueToString(args[0]), " \t\n\r"), nil
}

func fnReplace(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("REPLACE requires 3 arguments")
	}
	return strings.ReplaceAll(eval.ValueToString(args[0]), eval.ValueToString(args[1]), eval.ValueToString(args[2])), nil
}

func fnPosition(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("POSITION requires 2 arguments")
	}
	substr := eval.ValueToString(args[0])
	s := eval.ValueToString(args[1])
	runes := []rune(s)
	subRunes := []rune(substr)
	for i := 0; i <= len(runes)-len(subRunes); i++ {
		if string(runes[i:i+len(subRunes)]) == substr {
			return int64(i + 1), nil
		}
	}
	return int64(0), nil
}

func fnLeft(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("LEFT requires 2 arguments")
	}
	s := eval.ValueToString(args[0])
	n, ok := eval.ToInt64(args[1])
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

func fnRight(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("RIGHT requires 2 arguments")
	}
	s := eval.ValueToString(args[0])
	n, ok := eval.ToInt64(args[1])
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

func fnLpad(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("LPAD requires 2 or 3 arguments")
	}
	s := eval.ValueToString(args[0])
	length, ok := eval.ToInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("LPAD length must be integer")
	}
	pad := " "
	if len(args) == 3 {
		pad = eval.ValueToString(args[2])
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

func fnRpad(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("RPAD requires 2 or 3 arguments")
	}
	s := eval.ValueToString(args[0])
	length, ok := eval.ToInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("RPAD length must be integer")
	}
	pad := " "
	if len(args) == 3 {
		pad = eval.ValueToString(args[2])
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

func fnReverse(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("REVERSE requires 1 argument")
	}
	runes := []rune(eval.ValueToString(args[0]))
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes), nil
}

func fnInitcapBuiltin(args []interface{}, _ *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("INITCAP requires 1 argument")
	}
	return eval.Initcap(eval.ValueToString(args[0])), nil
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
func fnBm25Score(args []interface{}, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 && len(args) != 3 {
		return nil, fmt.Errorf("BM25_SCORE requires 2 or 3 arguments: bm25_score(table_name, column_name [, query])")
	}
	if ctx == nil || ctx.Storage == nil {
		return nil, fmt.Errorf("BM25_SCORE: no storage engine available")
	}

	tableName := eval.ValueToString(args[0])
	colName := eval.ValueToString(args[1])

	var query string
	if len(args) == 3 {
		query = eval.ValueToString(args[2])
	} else {
		query = ctx.FtsQuery
	}

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
			docTerms := fts.Tokenize(eval.ValueToString(r[colIdx]))
			corpus.IndexDoc(docTerms)
		}
	}

	queryTerms := fts.Tokenize(query)
	if len(queryTerms) == 0 || corpus.TotalDocs == 0 {
		return 0.0, nil
	}

	currentText := ""
	if row != nil && colIdx < len(row) {
		currentText = eval.ValueToString(row[colIdx])
	} else if schema != nil {
		if val, err := eval.ResolveColumn(row, schema, colName, ctx.ColumnIndex); err == nil {
			currentText = eval.ValueToString(val)
		}
	}

	docTerms := fts.Tokenize(currentText)
	return corpus.ScoreDocument(docTerms, queryTerms), nil
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

	cmd, err := ctx.CreateCommand(sel)
	if err != nil {
		return nil, fmt.Errorf("function '%s': %w", funcName, err)
	}
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
	wasmPath = strings.TrimPrefix(wasmPath, "file://")

	memLimit, timeout, err := wasmudf.ParseOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("WASM options: %w", err)
	}

	var maxPages uint32
	if memLimit > 0 {
		maxPages = memLimit / (64 * 1024)
		if maxPages == 0 {
			maxPages = 1
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

	return fn.Call(nil, args)
}
