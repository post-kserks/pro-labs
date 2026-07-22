package index

import (
	"errors"
)

// ExtractExpressionKey evaluates a generic AST expression on a tuple to extract the indexable key.
// Following YAGNI/KISS: only a stub is provided until complete AST resolution is integrated.
func ExtractExpressionKey(expr interface{}, row IndexableRow) (interface{}, error) {
	if expr == nil {
		return nil, errors.New("expression is nil")
	}
	return nil, errors.New("expression evaluation not implemented")
}
