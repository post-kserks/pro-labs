package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

const (
	ftsScoreThreshold      = 0.1
	semanticMatchThreshold = 0.7
	embeddingTimeout       = 10 * time.Second
)

// evalFtsMatch выполняет полнотекстовый поиск (FTS_MATCH и @@).
func evalFtsMatch(left, right interface{}) (bool, error) {
	return evalFtsMatchScored(valueToString(left), valueToString(right)) > ftsScoreThreshold, nil
}

// evalFtsMatchScored вычисляет score полнотекстового совпадения.
func evalFtsMatchScored(text, query string) float64 {
	text = strings.ToLower(text)
	query = strings.ToLower(query)

	queryTerms := strings.Fields(query)
	if len(queryTerms) == 0 {
		return 1.0
	}

	textTerms := strings.Fields(text)
	if len(textTerms) == 0 {
		return 0.0
	}

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
	return score
}

// evalJsonContains проверяет содержит ли JSON массив все элементы другого массива.
func evalJsonContains(left, right interface{}) (interface{}, error) {
	leftStr := valueToString(left)
	rightStr := valueToString(right)
	rawLeft, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSON contains: left is not JSON array")
	}
	leftArr, ok := rawLeft.([]interface{})
	if !ok {
		return nil, fmt.Errorf("JSON contains: left is not JSON array")
	}
	rawRight, err := storage.DecodeJSON([]byte(rightStr))
	if err != nil {
		return nil, fmt.Errorf("JSON contains: right is not JSON array")
	}
	rightArr, ok := rawRight.([]interface{})
	if !ok {
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

// evalJsonContainedBy проверяет содержится ли JSON массив внутри другого.
func evalJsonContainedBy(left, right interface{}) (interface{}, error) {
	leftStr := valueToString(left)
	rightStr := valueToString(right)
	rawLeft, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSON contained by: left is not JSON array")
	}
	leftArr, ok := rawLeft.([]interface{})
	if !ok {
		return nil, fmt.Errorf("JSON contained by: left is not JSON array")
	}
	rawRight, err := storage.DecodeJSON([]byte(rightStr))
	if err != nil {
		return nil, fmt.Errorf("JSON contained by: right is not JSON array")
	}
	rightArr, ok := rawRight.([]interface{})
	if !ok {
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

// evalJsonHasKey проверяет наличие ключа в JSON объекте.
func evalJsonHasKey(left, right interface{}) (interface{}, error) {
	leftStr := valueToString(left)
	key := valueToString(right)
	raw, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSON has key: left is not JSON object")
	}
	data, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("JSON has key: left is not JSON object")
	}
	_, exists := data[key]
	return exists, nil
}

// evalJsonMerge объединяет два JSON объекта.
func evalJsonMerge(left, right interface{}) (interface{}, error) {
	leftStr := valueToString(left)
	rightStr := valueToString(right)
	rawLeft, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSON merge: left is not JSON object")
	}
	leftObj, ok := rawLeft.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("JSON merge: left is not JSON object")
	}
	rawRight, err := storage.DecodeJSON([]byte(rightStr))
	if err != nil {
		return nil, fmt.Errorf("JSON merge: right is not JSON object")
	}
	rightObj, ok := rawRight.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("JSON merge: right is not JSON object")
	}
	for k, v := range rightObj {
		leftObj[k] = v
	}
	data, _ := json.Marshal(leftObj)
	return string(data), nil
}

// evalOperandRaw извлекает raw value из parser expression.
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

// evalSemanticMatch сравнивает операнды по косинусной близости.
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
	return sim > semanticMatchThreshold, nil
}

// operandVector превращает операнд в вектор.
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
	embedCtx, cancel := context.WithTimeout(context.Background(), embeddingTimeout)
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

// evalJsonPath вычисляет JSON path выражение (->, ->>).
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
		raw, err := storage.DecodeJSON([]byte(v))
		if err != nil {
			return nil, nil
		}
		data, _ = raw.(map[string]interface{})
		if data == nil {
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
