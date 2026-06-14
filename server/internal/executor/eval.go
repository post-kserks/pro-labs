package executor

import (
	"context"
	"encoding/json"
	"fmt"
	crypto_rand "crypto/rand"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func evalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	if expr == nil {
		return true, nil
	}

	val, err := evalOperand(expr, row, schema, ctx)
	if err != nil {
		return false, err
	}

	if b, ok := val.(bool); ok {
		return b, nil
	}

	return false, fmt.Errorf("expression must return boolean, got %T", val)
}

func evalBinary(expr *parser.BinaryExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	left, err := evalOperand(expr.Left, row, schema, ctx)
	if err != nil {
		return nil, err
	}
	right, err := evalOperand(expr.Right, row, schema, ctx)
	if err != nil {
		return nil, err
	}

	switch expr.Operator {
	case "=", "!=", "<", ">", "<=", ">=":
		return compareValues(left, right, expr.Operator)
	case "+", "-", "*", "/":
		return evalArithmetic(left, right, expr.Operator)
	case "LIKE":
		return evalLike(left, right)
	case "IS":
		return left == nil, nil
	case "IS NOT":
		return left != nil, nil
	case "SEMANTIC_MATCH":
		return evalSemanticMatch(left, right, ctx)
	case "FTS_MATCH":
		return evalFtsMatch(left, right)
	case "@>":
		return evalJsonContains(left, right)
	case "<@":
		return evalJsonContainedBy(left, right)
	case "?":
		return evalJsonHasKey(left, right)
	case "||":
		return evalJsonMerge(left, right)
	case "@@":
		return evalFullTextMatch(left, right, ctx)
	default:
		return nil, fmt.Errorf("unsupported operator '%s'", expr.Operator)
	}
}

func evalFtsMatch(left, right interface{}) (bool, error) {
	text := strings.ToLower(valueToString(left))
	query := strings.ToLower(valueToString(right))

	// Simplified BM25 / TF-IDF
	queryTerms := strings.Fields(query)
	if len(queryTerms) == 0 {
		return true, nil
	}

	docTerms := strings.Fields(text)
	docFreq := make(map[string]int)
	for _, term := range docTerms {
		docFreq[term]++
	}

	score := 0.0
	for _, q := range queryTerms {
		if count, ok := docFreq[q]; ok {
			// TF part
			score += float64(count) / float64(len(docTerms))
		}
	}

	return score > 0.1, nil // Threshold for "match"
}

func evalJsonContains(left, right interface{}) (interface{}, error) {
	leftStr := valueToString(left)
	rightStr := valueToString(right)
	var leftArr, rightArr []interface{}
	if err := json.Unmarshal([]byte(leftStr), &leftArr); err != nil {
		return nil, fmt.Errorf("JSON contains: left is not JSON array")
	}
	if err := json.Unmarshal([]byte(rightStr), &rightArr); err != nil {
		return nil, fmt.Errorf("JSON contains: right is not JSON array")
	}
	leftSet := make(map[string]bool)
	for _, v := range leftArr {
		leftSet[fmt.Sprintf("%v", v)] = true
	}
	for _, v := range rightArr {
		if !leftSet[fmt.Sprintf("%v", v)] {
			return false, nil
		}
	}
	return true, nil
}

func evalJsonContainedBy(left, right interface{}) (interface{}, error) {
	leftStr := valueToString(left)
	rightStr := valueToString(right)
	var leftArr, rightArr []interface{}
	if err := json.Unmarshal([]byte(leftStr), &leftArr); err != nil {
		return nil, fmt.Errorf("JSON contained by: left is not JSON array")
	}
	if err := json.Unmarshal([]byte(rightStr), &rightArr); err != nil {
		return nil, fmt.Errorf("JSON contained by: right is not JSON array")
	}
	rightSet := make(map[string]bool)
	for _, v := range rightArr {
		rightSet[fmt.Sprintf("%v", v)] = true
	}
	for _, v := range leftArr {
		if !rightSet[fmt.Sprintf("%v", v)] {
			return false, nil
		}
	}
	return true, nil
}

func evalJsonHasKey(left, right interface{}) (interface{}, error) {
	leftStr := valueToString(left)
	key := valueToString(right)
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(leftStr), &data); err != nil {
		return nil, fmt.Errorf("JSON has key: left is not JSON object")
	}
	_, exists := data[key]
	return exists, nil
}

func evalJsonMerge(left, right interface{}) (interface{}, error) {
	leftStr := valueToString(left)
	rightStr := valueToString(right)
	var leftObj, rightObj map[string]interface{}
	if err := json.Unmarshal([]byte(leftStr), &leftObj); err != nil {
		return nil, fmt.Errorf("JSON merge: left is not JSON object")
	}
	if err := json.Unmarshal([]byte(rightStr), &rightObj); err != nil {
		return nil, fmt.Errorf("JSON merge: right is not JSON object")
	}
	for k, v := range rightObj {
		leftObj[k] = v
	}
	data, _ := json.Marshal(leftObj)
	return string(data), nil
}

func evalOperandRaw(expr parser.Expression) interface{} {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case parser.Value:
		return parserValueToRaw(e)
	case *parser.Value:
		return parserValueToRaw(*e)
	default:
		return nil
	}
}

func evalFullTextMatch(left, right interface{}, ctx *ExecutionContext) (interface{}, error) {
	leftStr := valueToString(left)
	query := valueToString(right)
	
	text := strings.ToLower(leftStr)
	queryTerms := strings.Fields(strings.ToLower(query))
	
	if len(queryTerms) == 0 {
		return true, nil
	}
	
	textTerms := strings.Fields(text)
	freq := make(map[string]int)
	for _, term := range textTerms {
		freq[term]++
	}
	
	score := 0.0
	for _, q := range queryTerms {
		if count, ok := freq[q]; ok {
			score += float64(count) / float64(len(textTerms))
		}
	}
	
	return score > 0.1, nil
}

// evalSemanticMatch сравнивает операнды по косинусной близости. Операнды,
// которые уже являются векторами, используются как есть; текст прогоняется
// через настроенный embedding-провайдер. Если AI не настроен, возвращается
// понятная ошибка (NoopEmbedder), а не тихий mock-результат.
func evalSemanticMatch(left, right interface{}, ctx *ExecutionContext) (bool, error) {
	v1, err := operandVector(left, ctx)
	if err != nil {
		return false, fmt.Errorf("SEMANTIC_MATCH: %w", err)
	}
	v2, err := operandVector(right, ctx)
	if err != nil {
		return false, fmt.Errorf("SEMANTIC_MATCH: %w", err)
	}

	sim := cosineSimilarity(v1, v2)
	return sim > 0.7, nil
}

// operandVector превращает операнд в вектор: готовые векторы проходят как
// есть, текст эмбеддится через настроенный провайдер.
func operandVector(val interface{}, ctx *ExecutionContext) ([]float64, error) {
	if v, err := toVector(val); err == nil {
		return v, nil
	}
	return embedText(ctx, valueToString(val))
}

// embedText вызывает настроенный embedding-провайдер с таймаутом.
func embedText(ctx *ExecutionContext, text string) ([]float64, error) {
	var embedder ai.Embedder = ai.NoopEmbedder{}
	if ctx != nil && ctx.Embedder != nil {
		embedder = ctx.Embedder
	}
	embedCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return embedder.Embed(embedCtx, text)
}

func toVector(val interface{}) ([]float64, error) {
	switch v := val.(type) {
	case []float64:
		return v, nil
	case []interface{}:
		res := make([]float64, len(v))
		for i, x := range v {
			if f, ok := toFloat(x); ok {
				res[i] = f
			}
		}
		return res, nil
	case string:
		var res []float64
		if err := json.Unmarshal([]byte(v), &res); err == nil {
			return res, nil
		}
	}
	return nil, fmt.Errorf("cannot convert %T to VECTOR", val)
}

func cosineSimilarity(v1, v2 []float64) float64 {
	if len(v1) != len(v2) || len(v1) == 0 {
		return 0
	}
	var dot, n1, n2 float64
	for i := range v1 {
		dot += v1[i] * v2[i]
		n1 += v1[i] * v1[i]
		n2 += v2[i] * v2[i]
	}
	if n1 == 0 || n2 == 0 {
		return 0
	}
	return dot / (math.Sqrt(n1) * math.Sqrt(n2))
}

func evalArithmetic(left, right interface{}, op string) (interface{}, error) {
	if left == nil || right == nil {
		return nil, nil
	}

	leftStr := valueToString(left)
	rightStr := valueToString(right)
	if isIntervalString(rightStr) && (op == "+" || op == "-") {
		return evalDateInterval(leftStr, rightStr, op)
	}
	if isIntervalString(leftStr) && (op == "+" || op == "-") {
		return evalDateInterval(rightStr, leftStr, op)
	}

	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if !lok || !rok {
		return nil, fmt.Errorf("arithmetic requires numeric operands, got %T and %T", left, right)
	}

	var res float64
	switch op {
	case "+":
		res = lf + rf
	case "-":
		res = lf - rf
	case "*":
		res = lf * rf
	case "/":
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		res = lf / rf
	}

	// If both were integers, try to return integer
	_, lint := left.(int64)
	if !lint {
		_, lint = left.(int)
	}
	_, rint := right.(int64)
	if !rint {
		_, rint = right.(int)
	}

	if lint && rint && op != "/" {
		return int64(res), nil
	}

	return res, nil
}

func evalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	if expr == nil {
		return nil, nil
	}
	switch e := expr.(type) {
	case parser.Value:
		return parserValueToRaw(e), nil
	case *parser.Value:
		return parserValueToRaw(*e), nil
	case *parser.ColumnRef:
		return resolveColumn(row, schema, e.Name)
	case *parser.BinaryExpr:
		return evalBinary(e, row, schema, ctx)
	case *parser.AndExpr:
		left, err := evalExpr(e.Left, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		right, err := evalExpr(e.Right, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return left && right, nil
	case *parser.OrExpr:
		left, err := evalExpr(e.Left, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		right, err := evalExpr(e.Right, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return left || right, nil
	case *parser.NotExpr:
		val, err := evalExpr(e.Expr, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return !val, nil
	case *parser.SubqueryExpr:
		return executeSubquery(e, row, schema, ctx)
	case *parser.InExpr:
		return evalInExpr(e, row, schema, ctx)
	case *parser.BetweenExpr:
		return evalBetweenExpr(e, row, schema, ctx)
	case *parser.ExistsExpr:
		return evalExistsExpr(e, row, schema, ctx)
	case *parser.ComparisonSubqueryExpr:
		return evalComparisonSubquery(e, row, schema, ctx)
	case *parser.WindowFunctionExpr:
		if ctx != nil && ctx.WindowCols != nil {
			if name, ok := ctx.WindowCols[e]; ok {
				return resolveColumn(row, schema, name)
			}
		}
		return resolveColumn(row, schema, "window_func")
	case *parser.FunctionCall:
		return evalFunctionCall(e, row, schema, ctx)
	case *parser.CastExpr:
		return evalCast(e, row, schema, ctx)
	case *parser.CaseExpr:
		return evalCase(e, row, schema, ctx)
	case *parser.JsonPathExpr:
		return evalJsonPath(e, row, schema, ctx)
	case *parser.AggregateExpr:
		return nil, fmt.Errorf("aggregate function %s() not allowed here", e.Name)
	default:
		return nil, fmt.Errorf("invalid operand type %T", expr)
	}
}


type builtinFunc func(args []interface{}, ctx *ExecutionContext) (interface{}, error)

func fnNow(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return time.Now().UTC().Format(time.RFC3339), nil
}

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
	if len(args) < 1 || len(args) > 1 {
		return nil, fmt.Errorf("TRIM requires 1 argument")
	}
	s := valueToString(args[0])
	return strings.TrimSpace(s), nil
}

func fnLtrim(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LTRIM requires 1 argument")
	}
	s := valueToString(args[0])
	return strings.TrimLeft(s, " \t\n\r"), nil
}

func fnRtrim(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("RTRIM requires 1 argument")
	}
	s := valueToString(args[0])
	return strings.TrimRight(s, " \t\n\r"), nil
}

func fnReplace(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("REPLACE requires 3 arguments")
	}
	s := valueToString(args[0])
	old := valueToString(args[1])
	new := valueToString(args[2])
	return strings.ReplaceAll(s, old, new), nil
}

func fnPosition(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("POSITION requires 2 arguments")
	}
	substr := valueToString(args[0])
	s := valueToString(args[1])
	idx := strings.Index(s, substr)
	if idx == -1 {
		return int64(0), nil
	}
	return int64(idx + 1), nil
}

func fnLeft(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("LEFT requires 2 arguments")
	}
	s := valueToString(args[0])
	n, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("LEFT length must be integer")
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
		return nil, fmt.Errorf("RIGHT length must be integer")
	}
	runes := []rune(s)
	if n > int64(len(runes)) {
		n = int64(len(runes))
	}
	if n < 0 {
		n = 0
	}
	return string(runes[int(len(runes)-int(n)):]), nil
}

func fnLpad(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("LPAD requires 3 arguments")
	}
	s := valueToString(args[0])
	n, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("LPAD length must be integer")
	}
	padStr := valueToString(args[2])
	runes := []rune(s)
	if int64(len(runes)) >= n {
		return string(runes[:n]), nil
	}
	padRunes := []rune(padStr)
	padLen := n - int64(len(runes))
	var pad strings.Builder
	for i := int64(0); i < padLen; i++ {
		pad.WriteRune(padRunes[i%int64(len(padRunes))])
	}
	return pad.String() + s, nil
}

func fnRpad(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("RPAD requires 3 arguments")
	}
	s := valueToString(args[0])
	n, ok := toInt64(args[1])
	if !ok {
		return nil, fmt.Errorf("RPAD length must be integer")
	}
	padStr := valueToString(args[2])
	runes := []rune(s)
	if int64(len(runes)) >= n {
		return string(runes[:n]), nil
	}
	padRunes := []rune(padStr)
	padLen := n - int64(len(runes))
	var pad strings.Builder
	for i := int64(0); i < padLen; i++ {
		pad.WriteRune(padRunes[i%int64(len(padRunes))])
	}
	return s + pad.String(), nil
}

func fnReverse(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("REVERSE requires 1 argument")
	}
	s := valueToString(args[0])
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes), nil
}

func fnInitcap(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("INITCAP requires 1 argument")
	}
	s := valueToString(args[0])
	return initcap(s), nil
}

func fnMod(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("MOD requires 2 arguments")
	}
	a, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("MOD requires numeric arguments")
	}
	b, ok := toFloat(args[1])
	if !ok {
		return nil, fmt.Errorf("MOD requires numeric arguments")
	}
	if b == 0 {
		return nil, fmt.Errorf("division by zero in MOD")
	}
	return math.Mod(a, b), nil
}

func fnPower(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("POWER requires 2 arguments")
	}
	base, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("POWER requires numeric arguments")
	}
	exp, ok := toFloat(args[1])
	if !ok {
		return nil, fmt.Errorf("POWER requires numeric arguments")
	}
	return math.Pow(base, exp), nil
}

func fnSqrt(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("SQRT requires 1 argument")
	}
	f, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("SQRT requires numeric argument")
	}
	if f < 0 {
		return nil, fmt.Errorf("SQRT of negative number")
	}
	return math.Sqrt(f), nil
}

func fnLn(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LN requires 1 argument")
	}
	f, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("LN requires numeric argument")
	}
	if f <= 0 {
		return nil, fmt.Errorf("LN of non-positive number")
	}
	return math.Log(f), nil
}

func fnLog(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LOG requires 1 argument")
	}
	f, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("LOG requires numeric argument")
	}
	if f <= 0 {
		return nil, fmt.Errorf("LOG of non-positive number")
	}
	return math.Log10(f), nil
}

func fnExp(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("EXP requires 1 argument")
	}
	f, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("EXP requires numeric argument")
	}
	return math.Exp(f), nil
}

func fnSign(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("SIGN requires 1 argument")
	}
	f, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("SIGN requires numeric argument")
	}
	if f > 0 {
		return int64(1), nil
	} else if f < 0 {
		return int64(-1), nil
	}
	return int64(0), nil
}

func fnAbs(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ABS requires 1 argument")
	}
	if f, ok := toFloat(args[0]); ok {
		return math.Abs(f), nil
	}
	return nil, fmt.Errorf("ABS requires numeric argument")
}

func fnCeil(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("CEIL requires 1 argument")
	}
	if f, ok := toFloat(args[0]); ok {
		return math.Ceil(f), nil
	}
	return nil, fmt.Errorf("CEIL requires numeric argument")
}

func fnFloor(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("FLOOR requires 1 argument")
	}
	if f, ok := toFloat(args[0]); ok {
		return math.Floor(f), nil
	}
	return nil, fmt.Errorf("FLOOR requires numeric argument")
}

func fnRound(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("ROUND requires 1 or 2 arguments")
	}
	f, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("ROUND requires numeric argument")
	}
	places := 0.0
	if len(args) == 2 {
		if p, ok := toFloat(args[1]); ok {
			places = p
		}
	}
	shift := math.Pow(10, places)
	return math.Round(f*shift) / shift, nil
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
	if CompareValues(args[0], args[1]) == 0 {
		return nil, nil
	}
	return args[0], nil
}

func fnGreatest(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("GREATEST requires at least 1 argument")
	}
	result := args[0]
	for _, arg := range args[1:] {
		if CompareValues(arg, result) > 0 {
			result = arg
		}
	}
	return result, nil
}

func fnLeast(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("LEAST requires at least 1 argument")
	}
	result := args[0]
	for _, arg := range args[1:] {
		if CompareValues(arg, result) < 0 {
			result = arg
		}
	}
	return result, nil
}

func fnJsonObject(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("JSON_OBJECT requires even number of arguments (key-value pairs)")
	}
	obj := make(map[string]interface{})
	for i := 0; i < len(args); i += 2 {
		key := valueToString(args[i])
		obj[key] = args[i+1]
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("JSON_OBJECT: %w", err)
	}
	return string(data), nil
}

func fnJsonArray(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	arr := make([]interface{}, len(args))
	for i, arg := range args {
		arr[i] = arg
	}
	data, err := json.Marshal(arr)
	if err != nil {
		return nil, fmt.Errorf("JSON_ARRAY: %w", err)
	}
	return string(data), nil
}

func fnJsonExtract(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSON_EXTRACT requires 2 arguments")
	}
	jsonStr := valueToString(args[0])
	path := valueToString(args[1])
	var data interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return nil, nil
	}
	parts := strings.Split(path, ".")
	current := data
	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			current = v[part]
		case []interface{}:
			idx := 0
			if _, err := fmt.Sscanf(part, "%d", &idx); err == nil && idx < len(v) {
				current = v[idx]
			} else {
				return nil, nil
			}
		default:
			return nil, nil
		}
	}
	return current, nil
}

func fnCurrentDate(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return time.Now().UTC().Format("2006-01-02"), nil
}

func fnCurrentTime(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return time.Now().UTC().Format("15:04:05"), nil
}

func fnCurrentTimestamp(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return time.Now().UTC().Format(time.RFC3339), nil
}

func fnDateTrunc(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("DATE_TRUNC requires 2 arguments")
	}
	part := strings.ToUpper(valueToString(args[0]))
	ts, err := parseTimestamp(valueToString(args[1]))
	if err != nil {
		return nil, fmt.Errorf("DATE_TRUNC: %w", err)
	}
	switch part {
	case "YEAR":
		return time.Date(ts.Year(), 1, 1, 0, 0, 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "MONTH":
		return time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "DAY":
		return time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "HOUR":
		return time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), 0, 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "MINUTE":
		return time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), 0, 0, ts.Location()).Format(time.RFC3339), nil
	case "SECOND":
		return time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), ts.Second(), 0, ts.Location()).Format(time.RFC3339), nil
	default:
		return nil, fmt.Errorf("DATE_TRUNC: unknown part %q", part)
	}
}

func fnExtract(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("EXTRACT requires 1 or 2 arguments")
	}
	if len(args) == 1 {
		s := valueToString(args[0])
		ts, err := parseTimestamp(s)
		if err != nil {
			return nil, fmt.Errorf("EXTRACT: %w", err)
		}
		return int64(ts.Unix()), nil
	}
	field := strings.ToUpper(valueToString(args[0]))
	t, err := parseTimestamp(valueToString(args[1]))
	if err != nil {
		return nil, fmt.Errorf("EXTRACT: %w", err)
	}
	switch field {
	case "YEAR":
		return int64(t.Year()), nil
	case "MONTH":
		return int64(t.Month()), nil
	case "DAY":
		return int64(t.Day()), nil
	case "HOUR":
		return int64(t.Hour()), nil
	case "MINUTE":
		return int64(t.Minute()), nil
	case "SECOND":
		return int64(t.Second()), nil
	case "DOW":
		return int64(t.Weekday()), nil
	case "DOY":
		return int64(t.YearDay()), nil
	default:
		return nil, fmt.Errorf("EXTRACT: unknown field %q", field)
	}
}

func fnArrayAppend(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_APPEND requires 2 arguments: array, value")
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
	arr := parseJSONArray(valueToString(args[0]))
	return int64(len(arr)), nil
}

func fnArrayContains(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_CONTAINS requires 2 arguments: array, value")
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
		return nil, fmt.Errorf("ARRAY_TO_STRING requires 2 arguments: array, delimiter")
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
		return nil, fmt.Errorf("ARRAY_SLICE requires 2 or 3 arguments: array, start[, end]")
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

func fnAge(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("AGE requires 1 or 2 arguments")
	}
	ts, err := parseTimestamp(valueToString(args[0]))
	if err != nil {
		return nil, fmt.Errorf("AGE: %w", err)
	}
	var diff time.Duration
	if len(args) == 2 {
		ts2, err := parseTimestamp(valueToString(args[1]))
		if err != nil {
			return nil, fmt.Errorf("AGE: %w", err)
		}
		diff = ts.Sub(ts2)
	} else {
		diff = time.Since(ts)
	}
	days := int(diff.Hours() / 24)
	hours := int(math.Mod(diff.Hours(), 24))
	minutes := int(math.Mod(diff.Minutes(), 60))
	seconds := int(math.Mod(diff.Seconds(), 60))
	return fmt.Sprintf("%d days %d hours %d mins %d secs", days, hours, minutes, seconds), nil
}

func fnToDate(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("TO_DATE requires 2 arguments")
	}
	s := valueToString(args[0])
	layout := valueToString(args[1])
	t, err := time.Parse(layout, s)
	if err != nil {
		return nil, fmt.Errorf("TO_DATE: %w", err)
	}
	return t.Format("2006-01-02"), nil
}

func fnToChar(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("TO_CHAR requires 2 arguments")
	}
	ts, err := parseTimestamp(valueToString(args[0]))
	if err != nil {
		return nil, fmt.Errorf("TO_CHAR: %w", err)
	}
	layout := valueToString(args[1])
	return ts.Format(layout), nil
}

func fnToTimestamp(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("TO_TIMESTAMP requires 2 arguments")
	}
	s := valueToString(args[0])
	layout := valueToString(args[1])
	t, err := time.Parse(layout, s)
	if err != nil {
		return nil, fmt.Errorf("TO_TIMESTAMP: %w", err)
	}
	return t.Format(time.RFC3339), nil
}

func fnDateAdd(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("DATE_ADD requires 3 arguments: date, amount, unit")
	}
	dateStr := valueToString(args[0])
	t, err := parseTimestamp(dateStr)
	if err != nil {
		return nil, fmt.Errorf("DATE_ADD: %w", err)
	}
	amount, ok := toFloat(args[1])
	if !ok {
		return nil, fmt.Errorf("DATE_ADD: amount must be numeric")
	}
	unit := strings.ToUpper(valueToString(args[2]))
	switch unit {
	case "YEAR":
		t = t.AddDate(int(amount), 0, 0)
	case "MONTH":
		t = t.AddDate(0, int(amount), 0)
	case "DAY":
		t = t.AddDate(0, 0, int(amount))
	case "HOUR":
		t = t.Add(time.Duration(amount) * time.Hour)
	case "MINUTE":
		t = t.Add(time.Duration(amount) * time.Minute)
	case "SECOND":
		t = t.Add(time.Duration(amount) * time.Second)
	default:
		return nil, fmt.Errorf("DATE_ADD: unknown unit %q", unit)
	}
	return t.Format(time.RFC3339), nil
}

func fnDateSub(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("DATE_SUB requires 3 arguments: date, amount, unit")
	}
	dateStr := valueToString(args[0])
	t, err := parseTimestamp(dateStr)
	if err != nil {
		return nil, fmt.Errorf("DATE_SUB: %w", err)
	}
	amount, ok := toFloat(args[1])
	if !ok {
		return nil, fmt.Errorf("DATE_SUB: amount must be numeric")
	}
	unit := strings.ToUpper(valueToString(args[2]))
	switch unit {
	case "YEAR":
		t = t.AddDate(-int(amount), 0, 0)
	case "MONTH":
		t = t.AddDate(0, -int(amount), 0)
	case "DAY":
		t = t.AddDate(0, 0, -int(amount))
	case "HOUR":
		t = t.Add(-time.Duration(amount) * time.Hour)
	case "MINUTE":
		t = t.Add(-time.Duration(amount) * time.Minute)
	case "SECOND":
		t = t.Add(-time.Duration(amount) * time.Second)
	default:
		return nil, fmt.Errorf("DATE_SUB: unknown unit %q", unit)
	}
	return t.Format(time.RFC3339), nil
}

func fnDateDiff(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("DATE_DIFF requires 3 arguments: unit, date1, date2")
	}
	unit := strings.ToUpper(valueToString(args[0]))
	t1, err := parseTimestamp(valueToString(args[1]))
	if err != nil {
		return nil, fmt.Errorf("DATE_DIFF: %w", err)
	}
	t2, err := parseTimestamp(valueToString(args[2]))
	if err != nil {
		return nil, fmt.Errorf("DATE_DIFF: %w", err)
	}
	diff := t2.Sub(t1)
	switch unit {
	case "DAY":
		return int64(diff.Hours() / 24), nil
	case "HOUR":
		return int64(diff.Hours()), nil
	case "MINUTE":
		return int64(diff.Minutes()), nil
	case "SECOND":
		return int64(diff.Seconds()), nil
	case "MONTH":
		return int64(diff.Hours() / 24 / 30), nil
	case "YEAR":
		return int64(diff.Hours() / 24 / 365), nil
	default:
		return nil, fmt.Errorf("DATE_DIFF: unknown unit %q", unit)
	}
}

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
	return generateUUID(), nil
}

func fnInterval(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("INTERVAL requires 1 argument")
	}
	return valueToString(args[0]), nil
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
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("JSONB_BUILD_OBJECT: %w", err)
	}
	return string(data), nil
}

func fnJsonbBuildArray(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	arr := make([]interface{}, len(args))
	for i, arg := range args {
		arr[i] = arg
	}
	data, err := json.Marshal(arr)
	if err != nil {
		return nil, fmt.Errorf("JSONB_BUILD_ARRAY: %w", err)
	}
	return string(data), nil
}

func fnJsonbArrayElements(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("JSONB_ARRAY_ELEMENTS requires 1 argument")
	}
	s := valueToString(args[0])
	var arr []interface{}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, fmt.Errorf("JSONB_ARRAY_ELEMENTS: %w", err)
	}
	result := make([]string, len(arr))
	for i, v := range arr {
		data, _ := json.Marshal(v)
		result[i] = string(data)
	}
	return strings.Join(result, ","), nil
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
	case map[string]interface{}:
		return "object", nil
	case []interface{}:
		return "array", nil
	case float64:
		return "number", nil
	case bool:
		return "boolean", nil
	case nil:
		return "null", nil
	default:
		return "string", nil
	}
}

func fnJsonbSet(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("JSONB_SET requires 3 arguments: jsonb, path, new_value")
	}
	s := valueToString(args[0])
	path := valueToString(args[1])
	newVal := args[2]
	var data interface{}
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		return nil, fmt.Errorf("JSONB_SET: %w", err)
	}
	pathParts := strings.Split(path, ".")
	if len(pathParts) == 1 {
		if m, ok := data.(map[string]interface{}); ok {
			m[pathParts[0]] = newVal
		}
	}
	result, _ := json.Marshal(data)
	return string(result), nil
}

func fnJsonbExtractPath(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSONB_EXTRACT_PATH requires 2 arguments")
	}
	s := valueToString(args[0])
	var data interface{}
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		return nil, fmt.Errorf("JSONB_EXTRACT_PATH: %w", err)
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

var builtinFuncs = map[string]builtinFunc{
	"NOW":                fnNow,
	"UPPER":              fnUpper,
	"LOWER":              fnLower,
	"CONCAT":             fnConcat,
	"LENGTH":             fnLength,
	"LEN":                fnLength,
	"SUBSTRING":          fnSubstring,
	"SUBSTR":             fnSubstring,
	"TRIM":               fnTrim,
	"LTRIM":              fnLtrim,
	"RTRIM":              fnRtrim,
	"REPLACE":            fnReplace,
	"POSITION":           fnPosition,
	"LEFT":               fnLeft,
	"RIGHT":              fnRight,
	"LPAD":               fnLpad,
	"RPAD":               fnRpad,
	"REVERSE":            fnReverse,
	"INITCAP":            fnInitcap,
	"MOD":                fnMod,
	"POWER":              fnPower,
	"POW":                fnPower,
	"SQRT":               fnSqrt,
	"LN":                 fnLn,
	"LOG":                fnLog,
	"LOG10":              fnLog,
	"EXP":                fnExp,
	"SIGN":               fnSign,
	"ABS":                fnAbs,
	"CEIL":               fnCeil,
	"CEILING":            fnCeil,
	"FLOOR":              fnFloor,
	"ROUND":              fnRound,
	"COALESCE":           fnCoalesce,
	"NULLIF":             fnNullif,
	"GREATEST":           fnGreatest,
	"LEAST":              fnLeast,
	"JSON_OBJECT":        fnJsonObject,
	"JSON_ARRAY":         fnJsonArray,
	"JSON_EXTRACT":       fnJsonExtract,
	"CURRENT_DATE":       fnCurrentDate,
	"CURRENT_TIME":       fnCurrentTime,
	"CURRENT_TIMESTAMP":  fnCurrentTimestamp,
	"DATE_TRUNC":         fnDateTrunc,
	"EXTRACT":            fnExtract,
	"ARRAY_APPEND":       fnArrayAppend,
	"ARRAY_LENGTH":       fnArrayLength,
	"ARRAY_CONTAINS":     fnArrayContains,
	"ARRAY_TO_STRING":    fnArrayToString,
	"ARRAY_SLICE":        fnArraySlice,
	"AGE":                fnAge,
	"TO_DATE":            fnToDate,
	"TO_CHAR":            fnToChar,
	"TO_TIMESTAMP":       fnToTimestamp,
	"DATE_ADD":           fnDateAdd,
	"DATE_SUB":           fnDateSub,
	"DATE_DIFF":          fnDateDiff,
	"AI_EMBED":           fnAiEmbed,
	"UUID":               fnUuid,
	"INTERVAL":           fnInterval,
	"JSONB_BUILD_OBJECT": fnJsonbBuildObject,
	"JSONB_BUILD_ARRAY":  fnJsonbBuildArray,
	"JSONB_ARRAY_ELEMENTS": fnJsonbArrayElements,
	"JSONB_TYPEOF":       fnJsonbTypeof,
	"JSONB_SET":          fnJsonbSet,
	"JSONB_EXTRACT_PATH": fnJsonbExtractPath,
}

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
	if ctx != nil && ctx.CurrentDB != nil && *ctx.CurrentDB != "" {
		if result, err := executeUserDefinedFunction(*ctx.CurrentDB, name, args, ctx); err == nil {
			return result, nil
		}
	}
	return nil, fmt.Errorf("unknown function: %s", name)
}

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

func parseJSONArray(s string) []interface{} {
	var arr []interface{}
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil
	}
	return arr
}

func generateUUID() string {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(rand.Intn(256))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

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

func substituteParam(expr parser.Expression, paramName string, paramValue interface{}) parser.Expression {
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		return &parser.BinaryExpr{
			Left:     substituteParam(e.Left, paramName, paramValue),
			Operator: e.Operator,
			Right:    substituteParam(e.Right, paramName, paramValue),
		}
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
	default:
		return expr
	}
}

func evalInExpr(e *parser.InExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	leftVal, err := evalOperand(e.Left, row, schema, ctx)
	if err != nil {
		return nil, err
	}

	// Check if Right has a subquery
	if len(e.Right) == 1 {
		if sub, ok := e.Right[0].(*parser.SubqueryExpr); ok {
			subQuery := *sub.Query
			if row != nil && schema != nil && subQuery.Where != nil && subQuery.TableName != schema.Name {
				subQuery.Where = injectOuterColumns(subQuery.Where, row, schema)
			}
			cmd, err := CommandFactory(&subQuery)
			if err != nil {
				return nil, err
			}
			res, err := cmd.Execute(ctx)
			if err != nil {
				return nil, err
			}

			found := false
			for _, row := range res.Rows {
				if len(row) > 0 {
					var rowVal storage.Value
					if len(res.Schema.Columns) > 0 {
						var err error
						rowVal, err = convertStringToValue(row[0], res.Schema.Columns[0])
						if err != nil {
							rowVal = row[0] // fallback
						}
					} else {
						rowVal = row[0]
					}

					if CompareValues(leftVal, rowVal) == 0 {
						found = true
						break
					}
				}
			}
			if e.Not {
				return !found, nil
			}
			return found, nil
		}
	}

	// Normal IN (val1, val2, ...)
	found := false
	for _, rightExpr := range e.Right {
		rightVal, err := evalOperand(rightExpr, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if CompareValues(leftVal, rightVal) == 0 {
			found = true
			break
		}
	}

	if e.Not {
		return !found, nil
	}
	return found, nil
}

func resolveColumn(row storage.Row, schema *storage.TableSchema, name string) (interface{}, error) {
	for i, column := range schema.Columns {
		// Try exact match (including potential Table.Column in schema)
		if strings.EqualFold(column.Name, name) {
			if i < len(row) {
				return row[i], nil
			}
		}

		// Try matching just the column part if name is unqualified
		if !strings.Contains(name, ".") {
			parts := strings.Split(column.Name, ".")
			if len(parts) == 2 && strings.EqualFold(parts[1], name) {
				if i < len(row) {
					return row[i], nil
				}
			}
		}
	}

	return nil, fmt.Errorf("unknown column '%s'", name)
}

func inferTypeFromExpr(expr parser.Expression, schema *storage.TableSchema) string {
	if expr == nil {
		return "TEXT"
	}
	switch e := expr.(type) {
	case *parser.ColumnRef:
		if schema != nil {
			for _, col := range schema.Columns {
				if strings.EqualFold(col.Name, e.Name) {
					return col.Type
				}
				// Handle Table.Column
				parts := strings.Split(col.Name, ".")
				if len(parts) == 2 && strings.EqualFold(parts[1], e.Name) {
					return col.Type
				}
			}
		}
	case *parser.AggregateExpr:
		switch strings.ToUpper(e.Name) {
		case "COUNT":
			return "INT"
		case "AVG":
			return "FLOAT"
		case "SUM":
			return "FLOAT" // or INT depending on arg, FLOAT is safer
		}
	case *parser.FunctionCall:
		switch strings.ToUpper(e.Name) {
		case "COSINE_SIMILARITY":
			return "FLOAT"
		}
	case parser.Value:
		return inferType(parserValueToRaw(e))
	case *parser.Value:
		return inferType(parserValueToRaw(*e))
	}
	return "TEXT"
}

func parserValueToRaw(value parser.Value) interface{} {
	switch value.Type {
	case "int":
		return value.IntVal
	case "float":
		return value.FltVal
	case "string":
		return value.StrVal
	case "bool":
		return value.BoolVal
	case "null":
		return nil
	default:
		return nil
	}
}

func compareValues(left, right interface{}, op string) (bool, error) {
	if left == nil || right == nil {
		switch op {
		case "=":
			return left == nil && right == nil, nil
		case "!=":
			return !(left == nil && right == nil), nil
		default:
			return false, nil
		}
	}

	if lf, lok := toFloat(left); lok {
		rf, rok := toFloat(right)
		if !rok {
			return false, fmt.Errorf("type mismatch in comparison: %T %s %T", left, op, right)
		}
		return compareOrdered(lf, rf, op)
	}

	switch l := left.(type) {
	case string:
		r, ok := right.(string)
		if !ok {
			return false, fmt.Errorf("type mismatch in comparison: %T %s %T", left, op, right)
		}
		return compareOrdered(l, r, op)
	case bool:
		r, ok := right.(bool)
		if !ok {
			return false, fmt.Errorf("type mismatch in comparison: %T %s %T", left, op, right)
		}
		switch op {
		case "=":
			return l == r, nil
		case "!=":
			return l != r, nil
		default:
			return false, fmt.Errorf("operator '%s' is not supported for BOOL", op)
		}
	default:
		return false, fmt.Errorf("unsupported comparison type %T", left)
	}
}

// CompareValues returns -1 if a < b, 1 if a > b, and 0 if a == b.
// It handles mixed numeric types and NULLs (nil).
func CompareValues(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			if af < bf {
				return -1
			}
			if af > bf {
				return 1
			}
			return 0
		}
	}

	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
	case bool:
		if bv, ok := b.(bool); ok {
			if av == bv {
				return 0
			}
			if !av {
				return -1
			}
			return 1
		}
	}

	return -1
}

// initcap capitalizes the first letter of each word in a string.
func initcap(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		runes := []rune(strings.ToLower(w))
		if r := runes[0]; r >= 'a' && r <= 'z' {
			runes[0] = r - 32
		}
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

func compareOrdered[T ~float64 | ~string](left, right T, op string) (bool, error) {
	switch op {
	case "=":
		return left == right, nil
	case "!=":
		return left != right, nil
	case "<":
		return left < right, nil
	case ">":
		return left > right, nil
	case "<=":
		return left <= right, nil
	case ">=":
		return left >= right, nil
	default:
		return false, fmt.Errorf("unknown operator '%s'", op)
	}
}

func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		if math.IsNaN(v) {
			return 0, false
		}
		return v, true
	default:
		return 0, false
	}
}

func isIntervalString(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.ToUpper(s)
	return strings.Contains(s, "INTERVAL") || strings.HasSuffix(s, "DAYS") ||
		strings.HasSuffix(s, "HOURS") || strings.HasSuffix(s, "MINUTES") ||
		strings.HasSuffix(s, "SECONDS") || strings.HasSuffix(s, "MONTHS") ||
		strings.HasSuffix(s, "YEARS")
}

func evalDateInterval(dateStr, intervalStr, op string) (interface{}, error) {
	t, err := parseTimestamp(dateStr)
	if err != nil {
		return nil, fmt.Errorf("date interval: %w", err)
	}
	intervalStr = strings.TrimSpace(intervalStr)
	intervalStr = strings.TrimPrefix(strings.ToUpper(intervalStr), "INTERVAL")
	intervalStr = strings.TrimSpace(intervalStr)
	intervalStr = strings.Trim(intervalStr, "'\"")

	amount := 1
	parts := strings.Fields(intervalStr)
	if len(parts) >= 2 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			amount = n
		}
	}
	unit := strings.ToUpper(parts[len(parts)-1])
	if strings.HasSuffix(unit, "S") {
		unit = unit[:len(unit)-1]
	}

	switch op {
	case "+":
		switch unit {
		case "DAY":
			t = t.AddDate(0, 0, amount)
		case "HOUR":
			t = t.Add(time.Duration(amount) * time.Hour)
		case "MINUTE":
			t = t.Add(time.Duration(amount) * time.Minute)
		case "SECOND":
			t = t.Add(time.Duration(amount) * time.Second)
		case "MONTH":
			t = t.AddDate(0, amount, 0)
		case "YEAR":
			t = t.AddDate(amount, 0, 0)
		case "WEEK":
			t = t.AddDate(0, 0, amount*7)
		default:
			return nil, fmt.Errorf("unknown interval unit: %s", unit)
		}
	case "-":
		switch unit {
		case "DAY":
			t = t.AddDate(0, 0, -amount)
		case "HOUR":
			t = t.Add(-time.Duration(amount) * time.Hour)
		case "MINUTE":
			t = t.Add(-time.Duration(amount) * time.Minute)
		case "SECOND":
			t = t.Add(-time.Duration(amount) * time.Second)
		case "MONTH":
			t = t.AddDate(0, -amount, 0)
		case "YEAR":
			t = t.AddDate(-amount, 0, 0)
		case "WEEK":
			t = t.AddDate(0, 0, -amount*7)
		default:
			return nil, fmt.Errorf("unknown interval unit: %s", unit)
		}
	}
	return t.Format(time.RFC3339), nil
}

func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

func evalCast(e *parser.CastExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	val, err := evalOperand(e.Expr, row, schema, ctx)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}

	switch strings.ToUpper(e.TargetType) {
	case "INT":
		if i, ok := toInt64(val); ok {
			return i, nil
		}
		if f, ok := toFloat(val); ok {
			return int64(f), nil
		}
		if s, ok := val.(string); ok {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i, nil
			}
		}
	case "FLOAT":
		if f, ok := toFloat(val); ok {
			return f, nil
		}
		if s, ok := val.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, nil
			}
		}
	case "TEXT", "VARCHAR":
		return valueToString(val), nil
	case "BOOL":
		if b, ok := val.(bool); ok {
			return b, nil
		}
		s := strings.ToUpper(valueToString(val))
		if s == "TRUE" || s == "1" {
			return true, nil
		}
		if s == "FALSE" || s == "0" {
			return false, nil
		}
	}

	return nil, fmt.Errorf("cannot cast %T to %s", val, e.TargetType)
}

func evalCase(e *parser.CaseExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	var baseVal interface{}
	var err error
	if e.Base != nil {
		baseVal, err = evalOperand(e.Base, row, schema, ctx)
		if err != nil {
			return nil, err
		}
	}

	for _, when := range e.Whens {
		if baseVal != nil {
			// CASE base WHEN val THEN ...
			whenVal, err := evalOperand(when.Condition, row, schema, ctx)
			if err != nil {
				return nil, err
			}
			if CompareValues(baseVal, whenVal) == 0 {
				return evalOperand(when.Result, row, schema, ctx)
			}
		} else {
			// CASE WHEN cond THEN ...
			match, err := evalExpr(when.Condition, row, schema, ctx)
			if err != nil {
				return nil, err
			}
			if match {
				return evalOperand(when.Result, row, schema, ctx)
			}
		}
	}

	if e.Else != nil {
		return evalOperand(e.Else, row, schema, ctx)
	}

	return nil, nil
}

func evalJsonPath(e *parser.JsonPathExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	left, err := evalOperand(e.Left, row, schema, ctx)
	if err != nil {
		return nil, err
	}
	if left == nil {
		return nil, nil
	}

	var data map[string]interface{}
	switch v := left.(type) {
	case map[string]interface{}:
		data = v
	case string:
		if err := json.Unmarshal([]byte(v), &data); err != nil {
			return nil, nil
		}
	default:
		return nil, nil
	}

	val, ok := data[e.Path]
	if !ok {
		return nil, nil
	}

	if e.Op == "->>" {
		return valueToString(val), nil
	}
	return val, nil
}

// parseTimestamp пытается распарсить строку как timestamp в различных форматах.
func parseTimestamp(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"01/02/2006 15:04:05",
		"01/02/2006",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse timestamp %q", s)
}

// evalBetweenExpr вычисляет BETWEEN ... AND ... выражение.
func evalBetweenExpr(e *parser.BetweenExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	val, err := evalOperand(e.Expr, row, schema, ctx)
	if err != nil {
		return false, err
	}

	lower, err := evalOperand(e.Lower, row, schema, ctx)
	if err != nil {
		return false, err
	}

	upper, err := evalOperand(e.Upper, row, schema, ctx)
	if err != nil {
		return false, err
	}

	// val >= lower && val <= upper
	cmpLower, err := compareValues(val, lower, ">=")
	if err != nil {
		return false, err
	}

	cmpUpper, err := compareValues(val, upper, "<=")
	if err != nil {
		return false, err
	}

	result := cmpLower && cmpUpper
	if e.Not {
		result = !result
	}
	return result, nil
}

// evalExistsExpr вычисляет EXISTS/NOT EXISTS выражение.
func evalExistsExpr(e *parser.ExistsExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	if e.Select == nil {
		return false, fmt.Errorf("EXISTS: subquery is nil")
	}

	subQuery := *e.Select
	if row != nil && schema != nil && subQuery.Where != nil && subQuery.TableName != schema.Name {
		subQuery.Where = injectOuterColumns(subQuery.Where, row, schema)
	}

	cmd, err := CommandFactory(&subQuery)
	if err != nil {
		return false, fmt.Errorf("EXISTS: %w", err)
	}

	res, err := cmd.Execute(ctx)
	if err != nil {
		return false, fmt.Errorf("EXISTS: %w", err)
	}

	exists := len(res.Rows) > 0
	if e.Not {
		exists = !exists
	}
	return exists, nil
}

func evalComparisonSubquery(e *parser.ComparisonSubqueryExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	leftVal, err := evalOperand(e.Left, row, schema, ctx)
	if err != nil {
		return false, err
	}

	subQuery := *e.Subquery
	if row != nil && schema != nil && subQuery.Where != nil {
		subQuery.Where = injectOuterColumns(subQuery.Where, row, schema)
	}

	cmd, err := CommandFactory(&subQuery)
	if err != nil {
		return false, err
	}
	res, err := cmd.Execute(ctx)
	if err != nil {
		return false, err
	}

	values := make([]interface{}, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 0 {
			val, err := convertStringToValue(r[0], storage.ColumnSchema{Type: "TEXT"})
			if err == nil {
				values = append(values, val)
			} else {
				values = append(values, r[0])
			}
		}
	}

	switch e.Quantifier {
	case "ALL":
		for _, v := range values {
			cmp, err := compareValues(leftVal, v, e.Operator)
			if err != nil {
				return false, err
			}
			if !cmp {
				return false, nil
			}
		}
		return true, nil
	case "ANY", "SOME":
		for _, v := range values {
			cmp, err := compareValues(leftVal, v, e.Operator)
			if err != nil {
				return false, err
			}
			if cmp {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("unknown quantifier: %s", e.Quantifier)
}
