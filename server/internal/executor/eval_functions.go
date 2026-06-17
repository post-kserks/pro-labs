package executor

import (
	crypto_rand "crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// builtinFunc — тип функции для builtin функций SQL.
type builtinFunc func(args []interface{}, ctx *ExecutionContext) (interface{}, error)

// builtinFuncs — карта встроенных SQL функций.
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

// evalFunctionCall вычисляет вызов функции.
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

// executeSubquery выполняет скалярный подзапрос.
func executeSubquery(sub *parser.SubqueryExpr, outerRow storage.Row, outerSchema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	subQuery := *sub.Query

	if outerRow != nil && outerSchema != nil && subQuery.Where != nil {
		subQuery.Where = injectOuterColumns(subQuery.Where, outerRow, outerSchema)
	}

	cmd, err := CommandFactory(&subQuery)
	if err != nil {
		return nil, err
	}

	res, err := cmd.Execute(ctx)
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

// injectOuterColumns подставляет значения внешних столбцов в подзапрос.
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

// parseJSONArray парсит JSON массив.
func parseJSONArray(s string) []interface{} {
	var arr []interface{}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil
	}
	return arr
}

// generateUUID генерирует UUID v4.
func generateUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate UUID: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// executeUserDefinedFunction выполняет пользовательскую функцию.
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

	if body == "" {
		return nil, fmt.Errorf("function '%s' has no body", funcName)
	}

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

// substituteParam подставляет значение параметра в выражение.
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

// ─── Math Functions ─────────────────────────────────────────────────────────

func fnMod(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("MOD requires 2 arguments")
	}
	a, ok1 := toFloat(args[0])
	b, ok2 := toFloat(args[1])
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("MOD requires numeric arguments")
	}
	if b == 0 {
		return nil, fmt.Errorf("MOD: division by zero")
	}
	return a - (float64(int(a/b)) * b), nil
}

func fnPower(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("POWER requires 2 arguments")
	}
	base, ok1 := toFloat(args[0])
	exp, ok2 := toFloat(args[1])
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("POWER requires numeric arguments")
	}
	return math.Pow(base, exp), nil
}

func fnSqrt(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("SQRT requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("SQRT requires numeric argument")
	}
	if v < 0 {
		return nil, fmt.Errorf("SQRT of negative number")
	}
	return math.Sqrt(v), nil
}

func fnLn(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LN requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("LN requires numeric argument")
	}
	if v <= 0 {
		return nil, fmt.Errorf("LN of non-positive number")
	}
	return math.Log(v), nil
}

func fnLog(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LOG requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("LOG requires numeric argument")
	}
	if v <= 0 {
		return nil, fmt.Errorf("LOG of non-positive number")
	}
	return math.Log10(v), nil
}

func fnExp(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("EXP requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("EXP requires numeric argument")
	}
	return math.Exp(v), nil
}

func fnSign(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("SIGN requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("SIGN requires numeric argument")
	}
	if v > 0 {
		return int64(1), nil
	}
	if v < 0 {
		return int64(-1), nil
	}
	return int64(0), nil
}

func fnAbs(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ABS requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("ABS requires numeric argument")
	}
	return math.Abs(v), nil
}

func fnCeil(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("CEIL requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("CEIL requires numeric argument")
	}
	return math.Ceil(v), nil
}

func fnFloor(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("FLOOR requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("FLOOR requires numeric argument")
	}
	return math.Floor(v), nil
}

func fnRound(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("ROUND requires 1 or 2 arguments")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("ROUND requires numeric argument")
	}
	places := 0
	if len(args) == 2 {
		if p, ok := toInt64(args[1]); ok {
			places = int(p)
		}
	}
	if places == 0 {
		return math.Round(v), nil
	}
	factor := math.Pow(10, float64(places))
	return math.Round(v*factor) / factor, nil
}

func fnCoalesce(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	for _, arg := range args {
		if arg != nil {
			return arg, nil
		}
	}
	return nil, nil
}

func fnNullif(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("NULLIF requires 2 arguments")
	}
	if args[0] == nil || args[1] == nil {
		return args[0], nil
	}
	eq, _ := compareValues(args[0], args[1], "=")
	if eq {
		return nil, nil
	}
	return args[0], nil
}

func fnGreatest(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("GREATEST requires at least 1 argument")
	}
	result := args[0]
	for _, arg := range args[1:] {
		cmp, _ := compareValues(result, arg, "<")
		if cmp {
			result = arg
		}
	}
	return result, nil
}

func fnLeast(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("LEAST requires at least 1 argument")
	}
	result := args[0]
	for _, arg := range args[1:] {
		cmp, _ := compareValues(result, arg, ">")
		if cmp {
			result = arg
		}
	}
	return result, nil
}

// ─── JSON Functions ─────────────────────────────────────────────────────────

func fnJsonObject(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("JSON_OBJECT requires even number of arguments")
	}
	obj := make(map[string]interface{})
	for i := 0; i < len(args); i += 2 {
		key := valueToString(args[i])
		obj[key] = args[i+1]
	}
	data, _ := json.Marshal(obj)
	return string(data), nil
}

func fnJsonArray(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	data, _ := json.Marshal(args)
	return string(data), nil
}

func fnJsonExtract(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSON_EXTRACT requires at least 2 arguments")
	}
	leftStr := valueToString(args[0])
	var data interface{}
	if err := json.Unmarshal([]byte(leftStr), &data); err != nil {
		return nil, fmt.Errorf("JSON_EXTRACT: not valid JSON")
	}

	for i := 1; i < len(args); i++ {
		key := valueToString(args[i])
		switch v := data.(type) {
		case map[string]interface{}:
			data = v[key]
		case []interface{}:
			idx := 0
			if _, err := fmt.Sscanf(key, "%d", &idx); err == nil && idx < len(v) {
				data = v[idx]
			} else {
				data = nil
			}
		default:
			data = nil
		}
	}
	if data == nil {
		return nil, nil
	}
	result, _ := json.Marshal(data)
	return string(result), nil
}

func fnJsonbBuildObject(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("JSONB_BUILD_OBJECT requires even number of arguments")
	}
	obj := make(map[string]interface{})
	for i := 0; i < len(args); i += 2 {
		key := valueToString(args[i])
		obj[key] = args[i+1]
	}
	data, _ := json.Marshal(obj)
	return string(data), nil
}

func fnJsonbBuildArray(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	data, _ := json.Marshal(args)
	return string(data), nil
}

func fnJsonbArrayElements(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("JSONB_ARRAY_ELEMENTS requires 1 argument")
	}
	s := valueToString(args[0])
	var arr []interface{}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, fmt.Errorf("JSONB_ARRAY_ELEMENTS: not a JSON array")
	}
	if len(arr) == 0 {
		return nil, nil
	}
	return arr[0], nil
}

func fnJsonbTypeof(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("JSONB_TYPEOF requires 1 argument")
	}
	s := valueToString(args[0])
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return "string", nil
	}
	switch v.(type) {
	case nil:
		return "null", nil
	case bool:
		return "boolean", nil
	case float64:
		return "number", nil
	case string:
		return "string", nil
	case []interface{}:
		return "array", nil
	case map[string]interface{}:
		return "object", nil
	default:
		return "string", nil
	}
}

func fnJsonbSet(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("JSONB_SET requires 3 arguments: target, path, new_value")
	}
	targetStr := valueToString(args[0])
	path := valueToString(args[1])
	newVal := args[2]

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(targetStr), &data); err != nil {
		return nil, fmt.Errorf("JSONB_SET: target is not JSON object")
	}
	data[path] = newVal
	result, _ := json.Marshal(data)
	return string(result), nil
}

func fnJsonbExtractPath(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSONB_EXTRACT_PATH requires at least 2 arguments")
	}
	leftStr := valueToString(args[0])
	var data interface{}
	if err := json.Unmarshal([]byte(leftStr), &data); err != nil {
		return nil, fmt.Errorf("JSONB_EXTRACT_PATH: not valid JSON")
	}

	for i := 1; i < len(args); i++ {
		key := valueToString(args[i])
		switch v := data.(type) {
		case map[string]interface{}:
			data = v[key]
		case []interface{}:
			idx := 0
			if _, err := fmt.Sscanf(key, "%d", &idx); err == nil && idx < len(v) {
				data = v[idx]
			} else {
				data = nil
			}
		default:
			data = nil
		}
	}
	if data == nil {
		return nil, nil
	}
	result, _ := json.Marshal(data)
	return string(result), nil
}

// ─── Misc Functions ─────────────────────────────────────────────────────────

func fnAiEmbed(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("AI_EMBED requires 1 argument")
	}
	text := valueToString(args[0])
	vec, err := embedText(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("AI_EMBED: %w", err)
	}
	return vec, nil
}

func fnUuid(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return generateUUID()
}

func fnInterval(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("INTERVAL requires 1 argument")
	}
	return valueToString(args[0]), nil
}

// ─── Array Functions ────────────────────────────────────────────────────────

func fnArrayAppend(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_APPEND requires 2 arguments")
	}
	arr := parseJSONArray(valueToString(args[0]))
	arr = append(arr, args[1])
	data, _ := json.Marshal(arr)
	return string(data), nil
}

func fnArrayLength(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ARRAY_LENGTH requires 1 argument")
	}
	return int64(len(parseJSONArray(valueToString(args[0])))), nil
}

func fnArrayContains(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_CONTAINS requires 2 arguments")
	}
	arr := parseJSONArray(valueToString(args[0]))
	valStr := valueToString(args[1])
	for _, v := range arr {
		if valueToString(v) == valStr {
			return true, nil
		}
	}
	return false, nil
}

func fnArrayToString(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_TO_STRING requires 2 arguments")
	}
	arr := parseJSONArray(valueToString(args[0]))
	delim := valueToString(args[1])
	parts := make([]string, len(arr))
	for i, v := range arr {
		parts[i] = valueToString(v)
	}
	return strings.Join(parts, delim), nil
}

func fnArraySlice(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("ARRAY_SLICE requires 2 or 3 arguments")
	}
	arr := parseJSONArray(valueToString(args[0]))
	start, _ := toInt64(args[1])
	end := int64(len(arr))
	if len(args) == 3 {
		end, _ = toInt64(args[2])
	}
	if start < 0 {
		start = 0
	}
	if end > int64(len(arr)) {
		end = int64(len(arr))
	}
	if start >= end {
		return "[]", nil
	}
	sliced := arr[start:end]
	data, _ := json.Marshal(sliced)
	return string(data), nil
}
