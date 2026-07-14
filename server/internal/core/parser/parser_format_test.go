package parser

import (
	"testing"
)

func TestFormatExpressionRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name:     "ColumnRef simple",
			expr:     &ColumnRef{Name: "id"},
			expected: "id",
		},
		{
			name:     "ColumnRef with table",
			expr:     &ColumnRef{Name: "id", Table: "t"},
			expected: "t.id",
		},
		{
			name:     "ColumnRef with old table",
			expr:     &ColumnRef{Name: "val", Table: "old"},
			expected: "old.val",
		},
		{
			name:     "ColumnRef with new table",
			expr:     &ColumnRef{Name: "val", Table: "new"},
			expected: "new.val",
		},
		{
			name: "BinaryExpr",
			expr: &BinaryExpr{
				Left:     &ColumnRef{Name: "x"},
				Operator: ">",
				Right:    &Value{Type: "int", IntVal: 5},
			},
			expected: "x > 5",
		},
		{
			name: "AndExpr",
			expr: &AndExpr{
				Left:  &ColumnRef{Name: "a"},
				Right: &ColumnRef{Name: "b"},
			},
			expected: "(a AND b)",
		},
		{
			name: "OrExpr",
			expr: &OrExpr{
				Left:  &ColumnRef{Name: "a"},
				Right: &ColumnRef{Name: "b"},
			},
			expected: "(a OR b)",
		},
		{
			name:     "NotExpr",
			expr:     &NotExpr{Expr: &ColumnRef{Name: "alive"}},
			expected: "NOT alive",
		},
		{
			name: "InExpr",
			expr: &InExpr{
				Left: &ColumnRef{Name: "status"},
				Not:  false,
				Right: []Expression{
					&Value{Type: "string", StrVal: "active"},
					&Value{Type: "string", StrVal: "pending"},
				},
			},
			expected: "status IN ('active', 'pending')",
		},
		{
			name: "InExpr NOT",
			expr: &InExpr{
				Left: &ColumnRef{Name: "status"},
				Not:  true,
				Right: []Expression{
					&Value{Type: "string", StrVal: "archived"},
				},
			},
			expected: "status NOT IN ('archived')",
		},
		{
			name:     "ParamRef",
			expr:     &ParamRef{Index: 1},
			expected: "$1",
		},
		{
			name:     "ParamRef index 3",
			expr:     &ParamRef{Index: 3},
			expected: "$3",
		},
		{
			name: "FunctionCall no args",
			expr: &FunctionCall{
				Name: "NOW",
				Args: []Expression{},
			},
			expected: "NOW()",
		},
		{
			name: "FunctionCall with args",
			expr: &FunctionCall{
				Name: "COALESCE",
				Args: []Expression{
					&ColumnRef{Name: "a"},
					&Value{Type: "int", IntVal: 0},
				},
			},
			expected: "COALESCE(a, 0)",
		},
		{
			name: "AggregateExpr COUNT",
			expr: &AggregateExpr{
				Name:     "COUNT",
				Args:     []Expression{&ColumnRef{Name: "*"}},
				Distinct: false,
			},
			expected: "COUNT(*)",
		},
		{
			name: "AggregateExpr COUNT DISTINCT",
			expr: &AggregateExpr{
				Name:     "COUNT",
				Args:     []Expression{&ColumnRef{Name: "id"}},
				Distinct: true,
			},
			expected: "COUNT(DISTINCT id)",
		},
		{
			name: "AggregateExpr SUM",
			expr: &AggregateExpr{
				Name:     "SUM",
				Args:     []Expression{&ColumnRef{Name: "amount"}},
				Distinct: false,
			},
			expected: "SUM(amount)",
		},
		{
			name: "CastExpr",
			expr: &CastExpr{
				Expr:       &ColumnRef{Name: "val"},
				TargetType: "INT",
			},
			expected: "CAST(val AS INT)",
		},
		{
			name: "CastExpr VARCHAR",
			expr: &CastExpr{
				Expr:       &ColumnRef{Name: "num"},
				TargetType: "VARCHAR(50)",
			},
			expected: "CAST(num AS VARCHAR(50))",
		},
		{
			name: "CaseExpr full",
			expr: &CaseExpr{
				Base: &ColumnRef{Name: "status"},
				Whens: []CaseWhen{
					{
						Condition: &Value{Type: "string", StrVal: "active"},
						Result:    &Value{Type: "string", StrVal: "Active"},
					},
					{
						Condition: &Value{Type: "string", StrVal: "inactive"},
						Result:    &Value{Type: "string", StrVal: "Inactive"},
					},
				},
				Else: &Value{Type: "string", StrVal: "Unknown"},
			},
			expected: "CASE status WHEN 'active' THEN 'Active' WHEN 'inactive' THEN 'Inactive' ELSE 'Unknown' END",
		},
		{
			name: "CaseExpr no base",
			expr: &CaseExpr{
				Base: nil,
				Whens: []CaseWhen{
					{
						Condition: &BinaryExpr{
							Left:     &ColumnRef{Name: "x"},
							Operator: ">",
							Right:    &Value{Type: "int", IntVal: 0},
						},
						Result: &Value{Type: "string", StrVal: "positive"},
					},
				},
				Else: &Value{Type: "string", StrVal: "non-positive"},
			},
			expected: "CASE WHEN x > 0 THEN 'positive' ELSE 'non-positive' END",
		},
		{
			name: "BetweenExpr",
			expr: &BetweenExpr{
				Expr:  &ColumnRef{Name: "age"},
				Lower: &Value{Type: "int", IntVal: 18},
				Upper: &Value{Type: "int", IntVal: 65},
				Not:   false,
			},
			expected: "age BETWEEN 18 AND 65",
		},
		{
			name: "BetweenExpr NOT",
			expr: &BetweenExpr{
				Expr:  &ColumnRef{Name: "age"},
				Lower: &Value{Type: "int", IntVal: 0},
				Upper: &Value{Type: "int", IntVal: 17},
				Not:   true,
			},
			expected: "age NOT BETWEEN 0 AND 17",
		},
		{
			name: "JsonPathExpr arrow",
			expr: &JsonPathExpr{
				Left: &ColumnRef{Name: "data"},
				Op:   "->",
				Path: "key",
			},
			expected: "data->'key'",
		},
		{
			name: "JsonPathExpr arrow text",
			expr: &JsonPathExpr{
				Left: &ColumnRef{Name: "json_col"},
				Op:   "->>",
				Path: "nested.field",
			},
			expected: "json_col->>'nested.field'",
		},
		{
			name: "ComparisonSubqueryExpr ALL",
			expr: &ComparisonSubqueryExpr{
				Left:       &ColumnRef{Name: "score"},
				Operator:   ">",
				Quantifier: "ALL",
				Subquery:   &SelectStatement{},
			},
			expected: "score > ALL (SUBQUERY)",
		},
		{
			name: "ComparisonSubqueryExpr ANY",
			expr: &ComparisonSubqueryExpr{
				Left:       &ColumnRef{Name: "val"},
				Operator:   "=",
				Quantifier: "ANY",
				Subquery:   &SelectStatement{},
			},
			expected: "val = ANY (SUBQUERY)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionColumnRefTableQualifier(t *testing.T) {
	// Verify that ColumnRef with Table qualifier formats correctly
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name:     "no table",
			expr:     &ColumnRef{Name: "id"},
			expected: "id",
		},
		{
			name:     "table t",
			expr:     &ColumnRef{Name: "id", Table: "t"},
			expected: "t.id",
		},
		{
			name:     "table old",
			expr:     &ColumnRef{Name: "val", Table: "old"},
			expected: "old.val",
		},
		{
			name:     "table new",
			expr:     &ColumnRef{Name: "val", Table: "new"},
			expected: "new.val",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionAggregateExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name:     "COUNT(*)",
			expr:     &AggregateExpr{Name: "COUNT", Args: []Expression{&ColumnRef{Name: "*"}}, Distinct: false},
			expected: "COUNT(*)",
		},
		{
			name:     "COUNT(DISTINCT col)",
			expr:     &AggregateExpr{Name: "COUNT", Args: []Expression{&ColumnRef{Name: "id"}}, Distinct: true},
			expected: "COUNT(DISTINCT id)",
		},
		{
			name:     "SUM(col)",
			expr:     &AggregateExpr{Name: "SUM", Args: []Expression{&ColumnRef{Name: "amount"}}, Distinct: false},
			expected: "SUM(amount)",
		},
		{
			name:     "AVG(col)",
			expr:     &AggregateExpr{Name: "AVG", Args: []Expression{&ColumnRef{Name: "score"}}, Distinct: false},
			expected: "AVG(score)",
		},
		{
			name:     "MAX(col)",
			expr:     &AggregateExpr{Name: "MAX", Args: []Expression{&ColumnRef{Name: "price"}}, Distinct: false},
			expected: "MAX(price)",
		},
		{
			name:     "MIN(col)",
			expr:     &AggregateExpr{Name: "MIN", Args: []Expression{&ColumnRef{Name: "ts"}}, Distinct: false},
			expected: "MIN(ts)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionFunctionCall(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name:     "NOW()",
			expr:     &FunctionCall{Name: "NOW", Args: []Expression{}},
			expected: "NOW()",
		},
		{
			name:     "COALESCE(a, b)",
			expr:     &FunctionCall{Name: "COALESCE", Args: []Expression{&ColumnRef{Name: "a"}, &ColumnRef{Name: "b"}}},
			expected: "COALESCE(a, b)",
		},
		{
			name:     "LENGTH(col)",
			expr:     &FunctionCall{Name: "LENGTH", Args: []Expression{&ColumnRef{Name: "name"}}},
			expected: "LENGTH(name)",
		},
		{
			name: "nested function",
			expr: &FunctionCall{
				Name: "UPPER",
				Args: []Expression{
					&FunctionCall{Name: "TRIM", Args: []Expression{&ColumnRef{Name: "val"}}},
				},
			},
			expected: "UPPER(TRIM(val))",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionWindowFunctionExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name: "ROW_NUMBER() OVER (PARTITION BY dept)",
			expr: &WindowFunctionExpr{
				FuncName: "ROW_NUMBER",
				Args:     []Expression{},
				Over: WindowSpec{
					PartitionBy: []Expression{&ColumnRef{Name: "dept"}},
				},
			},
			expected: "ROW_NUMBER() OVER (PARTITION BY dept)",
		},
		{
			name: "SUM(col) OVER (PARTITION BY dept ORDER BY id)",
			expr: &WindowFunctionExpr{
				FuncName: "SUM",
				Args:     []Expression{&ColumnRef{Name: "amount"}},
				Over: WindowSpec{
					PartitionBy: []Expression{&ColumnRef{Name: "dept"}},
					OrderBy:     []OrderItem{{Expr: &ColumnRef{Name: "id"}, Direction: "ASC"}},
				},
			},
			expected: "SUM(amount) OVER (PARTITION BY dept ORDER BY id ASC)",
		},
		{
			name: "RANK() OVER (ORDER BY score DESC)",
			expr: &WindowFunctionExpr{
				FuncName: "RANK",
				Args:     []Expression{},
				Over: WindowSpec{
					OrderBy: []OrderItem{{Expr: &ColumnRef{Name: "score"}, Direction: "DESC"}},
				},
			},
			expected: "RANK() OVER (ORDER BY score DESC)",
		},
		{
			name: "LAG(col) OVER (ORDER BY ts) without explicit order direction",
			expr: &WindowFunctionExpr{
				FuncName: "LAG",
				Args:     []Expression{&ColumnRef{Name: "val"}},
				Over: WindowSpec{
					OrderBy: []OrderItem{{Expr: &ColumnRef{Name: "ts"}}},
				},
			},
			expected: "LAG(val) OVER (ORDER BY ts)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionCaseExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name: "simple CASE with base",
			expr: &CaseExpr{
				Base: &ColumnRef{Name: "type"},
				Whens: []CaseWhen{
					{Condition: &Value{Type: "string", StrVal: "A"}, Result: &Value{Type: "string", StrVal: "Alpha"}},
					{Condition: &Value{Type: "string", StrVal: "B"}, Result: &Value{Type: "string", StrVal: "Beta"}},
				},
				Else: &Value{Type: "string", StrVal: "Other"},
			},
			expected: "CASE type WHEN 'A' THEN 'Alpha' WHEN 'B' THEN 'Beta' ELSE 'Other' END",
		},
		{
			name: "searched CASE without base",
			expr: &CaseExpr{
				Base: nil,
				Whens: []CaseWhen{
					{
						Condition: &BinaryExpr{
							Left:     &ColumnRef{Name: "x"},
							Operator: ">",
							Right:    &Value{Type: "int", IntVal: 0},
						},
						Result: &Value{Type: "string", StrVal: "positive"},
					},
				},
				Else: &Value{Type: "string", StrVal: "non-positive"},
			},
			expected: "CASE WHEN x > 0 THEN 'positive' ELSE 'non-positive' END",
		},
		{
			name: "CASE with no ELSE",
			expr: &CaseExpr{
				Base: &ColumnRef{Name: "status"},
				Whens: []CaseWhen{
					{Condition: &Value{Type: "string", StrVal: "OK"}, Result: &Value{Type: "string", StrVal: "Good"}},
				},
				Else: nil,
			},
			expected: "CASE status WHEN 'OK' THEN 'Good' END",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionBetweenExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name: "BETWEEN",
			expr: &BetweenExpr{
				Expr:  &ColumnRef{Name: "age"},
				Lower: &Value{Type: "int", IntVal: 18},
				Upper: &Value{Type: "int", IntVal: 65},
				Not:   false,
			},
			expected: "age BETWEEN 18 AND 65",
		},
		{
			name: "NOT BETWEEN",
			expr: &BetweenExpr{
				Expr:  &ColumnRef{Name: "score"},
				Lower: &Value{Type: "int", IntVal: 0},
				Upper: &Value{Type: "int", IntVal: 100},
				Not:   true,
			},
			expected: "score NOT BETWEEN 0 AND 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionJsonPathExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name: "arrow",
			expr: &JsonPathExpr{
				Left: &ColumnRef{Name: "data"},
				Op:   "->",
				Path: "key",
			},
			expected: "data->'key'",
		},
		{
			name: "arrow text",
			expr: &JsonPathExpr{
				Left: &ColumnRef{Name: "json_col"},
				Op:   "->>",
				Path: "nested.field",
			},
			expected: "json_col->>'nested.field'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionExistsExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name:     "EXISTS",
			expr:     &ExistsExpr{Select: &SelectStatement{}, Not: false},
			expected: "EXISTS (SELECT)",
		},
		{
			name:     "NOT EXISTS",
			expr:     &ExistsExpr{Select: &SelectStatement{}, Not: true},
			expected: "NOT EXISTS (SELECT)",
		},
		{
			name:     "EXISTS nil select",
			expr:     &ExistsExpr{Select: nil, Not: false},
			expected: "EXISTS (SUBQUERY)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionSubqueryExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name:     "subquery select",
			expr:     &SubqueryExpr{Query: &SelectStatement{}},
			expected: "(SELECT)",
		},
		{
			name:     "subquery nil",
			expr:     &SubqueryExpr{Query: nil},
			expected: "(SUBQUERY)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionComparisonSubqueryExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name: "ALL",
			expr: &ComparisonSubqueryExpr{
				Left:       &ColumnRef{Name: "score"},
				Operator:   ">",
				Quantifier: "ALL",
				Subquery:   &SelectStatement{},
			},
			expected: "score > ALL (SUBQUERY)",
		},
		{
			name: "ANY",
			expr: &ComparisonSubqueryExpr{
				Left:       &ColumnRef{Name: "val"},
				Operator:   "=",
				Quantifier: "ANY",
				Subquery:   &SelectStatement{},
			},
			expected: "val = ANY (SUBQUERY)",
		},
		{
			name: "SOME",
			expr: &ComparisonSubqueryExpr{
				Left:       &ColumnRef{Name: "id"},
				Operator:   "<",
				Quantifier: "SOME",
				Subquery:   &SelectStatement{},
			},
			expected: "id < SOME (SUBQUERY)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionCastExpr(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name:     "CAST to INT",
			expr:     &CastExpr{Expr: &ColumnRef{Name: "val"}, TargetType: "INT"},
			expected: "CAST(val AS INT)",
		},
		{
			name:     "CAST to VARCHAR(50)",
			expr:     &CastExpr{Expr: &ColumnRef{Name: "num"}, TargetType: "VARCHAR(50)"},
			expected: "CAST(num AS VARCHAR(50))",
		},
		{
			name:     "CAST to FLOAT",
			expr:     &CastExpr{Expr: &Value{Type: "int", IntVal: 42}, TargetType: "FLOAT"},
			expected: "CAST(42 AS FLOAT)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionParamRef(t *testing.T) {
	tests := []struct {
		name     string
		expr     Expression
		expected string
	}{
		{
			name:     "$1",
			expr:     &ParamRef{Index: 1},
			expected: "$1",
		},
		{
			name:     "$2",
			expr:     &ParamRef{Index: 2},
			expected: "$2",
		},
		{
			name:     "$10",
			expr:     &ParamRef{Index: 10},
			expected: "$10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatExpression(tt.expr)
			if result != tt.expected {
				t.Errorf("FormatExpression() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatExpressionNilInput(t *testing.T) {
	result := FormatExpression(nil)
	if result != "" {
		t.Errorf("FormatExpression(nil) = %q, want empty string", result)
	}
}

func TestFormatExpressionComplex(t *testing.T) {
	// Test nested expression: (a > 1 AND b < 10) OR c = 5
	expr := &OrExpr{
		Left: &AndExpr{
			Left: &BinaryExpr{
				Left:     &ColumnRef{Name: "a"},
				Operator: ">",
				Right:    &Value{Type: "int", IntVal: 1},
			},
			Right: &BinaryExpr{
				Left:     &ColumnRef{Name: "b"},
				Operator: "<",
				Right:    &Value{Type: "int", IntVal: 10},
			},
		},
		Right: &BinaryExpr{
			Left:     &ColumnRef{Name: "c"},
			Operator: "=",
			Right:    &Value{Type: "int", IntVal: 5},
		},
	}
	result := FormatExpression(expr)
	expected := "((a > 1 AND b < 10) OR c = 5)"
	if result != expected {
		t.Errorf("FormatExpression() = %q, want %q", result, expected)
	}
}
