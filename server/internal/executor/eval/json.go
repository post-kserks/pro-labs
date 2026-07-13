package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"vaultdb/internal/ai"
	"vaultdb/internal/fts"
	"vaultdb/internal/storage"
)

const (
	ftsScoreThreshold      = 0.1
	semanticMatchThreshold = 0.7
)

// EvalFtsMatch performs full-text search (FTS_MATCH and @@).
func EvalFtsMatch(left, right interface{}) (bool, error) {
	return EvalFtsMatchScored(ValueToString(left), ValueToString(right)) > ftsScoreThreshold, nil
}

// EvalFtsMatchScored computes the full-text match score.
func EvalFtsMatchScored(text, query string) float64 {
	queryTerms := fts.Tokenize(query)
	if len(queryTerms) == 0 {
		return 1.0
	}

	textTerms := fts.Tokenize(text)
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

// EvalJsonContains checks if a JSON object/array contains all keys/elements of another.
func EvalJsonContains(left, right interface{}) (interface{}, error) {
	leftStr := ValueToString(left)
	rightStr := ValueToString(right)
	rawLeft, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSON contains: left is not valid JSON")
	}
	rawRight, err := storage.DecodeJSON([]byte(rightStr))
	if err != nil {
		return nil, fmt.Errorf("JSON contains: right is not valid JSON")
	}

	if leftObj, lok := rawLeft.(map[string]interface{}); lok {
		rightObj, rok := rawRight.(map[string]interface{})
		if !rok {
			return nil, fmt.Errorf("JSON contains: right is not JSON object")
		}
		for k, rv := range rightObj {
			lv, ok := leftObj[k]
			if !ok {
				return false, nil
			}
			if !jsonValuesEqual(lv, rv) {
				return false, nil
			}
		}
		return true, nil
	}

	leftArr, lok := rawLeft.([]interface{})
	if !lok {
		return nil, fmt.Errorf("JSON contains: left is not JSON array or object")
	}
	rightArr, rok := rawRight.([]interface{})
	if !rok {
		return nil, fmt.Errorf("JSON contains: right is not JSON array or object")
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

func jsonValuesEqual(a, b interface{}) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// EvalJsonContainedBy checks if a JSON object/array is contained within another.
func EvalJsonContainedBy(left, right interface{}) (interface{}, error) {
	leftStr := ValueToString(left)
	rightStr := ValueToString(right)
	rawLeft, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSON contained by: left is not valid JSON")
	}
	rawRight, err := storage.DecodeJSON([]byte(rightStr))
	if err != nil {
		return nil, fmt.Errorf("JSON contained by: right is not valid JSON")
	}

	if leftObj, lok := rawLeft.(map[string]interface{}); lok {
		rightObj, rok := rawRight.(map[string]interface{})
		if !rok {
			return nil, fmt.Errorf("JSON contained by: right is not JSON object")
		}
		for k, lv := range leftObj {
			rv, ok := rightObj[k]
			if !ok {
				return false, nil
			}
			if !jsonValuesEqual(lv, rv) {
				return false, nil
			}
		}
		return true, nil
	}

	leftArr, lok := rawLeft.([]interface{})
	if !lok {
		return nil, fmt.Errorf("JSON contained by: left is not JSON array or object")
	}
	rightArr, rok := rawRight.([]interface{})
	if !rok {
		return nil, fmt.Errorf("JSON contained by: right is not JSON array or object")
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

// EvalJsonHasKey checks for the presence of a key in a JSON object.
func EvalJsonHasKey(left, right interface{}) (interface{}, error) {
	leftStr := ValueToString(left)
	key := ValueToString(right)
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

// EvalJsonMerge merges two JSON objects.
func EvalJsonMerge(left, right interface{}) (interface{}, error) {
	leftStr := ValueToString(left)
	rightStr := ValueToString(right)
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

// EvalOperandRaw extracts raw value from parser expression.
func EvalOperandRaw(val interface{}) interface{} {
	// This is a helper that just returns the value as-is for expressions
	// that have already been evaluated to raw values.
	return val
}

// EvalSemanticMatch compares operands by cosine similarity.
func EvalSemanticMatch(left, right interface{}, ctx interface{}) (bool, error) {
	v1, err := OperandVector(left, ctx)
	if err != nil {
		return false, fmt.Errorf("SEMANTIC_MATCH: %w", err)
	}
	v2, err := OperandVector(right, ctx)
	if err != nil {
		return false, fmt.Errorf("SEMANTIC_MATCH: %w", err)
	}

	sim := CosineSimilarity(v1, v2)
	return sim > semanticMatchThreshold, nil
}

// OperandVector converts an operand to a vector.
func OperandVector(val interface{}, ctx interface{}) ([]float64, error) {
	if v, err := ToVector(val); err == nil {
		return v, nil
	}
	var embedder ai.Embedder
	var goCtx context.Context
	if ep, ok := ctx.(EmbedderProvider); ok {
		embedder = ep.GetEmbedder()
		goCtx = ep.GetGoContext()
	}
	return EmbedText(embedder, goCtx, ValueToString(val))
}

// ToVector converts a value to a float64 vector.
func ToVector(val interface{}) ([]float64, error) {
	switch v := val.(type) {
	case []float64:
		return v, nil
	case []interface{}:
		res := make([]float64, len(v))
		for i, x := range v {
			if f, ok := ToFloat(x); ok {
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

// CosineSimilarity computes cosine similarity between two vectors.
func CosineSimilarity(v1, v2 []float64) float64 {
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
